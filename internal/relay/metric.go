package relay

import "sync"

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
)

// recordMemberMetric 记录一次成员请求结果, 供延迟/成功率/综合评分策略排名使用。
func recordMemberMetric(groupID, itemID, window int, waitMs int64, success bool) {
	key := memberMetricKey{groupID: groupID, itemID: itemID}
	metricMu.Lock()
	defer metricMu.Unlock()
	metric := memberMetrics[key]
	if metric == nil {
		metric = &memberMetric{}
		memberMetrics[key] = metric
	}
	if metric.samples == 0 {
		metric.emaWaitMs = float64(waitMs)
		if success {
			metric.emaSuccess = 1
		}
	} else {
		alpha := metricAlpha(window)
		metric.emaWaitMs = alpha*float64(waitMs) + (1-alpha)*metric.emaWaitMs
		s := 0.0
		if success {
			s = 1
		}
		metric.emaSuccess = metricAlpha(window)*s + (1-metricAlpha(window))*metric.emaSuccess
	}
	metric.samples++
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
