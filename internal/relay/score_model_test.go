package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
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
