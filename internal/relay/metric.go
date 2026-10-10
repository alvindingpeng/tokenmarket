package relay

import (
	"sort"
	"sync"
)

// memberMetricKey 定位一个分组成员的运行指标。
type memberMetricKey struct {
	groupID int
	itemID  int
}

// memberMetric 成员的运行指标: 首响应耗时与成功率的指数滑动平均(EMA)。
// 只存进程内: 指标仅服务于选路, 重启后从冷启动重新积累, 跨分组同名成员互不影响。
type memberMetric struct {
	emaWaitMs  float64 // 首响应耗时 EMA, 毫秒; 流式为首帧, 非流式为完整响应。
	emaSuccess float64 // 成功率 EMA, 0..1。
	samples    int     // 已累计样本数, 0 表示冷成员(尚无任何请求结果)。
}

// metricAlpha 根据配置的样本窗口计算 EMA 新样本占比; window 越大越平滑。
func metricAlpha(window int) float64 {
	if window < 5 {
		window = 5
	}
	if window > 200 {
		window = 200
	}
	return 2 / float64(window+1)
}

var (
	metricMu      sync.Mutex
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	// modelMetrics 按上游渠道模型聚合的全局指标, 是综合评分的权威来源。
	// 评分以「上游模型」为单位: 同一渠道模型在任意分组、任意用户下共享同一份成绩,
	// 故某模型表现差只降它自己的名次, 不牵连同渠道其它模型, 打分结果也天然对所有用户一致。
	modelMetrics = make(map[int]*memberMetric)
)

// applySample 把一次观测并入 EMA。
func applySample(metric *memberMetric, window int, waitMs int64, success bool) {
	s := 0.0
	if success {
		s = 1
	}
	if metric.samples == 0 {
		metric.emaWaitMs = float64(waitMs)
		metric.emaSuccess = s
	} else {
		alpha := metricAlpha(window)
		metric.emaWaitMs = alpha*float64(waitMs) + (1-alpha)*metric.emaWaitMs
		metric.emaSuccess = alpha*s + (1-alpha)*metric.emaSuccess
	}
	metric.samples++
}

// recordMemberMetric 记录一次成员请求结果, 供延迟/成功率/综合评分策略排名使用。
// channelModelID 非零时同时并入该上游模型的全局指标, 使打分跨分组、跨用户一致。
func recordMemberMetric(groupID, itemID, channelModelID, window int, waitMs int64, success bool) {
	metricMu.Lock()
	defer metricMu.Unlock()

	key := memberMetricKey{groupID: groupID, itemID: itemID}
	metric := memberMetrics[key]
	if metric == nil {
		metric = &memberMetric{}
		memberMetrics[key] = metric
	}
	applySample(metric, window, waitMs, success)

	if channelModelID != 0 {
		global := modelMetrics[channelModelID]
		if global == nil {
			global = &memberMetric{}
			modelMetrics[channelModelID] = global
		}
		applySample(global, window, waitMs, success)
	}
	markMetricDirty(groupID, itemID)
}

// memberMetricOf 返回成员指标快照; samples 为 0 表示冷成员。
func memberMetricOf(groupID, itemID int) (waitMs float64, success float64, samples int) {
	metricMu.Lock()
	defer metricMu.Unlock()
	metric := memberMetrics[memberMetricKey{groupID: groupID, itemID: itemID}]
	if metric == nil {
		return 0, 0, 0
	}
	return metric.emaWaitMs, metric.emaSuccess, metric.samples
}

// modelMetricOf 返回上游渠道模型的全局指标快照; samples 为 0 表示该模型尚无观测。
func modelMetricOf(channelModelID int) (waitMs float64, success float64, samples int) {
	if channelModelID == 0 {
		return 0, 0, 0
	}
	metricMu.Lock()
	defer metricMu.Unlock()
	metric := modelMetrics[channelModelID]
	if metric == nil {
		return 0, 0, 0
	}
	return metric.emaWaitMs, metric.emaSuccess, metric.samples
}

// seedModelMetric 恢复持久化指标时并入一条历史观测, 保持全局模型指标的连续性。
func seedModelMetric(channelModelID int, waitMs, success float64, samples int) {
	if channelModelID == 0 || samples <= 0 {
		return
	}
	metricMu.Lock()
	defer metricMu.Unlock()
	metric := modelMetrics[channelModelID]
	if metric == nil || metric.samples < samples {
		modelMetrics[channelModelID] = &memberMetric{emaWaitMs: waitMs, emaSuccess: success, samples: samples}
	}
}

// MemberMetricView 是分组成员指标的可视快照, 供分组页展示。
type MemberMetricView struct {
	ItemID  int     `json:"item_id"` // 分组成员 ID, 与分组条目的 id 对应。
	WaitMs  float64 `json:"wait_ms"` // 首响应耗时 EMA, 毫秒。
	Success float64 `json:"success"` // 成功率 EMA, 0..1。
	Samples int     `json:"samples"` // 已累计样本数。
}

// MemberMetricViews 返回指定分组在给定成员集合内的指标快照, 无样本的成员不返回。
func MemberMetricViews(groupID int, itemIDs []int) []MemberMetricView {
	allowed := make(map[int]struct{}, len(itemIDs))
	for _, id := range itemIDs {
		allowed[id] = struct{}{}
	}
	metricMu.Lock()
	defer metricMu.Unlock()
	views := make([]MemberMetricView, 0, len(itemIDs))
	for key, metric := range memberMetrics {
		if key.groupID != groupID {
			continue
		}
		if _, ok := allowed[key.itemID]; !ok {
			continue
		}
		views = append(views, MemberMetricView{ItemID: key.itemID, WaitMs: metric.emaWaitMs, Success: metric.emaSuccess, Samples: metric.samples})
	}
	return views
}

// ModelScoreView 是单个上游渠道模型的全局指标快照, 供模型页卡片展示评分。
type ModelScoreView struct {
	ChannelModelID int     `json:"channel_model_id"`
	WaitMs         float64 `json:"wait_ms"`
	Success        float64 `json:"success"`
	Samples        int     `json:"samples"`
}

// ModelScoreViews 返回全部有观测的上游渠道模型的全局指标快照, 按模型 ID 定序。
func ModelScoreViews() []ModelScoreView {
	metricMu.Lock()
	views := make([]ModelScoreView, 0, len(modelMetrics))
	for id, metric := range modelMetrics {
		if metric == nil || metric.samples <= 0 {
			continue
		}
		views = append(views, ModelScoreView{ChannelModelID: id, WaitMs: metric.emaWaitMs, Success: metric.emaSuccess, Samples: metric.samples})
	}
	metricMu.Unlock()
	sort.Slice(views, func(i, j int) bool { return views[i].ChannelModelID < views[j].ChannelModelID })
	return views
}
