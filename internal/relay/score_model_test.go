package relay

import (
	"math"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// TestSelectByScoreDemotesUnreliableCheapMember 复现生产反馈:
// 某渠道模型价格最低但成功率已跌破可靠性下限时, 即使价格权重高达 60,
// 也必须排在达标成员之后, 不得靠静态价格长期霸占首位。
func TestSelectByScoreDemotesUnreliableCheapMember(t *testing.T) {
	metricMu.Lock()
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	modelMetrics = make(map[int]*memberMetric)
	metricMu.Unlock()

	group := model.Group{ID: 501, Mode: model.GroupModeScore}
	group.RelayConfig.ScorePriceWeight = 60
	group.RelayConfig.ScoreLatencyWeight = 10
	group.RelayConfig.ScoreSuccessWeight = 30
	group.RelayConfig.MetricWindowSize = 20

	cheap, reliable := model.GroupItem{}, model.GroupItem{}
	cheap.ID, cheap.ChannelModelID, cheap.UserPrice = 1, 901, model.LLMPrice{Input: 0.05, Output: 0.2}
	reliable.ID, reliable.ChannelModelID, reliable.UserPrice = 2, 902, model.LLMPrice{Input: 0.15, Output: 0.5}

	// 便宜成员: 多次失败, 成功率低于下限。
	for i := 0; i < 12; i++ {
		recordMemberMetric(group.ID, cheap.ID, cheap.ChannelModelID, 20, 40000, false)
	}
	// 可靠成员: 稳定成功, 成功率 100%。
	for i := 0; i < 12; i++ {
		recordMemberMetric(group.ID, reliable.ID, reliable.ChannelModelID, 20, 40000, true)
	}

	got := selectByScore(group, []model.GroupItem{cheap, reliable})
	if got.ID != reliable.ID {
		t.Fatalf("unreliable cheap member must be demoted: got item %d, want %d", got.ID, reliable.ID)
	}
}

// TestSelectByScoreSharedPerChannelModel 校验打分按上游模型全局共享:
// 同一渠道模型在另一个分组的新成员也应直接继承其已积累的成绩, 而不是被当成冷成员。
func TestSelectByScoreSharedPerChannelModel(t *testing.T) {
	metricMu.Lock()
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	modelMetrics = make(map[int]*memberMetric)
	metricMu.Unlock()

	// 模型 903 在分组 601 中积累了糟糕的成功率。
	for i := 0; i < 12; i++ {
		recordMemberMetric(601, 11, 903, 20, 30000, false)
	}
	// 分组 602 引用了同一渠道模型的新成员, 应继承该模型的不良成绩。
	waitMs, success, samples := modelMetricOf(903)
	if samples != 12 {
		t.Fatalf("model metric samples = %d, want 12", samples)
	}
	if success >= 0.7 {
		t.Fatalf("model 903 success = %.3f, want below floor (0.7)", success)
	}
	if waitMs <= 0 {
		t.Fatalf("model 903 waitMs = %.0f, want positive", waitMs)
	}
}

// TestReliabilityFloorResolution 校验下限的解析顺序: 分组自定义 > 系统设置 > 内置兜底。
func TestReliabilityFloorResolution(t *testing.T) {
	if got := effectiveReliabilityFloor(model.GroupRelayConfig{ReliabilityFloor: 90}); got != 0.9 {
		t.Fatalf("group override floor = %.2f, want 0.90", got)
	}
	if got := effectiveReliabilityFloor(model.GroupRelayConfig{}); got != reliabilityFloorDefault {
		t.Fatalf("fallback floor = %.2f, want %.2f", got, reliabilityFloorDefault)
	}
}

// TestLatencyModePrefersMeasuredMember 校验延迟模式下有实测数据的成员优先于冷成员。
func TestLatencyModePrefersMeasuredMember(t *testing.T) {
	metricMu.Lock()
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	modelMetrics = make(map[int]*memberMetric)
	metricMu.Unlock()

	group := model.Group{ID: 701, Mode: model.GroupModeLatency}
	group.RelayConfig.MetricWindowSize = 20

	cold := model.GroupItem{ID: 21, ChannelModelID: 921}
	measured := model.GroupItem{ID: 22, ChannelModelID: 922}
	recordMemberMetric(group.ID, measured.ID, measured.ChannelModelID, 20, 500*time.Millisecond.Milliseconds(), true)

	got := selectGroupItem(group, []model.GroupItem{cold, measured})
	if got.ID != measured.ID {
		t.Fatalf("latency mode should prefer measured member: got %d, want %d", got.ID, measured.ID)
	}
}

// TestSelectByScoreSoftPenaltyKeepsCheapestAhead 复现 klarns 反馈: 价格权重 60 时,
// 最便宜但成功率略低于下限(如 0.66 < 0.70)的模型不应被硬门槛压到更贵模型之后。
// 软性惩罚按 succ/floor 折减, 只要折扣后仍最高就应胜出; 而彻底不可用(succ≈0)者必须垫底。
func TestSelectByScoreSoftPenaltyKeepsCheapestAhead(t *testing.T) {
	metricMu.Lock()
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	modelMetrics = make(map[int]*memberMetric)
	metricMu.Unlock()

	group := model.Group{ID: 510, Mode: model.GroupModeScore}
	group.RelayConfig.ScorePriceWeight = 60
	group.RelayConfig.ScoreLatencyWeight = 10
	group.RelayConfig.ScoreSuccessWeight = 30
	group.RelayConfig.MetricWindowSize = 50 // 平滑, 让成功率稳定落在目标附近
	group.RelayConfig.ReliabilityFloor = 70

	cheap, pricier, broken := model.GroupItem{}, model.GroupItem{}, model.GroupItem{}
	cheap.ID, cheap.ChannelModelID, cheap.UserPrice = 1, 910, model.LLMPrice{Input: 0.05, Output: 0.2}
	pricier.ID, pricier.ChannelModelID, pricier.UserPrice = 2, 911, model.LLMPrice{Input: 0.15, Output: 0.5}
	broken.ID, broken.ChannelModelID, broken.UserPrice = 3, 912, model.LLMPrice{Input: 0.15, Output: 0.5}

	// 直接种入目标成功率, 避免受 EMA 收敛影响: cheap≈0.66(略低于下限), pricier=1.0, broken=0。
	metricMu.Lock()
	modelMetrics[910] = &memberMetric{emaWaitMs: 10000, emaSuccess: 0.66, samples: 42}
	modelMetrics[911] = &memberMetric{emaWaitMs: 7000, emaSuccess: 1.00, samples: 60}
	modelMetrics[912] = &memberMetric{emaWaitMs: 5000, emaSuccess: 0.00, samples: 3}
	metricMu.Unlock()

	got := selectByScore(group, []model.GroupItem{pricier, cheap, broken})
	if got.ID != cheap.ID {
		t.Fatalf("略低于下限的最便宜模型应凭价格胜出: got item %d, want %d", got.ID, cheap.ID)
	}

	// 彻底失败(succ=0)的成员即使比 pricier 更快也不应入选。
	got2 := selectByScore(group, []model.GroupItem{pricier, broken})
	if got2.ID != pricier.ID {
		t.Fatalf("succ=0 成员必须被软惩罚压到垫底: got item %d, want %d", got2.ID, pricier.ID)
	}
}

// TestModelScoreBriefsAggregatesChannels 校验模型页评分归并: 同名模型跨渠道的多份观测
// 按样本数加权, 分数按模型名定序, 无观测的渠道模型不出现。
func TestModelScoreBriefsAggregatesChannels(t *testing.T) {
	briefs := []op.ChannelModelBrief{
		{ID: 1, Name: "deepseek-v4-flash", ChannelName: "chB"},
		{ID: 2, Name: "DeepSeek-V4-flash", ChannelName: "chA"}, // 同名不同大小写
		{ID: 3, Name: "glm-5.3-flash", ChannelName: "chA"},
		{ID: 4, Name: "cold-model", ChannelName: "chA"}, // 无观测
	}
	views := []ModelScoreView{
		{ChannelModelID: 1, WaitMs: 1000, Success: 1.0, Samples: 1},
		{ChannelModelID: 2, WaitMs: 3000, Success: 0.0, Samples: 3},
		{ChannelModelID: 3, WaitMs: 2000, Success: 1.0, Samples: 5},
	}
	got := ModelScoreBriefs(briefs, views)
	if len(got) != 2 {
		t.Fatalf("expected 2 scored models, got %d", len(got))
	}
	if got[0].Name != "deepseek-v4-flash" || got[1].Name != "glm-5.3-flash" {
		t.Fatalf("models must be sorted by name: %+v", got)
	}
	merged := got[0]
	if merged.Samples != 4 {
		t.Fatalf("samples must sum: got %d", merged.Samples)
	}
	if diff := math.Abs(merged.WaitMs - 2500); diff > 0.001 {
		t.Fatalf("wait must be sample-weighted: got %v want 2500", merged.WaitMs)
	}
	if diff := math.Abs(merged.Success - 0.25); diff > 0.001 {
		t.Fatalf("success must be sample-weighted: got %v want 0.25", merged.Success)
	}
	if len(merged.Channels) != 2 || merged.Channels[0].Channel != "chA" {
		t.Fatalf("channels must be sorted: %+v", merged.Channels)
	}
}
