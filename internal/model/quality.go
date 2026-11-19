package model

import "time"

// 质量权重读取辅助（跨包依赖通过函数变量注入，由 op 包初始化时设置）。
var QualityWeightGetter func(SettingKey, int) int

// ChannelQualitySnapshot 是渠道的按日质量快照(P3)。
// 健康探测记录会被轮转(每渠道只留最近 1000 条), 而质量历史需要长期保留, 故单独落一张轻量表:
// 每日把 StatsChannelDaily(真实流量)与 ChannelHealthRecord(探测可用率)汇总成一行, 供 30 天趋势与等级展示。
// 与资金完全正交: 只读统计, 不回写任何余额口径。
type ChannelQualitySnapshot struct {
	ChannelID    int       `json:"channel_id" gorm:"primaryKey;index" `  // 渠道主键, 与 Date 组成复合主键。
	Date         string    `json:"date" gorm:"primaryKey;size:16;index"` // 日期, 格式 20060102。
	RequestCount int64     `json:"request_count"`                        // 当日真实转发请求数(成功+失败)。
	SuccessCount int64     `json:"success_count"`                        // 当日成功请求数。
	FailCount    int64     `json:"fail_count"`                           // 当日失败请求数。
	SuccessRate  float64   `json:"success_rate"`                         // 成功率 0..1, 来自 StatsChannelDaily。
	AvgWaitMs    int64     `json:"avg_wait_ms"`                          // 平均首响应耗时(毫秒), WaitTime/请求数。
	UptimePct    float64   `json:"uptime_pct"`                           // 当日探测健康占比 0..100, 无探测时记 0。
	HealthChecks int64     `json:"health_checks"`                        // 当日探测总次数。
	HealthFails  int64     `json:"health_fails"`                         // 当日探测失败次数。
	Grade        string    `json:"grade" gorm:"size:2"`                  // 当日固化等级 A|B|C|D, 空串表示样本不足不评级。
	CreatedAt    time.Time `json:"created_at"`
}

// TableName 指定表名。
func (ChannelQualitySnapshot) TableName() string { return "channel_quality_snapshots" }

// 质量等级: 由综合分与成功率共同决定, 与 relay/score_view 的归一化思路同源但独立实现。
// 单独成常量而非硬编码字符串, 前端与分享页据此做颜色映射。
const (
	QualityGradeA = "A" // 综合分 >= 90 且成功率 >= 99.5%
	QualityGradeB = "B" // 综合分 >= 75
	QualityGradeC = "C" // 综合分 >= 60
	QualityGradeD = "D" // 其余
)

// QualityMinSamples 是参与评级的最低当日请求数。
// 样本太少时一次失败就能把成功率打到 50%, 评级会剧烈抖动, 故样本不足直接不评级(前端显示 --)。
const QualityMinSamples int64 = 30

// qualityMinHealthChecks 是可用率维度生效的最低探测次数: 没有探测时该维度不参与加权。
const qualityMinHealthChecks int64 = 3

// QualityGradeOf 按成功率/延迟/可用率三个维度算出综合分与等级。
// 维度缺失时的处理:
//   - 成功率与延迟来自真实流量, 请求数为 0 时直接不评级;
//   - 可用率来自健康探测, 探测次数不足时不参与加权, 其余两维按权重归一(而非按 0 分计入, 否则没开探测的渠道永远得 D)。
//
// 权重与设计稿一致: 成功率 0.40 / 延迟 0.35 / 可用率 0.25。
func QualityGradeOf(successRate float64, avgWaitMs int64, uptimePct float64, requestCount, healthChecks int64) (score float64, grade string) {
	if requestCount < QualityMinSamples {
		return 0, ""
	}
	// 读取可调权重，默认 40/35/25（权重会自动归一化）。
	wSuccess, wLatency, wUptime := 40, 35, 25
	if QualityWeightGetter != nil {
		wSuccess = QualityWeightGetter(SettingKeyQualityWeightSuccess, 40)
		wLatency = QualityWeightGetter(SettingKeyQualityWeightLatency, 35)
		wUptime = QualityWeightGetter(SettingKeyQualityWeightUptime, 25)
	}
	// 权重归一化到总和 1.0（避免用户配置总和不等于 100）。
	weightSum := float64(wSuccess + wLatency + wUptime)
	if weightSum <= 0 {
		weightSum = 100 // 全零时兜底
	}
	wSuccessNorm := float64(wSuccess) / weightSum
	wLatencyNorm := float64(wLatency) / weightSum
	wUptimeNorm := float64(wUptime) / weightSum

	// 成功率维度: 直接用成功率 × 100。
	successDim := clamp01(successRate) * 100
	// 延迟维度: <2s 满分, 2-5s 线性衰减, >5s 归零。
	latencyDim := latencyDimension(avgWaitMs)

	// 计算加权得分。
	score = wSuccessNorm*successDim + wLatencyNorm*latencyDim
	activeWeightSum := wSuccessNorm + wLatencyNorm
	if healthChecks >= qualityMinHealthChecks {
		uptimeDim := clamp01(uptimePct/100) * 100
		score += wUptimeNorm * uptimeDim
		activeWeightSum += wUptimeNorm
	}
	// 归一化到 0-100: 探测未开启时权重只有成功率+延迟，需重新归一。
	if activeWeightSum > 0 {
		score = score / activeWeightSum
	}
	switch {
	case score >= 90 && successRate >= 0.995:
		return score, QualityGradeA
	case score >= 75:
		return score, QualityGradeB
	case score >= 60:
		return score, QualityGradeC
	default:
		return score, QualityGradeD
	}
}

// latencyDimension 把平均首响应耗时折算成 0-100 分: 2 秒内满分, 2-5 秒线性衰减, 超过 5 秒归零。
func latencyDimension(avgWaitMs int64) float64 {
	const fastMs, slowMs = 2000, 5000
	if avgWaitMs <= fastMs {
		return 100
	}
	if avgWaitMs >= slowMs {
		return 0
	}
	return float64(slowMs-avgWaitMs) / float64(slowMs-fastMs) * 100
}

// clamp01 把值夹到 [0, 1], 顺带挡掉 NaN(比较恒 false, 落到 return 0)。
func clamp01(value float64) float64 {
	if value != value {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// QualityBandUptime 把可用率折算成对客可见的区间(分享页只出区间, 不出精确值, 防竞争情报泄露)。
func QualityBandUptime(uptimePct float64) string {
	switch {
	case uptimePct >= 99.9:
		return "99.9%+"
	case uptimePct >= 99:
		return "99%+"
	case uptimePct >= 95:
		return "95%+"
	default:
		return "<95%"
	}
}

// QualityBandLatency 把平均耗时折算成区间: <2s | 2-5s | >5s。
func QualityBandLatency(avgWaitMs int64) string {
	switch {
	case avgWaitMs < 2000:
		return "<2s"
	case avgWaitMs <= 5000:
		return "2-5s"
	default:
		return ">5s"
	}
}

// QualityBandSuccess 把成功率折算成区间: 99.5%+ | 99%+ | 95%+ | <95%。
func QualityBandSuccess(successRate float64) string {
	switch {
	case successRate >= 0.995:
		return "99.5%+"
	case successRate >= 0.99:
		return "99%+"
	case successRate >= 0.95:
		return "95%+"
	default:
		return "<95%"
	}
}
