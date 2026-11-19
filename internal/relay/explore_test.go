package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// exploreTestGroup 构造一个复现生产场景的 score 分组:
// 成员 1 是最便宜、且排在优先级第一的新成员(零观测), 成员 2/3 是已有实测数据的老成员。
// 老成员之间拉开价格与延迟的差距, 才能让最小-最大归一化有真实区间 ——
// 冷成员的可达上限正是被这套归一化压住的: 延迟与成功率只能取中性 0.5。
func exploreTestGroup(t *testing.T, id int) model.Group {
	t.Helper()
	return model.Group{ID: id, Mode: model.GroupModeScore, RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeScore),
		Items: []model.GroupItem{
			{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: id*10 + 2, ModelName: "new", Available: true, Priority: 1,
				UserPrice: model.LLMPrice{Input: 0.01, Output: 0.05}},
			{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: id*10 + 3, ModelName: "old-fast", Available: true, Priority: 2,
				UserPrice: model.LLMPrice{Input: 0.08, Output: 0.12}},
			{ID: 3, ChannelID: 3, ChannelKeyID: 3, ChannelModelID: id*10 + 4, ModelName: "old-slow", Available: true, Priority: 3,
				UserPrice: model.LLMPrice{Input: 0.2, Output: 0.7}},
		}}
}

// exploreOldMetrics 是老成员的观测: 快而稳的那台正是评分每轮都会选中的对象。
func exploreOldMetrics(group model.Group) map[int]memberMetric {
	return map[int]memberMetric{
		group.Items[1].ChannelModelID: {emaWaitMs: 1200, emaSuccess: 1.0, samples: 500},
		group.Items[2].ChannelModelID: {emaWaitMs: 30000, emaSuccess: 1.0, samples: 500},
	}
}

// TestColdStartExploreBreaksScoreStarvation 复现生产反馈: 新增渠道最便宜且优先级第一,
// 却一周零调用。根因是冷成员在延迟与成功率两维只能取中性值, 分数被结构性封顶,
// 老成员只要"快而稳"就永远排在它前面, 于是它永远拿不到第一批样本。
// 冷启动探索配额必须打破这个死循环: 按固定节奏把一部分请求定向给新成员。
func TestColdStartExploreBreaksScoreStarvation(t *testing.T) {
	group := exploreTestGroup(t, 890)
	defer setupRecoveryTest(t, group.ID)()
	defer setModelMetricsForTest(t, exploreOldMetrics(group))()

	// 先确认只按评分时新成员确实永远排不上: 这就是"零调用"的根本原因。
	if got := selectByScore(group, group.Items); got.ID != group.Items[1].ID {
		t.Fatalf("评分应选中快而稳的老成员, 实际 %d", got.ID)
	}

	var explored model.GroupItem
	exploredAt := 0
	for i := 1; i <= exploreQuotaEvery; i++ {
		chosen := pickGroupItem(group)
		if chosen.ID == group.Items[0].ID {
			explored, exploredAt = chosen, i
			break
		}
		if chosen.ID != group.Items[1].ID {
			t.Fatalf("第 %d 轮应命中快而稳的老成员, 实际 %d", i, chosen.ID)
		}
	}
	if explored.ID != group.Items[0].ID {
		t.Fatalf("%d 轮内新成员一次也没拿到试探请求", exploreQuotaEvery)
	}
	if exploredAt != exploreQuotaEvery {
		t.Fatalf("试探应严格按节奏放行: 第 %d 轮命中, 期望第 %d 轮", exploredAt, exploreQuotaEvery)
	}
	// 试探必须能被解释: 否则用户看到日志里"按评分选路"却打到评分排名靠后的渠道, 无法理解。
	if decision := routeExplanation(group, explored, false); decision.Reason != "cold_start_explore" {
		t.Fatalf("试探请求的路由原因应为 cold_start_explore, 实际 %q", decision.Reason)
	}
	// 下一轮必须立刻回到评分择优: 探索是一次性的试探, 不是持续偏移。
	if next := pickGroupItem(group); next.ID != group.Items[1].ID {
		t.Fatalf("试探之后应回到评分择优, 实际 %d", next.ID)
	}
}

// TestColdStartExploreStopsAfterWarmupSamples 校验配额是"总量有界"的:
// 新成员攒够样本后不再享受探索, 回到与老成员同一套评分上公平竞争 ——
// 此时哪怕它最便宜, 实测更差也不会再被硬塞流量。
func TestColdStartExploreStopsAfterWarmupSamples(t *testing.T) {
	group := exploreTestGroup(t, 891)
	metrics := exploreOldMetrics(group)
	metrics[group.Items[0].ChannelModelID] = memberMetric{emaWaitMs: 30000, emaSuccess: 1.0, samples: coldStartSamples}
	defer setupRecoveryTest(t, group.ID)()
	defer setModelMetricsForTest(t, metrics)()

	for i := 1; i <= exploreQuotaEvery*4; i++ {
		if chosen := pickGroupItem(group); chosen.ID != group.Items[1].ID {
			t.Fatalf("样本已达标的新成员不应再占用探索配额, 第 %d 轮选中 %d", i, chosen.ID)
		}
	}
}

