package relay

import (
	"github.com/alvindingpeng/switchyard/internal/model"
)

// MemberScoreView 是分组成员在当前有效评分配比下的综合评分快照, 供模型配置页的成员信息展示。
// 口径与选路的 selectByScore 一致(同一套最小-最大归一化与可靠性软惩罚), 差别只在于把全部成员的
// 分数都算出来而不是只取最高分那一个; 归一化范围取本分组全部成员, 让分数在配置页上稳定可比。
type MemberScoreView struct {
	ItemID       int     `json:"item_id"`       // 分组成员 ID, 与分组条目的 id 对应。
	Score        float64 `json:"score"`         // 0-100 综合得分, 已含可靠性软惩罚。
	PriceScore   float64 `json:"price_score"`   // 0-100 价格维度得分, 越高越便宜。
	LatencyScore float64 `json:"latency_score"` // 0-100 延迟维度得分, 越高越快; 冷成员为中性 50。
	SuccessScore float64 `json:"success_score"` // 0-100 成功率维度得分, 越高越稳; 冷成员为中性 50。
	WaitMs       float64 `json:"wait_ms"`       // 首响应耗时 EMA(毫秒), 冷成员为 0。
	Success      float64 `json:"success"`       // 成功率 EMA(0..1), 冷成员为 0。
	Samples      int     `json:"samples"`       // 已累计样本数, 0 表示冷成员。
	Cold         bool    `json:"cold"`          // 尚无观测, 延迟与成功率按中性值参与评分。
	Healthy      bool    `json:"healthy"`       // 成功率是否达到可靠性下限; 冷成员视为达标。
}

// ScoreBasis 是本次评分实际采用的权重与可靠性下限, 让界面能说明分数是按哪套参数算出来的。
// 解析顺序与选路一致: 分组自定义 > 系统设置 > 内置兜底; 全 0 的维度即"未自定义"。
type ScoreBasis struct {
	PriceWeight      int     `json:"price_weight"`      // 价格权重(相对值)。
	LatencyWeight    int     `json:"latency_weight"`    // 延迟权重(相对值)。
	SuccessWeight    int     `json:"success_weight"`    // 成功率权重(相对值)。
	ReliabilityFloor float64 `json:"reliability_floor"` // 可靠性下限(0..1), 低于此值按比例折减总分。
}

// MemberScoreBasis 返回分组评分实际生效的权重与可靠性下限。
func MemberScoreBasis(group model.Group) ScoreBasis {
	price, latency, success := effectiveScoreWeights(group.RelayConfig)
	return ScoreBasis{
		PriceWeight:      price,
		LatencyWeight:    latency,
		SuccessWeight:    success,
		ReliabilityFloor: effectiveReliabilityFloor(group.RelayConfig),
	}
}

// memberMetricSnapshot 只读地取成员的评分指标: 优先用渠道模型全局指标, 缺失时回落本分组观测。
// 与 memberScopeMetric 的取值口径完全一致, 但不做冷启动播种 —— 播种是选路的副作用,
// 一次展示不该顺手改写全局指标。
func memberMetricSnapshot(group model.Group, item model.GroupItem) (waitMs float64, success float64, samples int) {
	groupWait, groupSuccess, groupSamples := memberMetricOf(group.ID, item.ID)
	if item.ChannelModelID != 0 {
		if waitMs, success, samples = modelMetricOf(item.ChannelModelID); samples > 0 {
			return waitMs, success, samples
		}
	}
	return groupWait, groupSuccess, groupSamples
}

// MemberScoreViews 按分组当前生效的评分配比算出全部成员的综合评分与各维度得分, 顺序与成员一致。
func MemberScoreViews(group model.Group) []MemberScoreView {
	items := group.Items
	if len(items) == 0 {
		return nil
	}
	basis := MemberScoreBasis(group)

	prices := make([]float64, len(items))
	waits := make([]float64, len(items))
	rates := make([]float64, len(items))
	samples := make([]int, len(items))
	known := make([]bool, len(items))
	for i, item := range items {
		prices[i] = memberPrice(group, item)
		waits[i], rates[i], samples[i] = memberMetricSnapshot(group, item)
		known[i] = samples[i] > 0
	}

	views := make([]MemberScoreView, len(items))
	for i, item := range items {
		view := MemberScoreView{
			ItemID:       item.ID,
			PriceScore:   normalizedDim(prices, i, false) * 100,
			LatencyScore: 50,
			SuccessScore: 50,
			WaitMs:       waits[i],
			Success:      rates[i],
			Samples:      samples[i],
			Cold:         !known[i],
			Healthy:      !known[i] || rates[i] >= basis.ReliabilityFloor,
		}
		if known[i] {
			view.LatencyScore = normalizedDim(waits, i, false) * 100
			view.SuccessScore = normalizedDim(rates, i, true) * 100
		}
		// 与 selectByScore 同一套算法: 加权求和后再按可靠性软惩罚折减, 冷成员不惩罚。
		score := memberScore(basis.PriceWeight, basis.LatencyWeight, basis.SuccessWeight, i, prices, waits, rates, known)
		if known[i] && rates[i] < basis.ReliabilityFloor {
			score *= rates[i] / basis.ReliabilityFloor
		}
		view.Score = score * 100
		views[i] = view
	}
	return views
}
