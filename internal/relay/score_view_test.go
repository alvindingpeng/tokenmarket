package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// setModelMetricsForTest 直接写入全局模型指标供测试构造观测, 返回还原函数。
func setModelMetricsForTest(t *testing.T, metrics map[int]memberMetric) func() {
	t.Helper()
	metricMu.Lock()
	original := modelMetrics
	modelMetrics = make(map[int]*memberMetric, len(metrics))
	for id, m := range metrics {
		copied := m
		modelMetrics[id] = &copied
	}
	metricMu.Unlock()
	return func() {
		metricMu.Lock()
		modelMetrics = original
		metricMu.Unlock()
	}
}

// TestMemberScoreBasisResolution 校验配比解析顺序: 分组自定义 > 系统设置 > 内置兜底。
func TestMemberScoreBasisResolution(t *testing.T) {
	custom := MemberScoreBasis(model.Group{RelayConfig: model.GroupRelayConfig{
		ScorePriceWeight: 60, ScoreLatencyWeight: 10, ScoreSuccessWeight: 30, ReliabilityFloor: 90,
	}})
	if custom.PriceWeight != 60 || custom.LatencyWeight != 10 || custom.SuccessWeight != 30 {
		t.Fatalf("分组自定义配比未生效: %+v", custom)
	}
	if custom.ReliabilityFloor != 0.9 {
		t.Fatalf("可靠性下限未按百分比折算: %+v", custom)
	}

	fallback := MemberScoreBasis(model.Group{RelayConfig: model.GroupRelayConfig{}})
	if fallback.PriceWeight != defaultScorePriceWeight ||
		fallback.LatencyWeight != defaultScoreLatencyWeight ||
		fallback.SuccessWeight != defaultScoreSuccessWeight {
		t.Fatalf("未自定义时应回落内置配比: %+v", fallback)
	}
	if fallback.ReliabilityFloor != reliabilityFloorDefault {
		t.Fatalf("未自定义时应回落内置可靠性下限: %+v", fallback)
	}
}

// TestMemberScoreViewsRankByPrice 校验价格维度的方向: 越便宜得分越高, 综合分同样领先。
func TestMemberScoreViewsRankByPrice(t *testing.T) {
	cheap := model.GroupItem{ID: 1, UserPrice: model.LLMPrice{Input: 1, Output: 1}}
	dear := model.GroupItem{ID: 2, UserPrice: model.LLMPrice{Input: 9, Output: 9}}
	group := model.Group{ID: 930, Items: []model.GroupItem{cheap, dear}}

	views := MemberScoreViews(group)
	if len(views) != 2 {
		t.Fatalf("应返回全部成员: %d", len(views))
	}
	if views[0].PriceScore <= views[1].PriceScore {
		t.Fatalf("价格维度未按低价高分排序: %v vs %v", views[0].PriceScore, views[1].PriceScore)
	}
	if views[0].Score <= views[1].Score {
		t.Fatalf("最便宜成员的综合分未领先: %v vs %v", views[0].Score, views[1].Score)
	}
}

// TestMemberScoreViewsColdNeutral 校验冷成员按中性值参与: 不因无样本被判最优或最差。
func TestMemberScoreViewsColdNeutral(t *testing.T) {
	a := model.GroupItem{ID: 1, UserPrice: model.LLMPrice{Input: 1, Output: 1}}
	b := model.GroupItem{ID: 2, UserPrice: model.LLMPrice{Input: 2, Output: 2}}
	views := MemberScoreViews(model.Group{ID: 931, Items: []model.GroupItem{a, b}})

	for _, view := range views {
		if !view.Cold {
			t.Fatalf("成员 %d 无样本却未标记冷成员", view.ItemID)
		}
		if view.LatencyScore != 50 || view.SuccessScore != 50 {
			t.Fatalf("冷成员的延迟/成功率应取中性 50: %+v", view)
		}
		if !view.Healthy {
			t.Fatalf("冷成员不应被判为不健康: %+v", view)
		}
	}
}

// TestMemberScoreViewsReliabilityPenalty 校验可靠性软惩罚与选路一致:
// 成功率低于下限的成员总分被折减, 即使价格相同也会落后于健康成员。
func TestMemberScoreViewsReliabilityPenalty(t *testing.T) {
	sick := model.GroupItem{ID: 1, UserPrice: model.LLMPrice{Input: 1, Output: 1}, ChannelModelID: 11}
	sound := model.GroupItem{ID: 2, UserPrice: model.LLMPrice{Input: 1, Output: 1}, ChannelModelID: 12}
	group := model.Group{ID: 932, Items: []model.GroupItem{sick, sound},
		RelayConfig: model.GroupRelayConfig{ReliabilityFloor: 70}}

	// 直接喂全局模型指标: 生病成员成功率 0.4, 健康成员 0.95。
	defer setModelMetricsForTest(t, map[int]memberMetric{
		11: {emaWaitMs: 100, emaSuccess: 0.4, samples: 20},
		12: {emaWaitMs: 100, emaSuccess: 0.95, samples: 20},
	})()

	views := MemberScoreViews(group)
	if views[0].Healthy {
		t.Fatalf("成功率 0.4 应判为不健康: %+v", views[0])
	}
	if !views[1].Healthy {
		t.Fatalf("成功率 0.95 应判为健康: %+v", views[1])
	}
	if views[0].Score >= views[1].Score {
		t.Fatalf("可靠性折减未生效: %.2f vs %.2f", views[0].Score, views[1].Score)
	}
}

// TestMemberScoreMatchesSelectByScore 校验展示分与实际选路用的是同一套分数:
// 展示分最高的成员正是 selectByScore 会选中的那个。
func TestMemberScoreMatchesSelectByScore(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, UserPrice: model.LLMPrice{Input: 0.4, Output: 0.9}, ChannelModelID: 21},
		{ID: 2, Priority: 2, UserPrice: model.LLMPrice{Input: 0.1, Output: 0.6}, ChannelModelID: 22},
		{ID: 3, Priority: 3, UserPrice: model.LLMPrice{Input: 0.2, Output: 0.5}, ChannelModelID: 23},
	}
	group := model.Group{ID: 933, Items: items, Mode: model.GroupModeScore,
		RelayConfig: model.GroupRelayConfig{ScorePriceWeight: 60, ScoreLatencyWeight: 10, ScoreSuccessWeight: 30}}

	defer setModelMetricsForTest(t, map[int]memberMetric{
		21: {emaWaitMs: 200, emaSuccess: 0.99, samples: 30},
		22: {emaWaitMs: 900, emaSuccess: 0.95, samples: 30},
		23: {emaWaitMs: 150, emaSuccess: 0.80, samples: 30},
	})()

	views := MemberScoreViews(group)
	best := views[0]
	for _, view := range views[1:] {
		if view.Score > best.Score {
			best = view
		}
	}
	chosen := selectByScore(group, items)
	if chosen.ID != best.ItemID {
		t.Fatalf("展示分最高(成员 %d, %.2f)与选路选中(成员 %d)不一致", best.ItemID, best.Score, chosen.ID)
	}
}