// TestColdStartExploreRotatesNewMembers 校验多个新成员之间轮流探索:
// 每轮只给观测最少的成员, 于是新成员不会互相饿死, 也不会有一个成员独占全部试探流量。
func TestColdStartExploreRotatesNewMembers(t *testing.T) {
	group := exploreTestGroup(t, 892)
	defer setupRecoveryTest(t, group.ID)()

	fresh := model.GroupItem{ID: 4, ChannelModelID: 8925, Available: true, Priority: 4}
	seen := model.GroupItem{ID: 5, ChannelModelID: 8926, Available: true, Priority: 5}
	group.Items = append(group.Items, fresh, seen)

	metrics := exploreOldMetrics(group)
	metrics[group.Items[0].ChannelModelID] = memberMetric{emaWaitMs: 9000, emaSuccess: 1.0, samples: 5}
	metrics[seen.ChannelModelID] = memberMetric{emaWaitMs: 5000, emaSuccess: 1.0, samples: 1}
	restore := setModelMetricsForTest(t, metrics)
	defer restore()

	route := &RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	// 把节奏推进到"下一轮就命中", 只观察轮到谁。
	route.exploreCounter = exploreQuotaEvery - 1
	chosen, ok := coldStartExplore(group, group.Items, group.Items[1], route)
	if !ok || chosen.ID != fresh.ID {
		t.Fatalf("应轮到零样本的新成员, 实际 %+v ok=%v", chosen, ok)
	}

	// 该成员拿到观测后, 轮到另一个样本更少的成员。
	restore()
	metrics[fresh.ChannelModelID] = memberMetric{emaWaitMs: 4000, emaSuccess: 1.0, samples: 2}
	defer setModelMetricsForTest(t, metrics)()
	route.exploreCounter = exploreQuotaEvery*3 - 1
	chosen, ok = coldStartExplore(group, group.Items, group.Items[1], route)
	if !ok || chosen.ID != seen.ID {
		t.Fatalf("应轮到另一个样本更少的新成员, 实际 %+v ok=%v", chosen, ok)
	}
}

// TestColdStartExploreKeepsHealthyBehaviour 校验探索不会越界:
// 全部成员都已攒够样本、或候选不足两个时, 一律按原策略选路, 不额外插流量。
func TestColdStartExploreKeepsHealthyBehaviour(t *testing.T) {
	group := exploreTestGroup(t, 893)
	metrics := exploreOldMetrics(group)
	metrics[group.Items[0].ChannelModelID] = memberMetric{emaWaitMs: 9000, emaSuccess: 1.0, samples: coldStartSamples}
	defer setupRecoveryTest(t, group.ID)()
	defer setModelMetricsForTest(t, metrics)()

	route := &RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	if _, ok := coldStartExplore(group, group.Items, group.Items[1], route); ok {
		t.Fatal("全部成员样本达标时不应探索")
	}
	if _, ok := coldStartExplore(group, group.Items[:1], group.Items[0], route); ok {
		t.Fatal("单候选分组不应探索")
	}
	for i := 1; i <= exploreQuotaEvery*3; i++ {
		if chosen := pickGroupItem(group); chosen.ID != group.Items[1].ID {
			t.Fatalf("全部成员都已达标时不应插入试探, 第 %d 轮选中 %d", i, chosen.ID)
		}
	}
}

// TestColdStartExploreSkipsNonStrategyModes 校验只有"按实测成绩择优"的模式才需要探索:
// 价格优先天然选中最便宜的成员, 故障转移按优先级, 都不存在结构性饿死, 也不该推进探索节奏。
func TestColdStartExploreSkipsNonStrategyModes(t *testing.T) {
	group := exploreTestGroup(t, 894)
	defer setupRecoveryTest(t, group.ID)()
	defer setModelMetricsForTest(t, exploreOldMetrics(group))()

	for _, mode := range []model.GroupMode{model.GroupModePrice, model.GroupModeFailover, model.GroupModeRandom} {
		route := &RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
		candidate := group
		candidate.Mode = mode
		if _, ok := coldStartExplore(candidate, candidate.Items, candidate.Items[1], route); ok {
			t.Fatalf("%s 模式不应触发冷启动探索", mode)
		}
		if route.exploreCounter != 0 {
			t.Fatalf("%s 模式不应推进探索节奏: %d", mode, route.exploreCounter)
		}
	}
}

// TestColdStartExploreSkipsWhenChosenIsCold 校验本轮评分本来就选中尚未攒够样本的成员时,
// 不再额外插一次探索: 正常选路已经在照顾它, 重复插队只会让它的流量翻倍。
func TestColdStartExploreSkipsWhenChosenIsCold(t *testing.T) {
	group := exploreTestGroup(t, 895)
	metrics := exploreOldMetrics(group)
	metrics[group.Items[0].ChannelModelID] = memberMetric{emaWaitMs: 3000, emaSuccess: 1.0, samples: 3}
	defer setupRecoveryTest(t, group.ID)()
	defer setModelMetricsForTest(t, metrics)()

	route := &RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	// 价格拉到最低, 让评分本身就选中新成员。
	group.Items[1].UserPrice = model.LLMPrice{Input: 5, Output: 9}
	group.Items[2].UserPrice = model.LLMPrice{Input: 6, Output: 9}
	route.exploreCounter = exploreQuotaEvery - 1
	if _, ok := coldStartExplore(group, group.Items, group.Items[0], route); ok {
		t.Fatal("评分已选中冷成员时不应重复探索")
	}
}
