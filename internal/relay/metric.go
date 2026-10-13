package relay

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// memberMetricKey 定位一个分组成员的运行指标。
type memberMetricKey struct {
	groupID int
	itemID  int
}

// memberMetric 成员的运行指标: 首响应耗时与成功率的指数滑动平均(EMA)。
// 只存进程内: 指标仅服务于选路, 重启后从持久化快照恢复历史成绩再继续积累。
type memberMetric struct {
	emaWaitMs      float64 // 首响应耗时 EMA, 毫秒; 流式为首帧, 非流式为完整响应。
	emaSuccess     float64 // 成功率 EMA, 0..1。
	samples        int     // 已累计样本数, 0 表示冷成员(尚无任何请求结果)。
	channelModelID int     // 该观测归属的渠道模型 ID; 0 表示未知(如映射缺失的残留行)。
	updatedAt      int64   // 最近一次观测写入时间(Unix 毫秒), 同样本数时用于定序。
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
	metric.updatedAt = time.Now().UnixMilli()
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
	if channelModelID != 0 {
		metric.channelModelID = channelModelID
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

// betterSeed 判定候选播种行优劣: 样本数大者优先, 同数取观测更新更近的, 仍同则取 (分组, 成员) 更小者保证确定性。
func betterSeed(a *memberMetric, aKey memberMetricKey, b *memberMetric, bKey memberMetricKey) bool {
	if a.samples != b.samples {
		return a.samples > b.samples
	}
	if a.updatedAt != b.updatedAt {
		return a.updatedAt > b.updatedAt
	}
	if aKey.groupID != bKey.groupID {
		return aKey.groupID < bKey.groupID
	}
	return aKey.itemID < bKey.itemID
}

// bestMemberMetricLocked 在成员观测里挑该渠道模型样本数最大的一条。调用方需持有 metricMu。
func bestMemberMetricLocked(channelModelID int) *memberMetric {
	var best *memberMetric
	var bestKey memberMetricKey
	for key, metric := range memberMetrics {
		if metric.channelModelID != channelModelID || metric.samples <= 0 {
			continue
		}
		if best == nil || betterSeed(metric, key, best, bestKey) {
			best, bestKey = metric, key
		}
	}
	return best
}

// seedModelMetricBestLocked 取渠道模型的有效指标并做冷启动播种:
// 全局指标缺失时, 用该模型已有成员观测里「样本数最大」的一条补上, 并非谁先被访问就用谁的观测。
// 之前的首个访问播种会让一条陈旧小样本钉死整个模型的成绩(生产上 1 个失败样本把共享评分乘成 0)。
// 调用方需持有 metricMu; 返回 (waitMs, success, samples), 无观测时全零。
func seedModelMetricBestLocked(channelModelID int) (float64, float64, int) {
	if channelModelID == 0 {
		return 0, 0, 0
	}
	if metric := modelMetrics[channelModelID]; metric != nil && metric.samples > 0 {
		return metric.emaWaitMs, metric.emaSuccess, metric.samples
	}
	best := bestMemberMetricLocked(channelModelID)
	if best == nil {
		return 0, 0, 0
	}
	modelMetrics[channelModelID] = &memberMetric{
		emaWaitMs:      best.emaWaitMs,
		emaSuccess:     best.emaSuccess,
		samples:        best.samples,
		channelModelID: channelModelID,
		updatedAt:      best.updatedAt,
	}
	return best.emaWaitMs, best.emaSuccess, best.samples
}

// seedModelMetricBest 取渠道模型的有效指标(全局优先, 缺失时按样本数最大的成员观测播种)。
func seedModelMetricBest(channelModelID int) (float64, float64, int) {
	metricMu.Lock()
	defer metricMu.Unlock()
	return seedModelMetricBestLocked(channelModelID)
}

// modelMetricSnapshotLocked 与 seedModelMetricBestLocked 同口径但绝不写回: 供展示侧只读取值。
func modelMetricSnapshotLocked(channelModelID int) (float64, float64, int) {
	if channelModelID == 0 {
		return 0, 0, 0
	}
	if metric := modelMetrics[channelModelID]; metric != nil && metric.samples > 0 {
		return metric.emaWaitMs, metric.emaSuccess, metric.samples
	}
	if best := bestMemberMetricLocked(channelModelID); best != nil {
		return best.emaWaitMs, best.emaSuccess, best.samples
	}
	return 0, 0, 0
}

// restoreMemberMetrics 把持久化指标快照装回内存, 并按渠道模型播种全局指标。
// itemModels 是成员 ID → 渠道模型 ID 映射(由 group_items ⋈ channel_grants 解析),
// 每个渠道模型取样本数最大的一行播种, 保证重启后共享评分从最有据的观测起步。
func restoreMemberMetrics(records []model.RouteMetricRecord, itemModels map[int]int) {
	metricMu.Lock()
	defer metricMu.Unlock()
	seeded := make(map[int]bool)
	for _, record := range records {
		if record.Samples <= 0 {
			continue
		}
		channelModelID := itemModels[record.ItemID]
		memberMetrics[memberMetricKey{groupID: record.GroupID, itemID: record.ItemID}] = &memberMetric{
			emaWaitMs:      record.EmaWaitMs,
			emaSuccess:     record.EmaSuccess,
			samples:        record.Samples,
			channelModelID: channelModelID,
			updatedAt:      record.UpdatedAt.UnixMilli(),
		}
		if channelModelID != 0 {
			seeded[channelModelID] = true
		}
	}
	for channelModelID := range seeded {
		seedModelMetricBestLocked(channelModelID)
	}
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

// ModelScore 是按模型名归并后的评分展示单元: 同名模型跨渠道的多份观测按样本数加权合并。
type ModelScore struct {
	Name     string              `json:"name"`
	WaitMs   float64             `json:"wait_ms"`
	Success  float64             `json:"success"`
	Samples  int                 `json:"samples"`
	Channels []ModelScoreChannel `json:"channels"`
}

// ModelScoreChannel 是单个渠道来源的观测: 对外展示名沿用渠道的发布编码口径。
type ModelScoreChannel struct {
	Channel string  `json:"channel"`
	WaitMs  float64 `json:"wait_ms"`
	Success float64 `json:"success"`
	Samples int     `json:"samples"`
}

// ModelScoreBriefs 把渠道模型的展示要素与全局指标按小写模型名归并为模型页评分列表。
// 归并口径与计费一致: 按样本数加权, 结果按模型名定序, 便于前端直接展示。
func ModelScoreBriefs(briefs []op.ChannelModelBrief, views []ModelScoreView) []ModelScore {
	byID := make(map[int]op.ChannelModelBrief, len(briefs))
	for _, brief := range briefs {
		byID[brief.ID] = brief
	}
	type acc struct {
		score  ModelScore
		weight float64
		wSum   float64
		sSum   float64
	}
	agg := make(map[string]*acc)
	for _, v := range views {
		brief, ok := byID[v.ChannelModelID]
		if !ok {
			continue
		}
		key := strings.ToLower(brief.Name)
		a := agg[key]
		if a == nil {
			a = &acc{score: ModelScore{Name: brief.Name}}
			agg[key] = a
		}
		a.score.Channels = append(a.score.Channels, ModelScoreChannel{Channel: brief.ChannelName, WaitMs: v.WaitMs, Success: v.Success, Samples: v.Samples})
		a.score.Samples += v.Samples
		a.weight += float64(v.Samples)
		a.wSum += v.WaitMs * float64(v.Samples)
		a.sSum += v.Success * float64(v.Samples)
	}
	out := make([]ModelScore, 0, len(agg))
	for _, a := range agg {
		if a.weight > 0 {
			a.score.WaitMs = a.wSum / a.weight
			a.score.Success = a.sSum / a.weight
		}
		sort.Slice(a.score.Channels, func(i, j int) bool { return a.score.Channels[i].Channel < a.score.Channels[j].Channel })
		out = append(out, a.score)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResetModelMetric 清空一个渠道模型的全部评分历史: 全局指标与其名下各成员观测(内存与持久化)一并清理,
// 供运维在观测被陈旧样本污染时手工纠偏。返回清理的成员观测条数。
func ResetModelMetric(channelModelID int) (int, error) {
	if channelModelID <= 0 {
		return 0, errInvalidMetricScope
	}
	metricMu.Lock()
	cleared := 0
	delete(modelMetrics, channelModelID)
	for key, metric := range memberMetrics {
		if metric.channelModelID == channelModelID {
			delete(memberMetrics, key)
			cleared++
		}
	}
	metricMu.Unlock()
	if _, err := op.RouteMetricDeleteByChannelModel(channelModelID); err != nil {
		return cleared, err
	}
	return cleared, nil
}

// ResetMemberMetric 清空单个分组成员的评分观测(内存与持久化), 并按其余观测重播该渠道模型的全局指标
// (无剩余观测则一并清空), 使被清掉的陈旧行不再参与任何打分。返回其归属的渠道模型 ID。
func ResetMemberMetric(groupID, itemID int) (int, error) {
	if groupID <= 0 || itemID <= 0 {
		return 0, errInvalidMetricScope
	}
	metricMu.Lock()
	key := memberMetricKey{groupID: groupID, itemID: itemID}
	metric := memberMetrics[key]
	channelModelID := 0
	if metric != nil {
		channelModelID = metric.channelModelID
	}
	delete(memberMetrics, key)
	if channelModelID != 0 {
		// 观测被移除后全局指标可能已陈旧: 删掉按剩余观测重播, 与恢复播种同一套择优规则。
		delete(modelMetrics, channelModelID)
		seedModelMetricBestLocked(channelModelID)
	}
	metricMu.Unlock()
	if err := op.RouteMetricDelete(groupID, itemID); err != nil {
		return channelModelID, err
	}
	return channelModelID, nil
}

// errInvalidMetricScope 指标重置作用域非法(渠道模型 ID 或分组成员 ID 未给出)。
var errInvalidMetricScope = errors.New("channel_model_id 或 group_id+item_id 必须给出一个")
