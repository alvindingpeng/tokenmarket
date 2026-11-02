package relay

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestPickCanaryItemReturnsConfiguredMember 校验灰度命中时选中配置的成员, 且不改写共享路由状态。
// 灰度成员若被写成 route.CurrentItemID, 亲和期内未命中灰度的请求也会被一起带到新渠道,
// 灰度比例就被亲和悄悄放大了, 所以这里显式断言路由状态没被动过。
func TestPickCanaryItemReturnsConfiguredMember(t *testing.T) {
	const groupID = 930
	defer setupRecoveryTest(t, groupID)()

	stable := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "stable", Available: true, Priority: 1}
	canary := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "canary", Available: true, Priority: 2}
	group := model.Group{ID: groupID, Mode: model.GroupModeScore, Items: []model.GroupItem{stable, canary},
		RelayConfig: model.GroupRelayConfig{CanaryItemID: canary.ID, CanaryPercent: 30}}

	item, ok := pickCanaryItem(group)
	if !ok || item.ID != canary.ID {
		t.Fatalf("canary pick = %+v ok=%v, want member %d", item, ok, canary.ID)
	}
	routeMu.Lock()
	route := routes[groupID]
	routeMu.Unlock()
	if route != nil && (route.CurrentItemID != 0 || route.AffinityUntil != 0) {
		t.Fatalf("canary selection touched shared route state: %+v", route)
	}
}

// TestPickCanaryItemFallsBackWhenUnavailable 校验灰度成员不可用时回落: 未开启、成员停用、冷却中都返回假,
// 由调用方走原本的策略选路。灰度只是"多分一点流量给新渠道", 不能因为新渠道没就绪就卡住请求。
func TestPickCanaryItemFallsBackWhenUnavailable(t *testing.T) {
	const groupID = 931
	defer setupRecoveryTest(t, groupID)()

	canary := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "canary", Available: true, Priority: 1}
	group := model.Group{ID: groupID, Mode: model.GroupModePrice, Items: []model.GroupItem{canary},
		RelayConfig: model.GroupRelayConfig{CanaryItemID: canary.ID, CanaryPercent: 50}}

	if _, ok := pickCanaryItem(model.Group{ID: groupID, Mode: model.GroupModePrice, Items: group.Items}); ok {
		t.Fatal("canary without configured member should not be picked")
	}

	disabled := group
	disabled.Items = []model.GroupItem{{ID: canary.ID, Available: false}}
	if _, ok := pickCanaryItem(disabled); ok {
		t.Fatal("disabled canary member should not be picked")
	}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{canary.ID: time.Now().Add(time.Minute).UnixMilli()}}
	routeMu.Unlock()
	if _, ok := pickCanaryItem(group); ok {
		t.Fatal("cooling canary member should not be picked")
	}
}

// TestPickCanaryItemYieldsToRecoveryProbe 校验灰度成员正在被恢复探测时让出本轮:
// 探测本身已经给它排了一个请求, 再叠一层灰度会在它刚恢复时放大瞬时压力。
func TestPickCanaryItemYieldsToRecoveryProbe(t *testing.T) {
	const groupID = 932
	defer setupRecoveryTest(t, groupID)()

	canary := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "canary", Available: true, Priority: 1}
	group := model.Group{ID: groupID, Mode: model.GroupModePrice, Items: []model.GroupItem{canary},
		RelayConfig: model.GroupRelayConfig{CanaryItemID: canary.ID, CanaryPercent: 50}}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{}, ProbeItemID: canary.ID}
	routeMu.Unlock()
	if _, ok := pickCanaryItem(group); ok {
		t.Fatal("canary member under recovery probe should not be double-loaded")
	}
}

// TestRouteExplanationReportsCanary 校验命中灰度时路由解释给出 canary_rollout 而不是策略名:
// 否则一次刻意安排的灰度样本会被读成"评分恰好选了它", 灰度效果就无从判断。
func TestRouteExplanationReportsCanary(t *testing.T) {
	const groupID = 933
	defer setupRecoveryTest(t, groupID)()

	canary := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "canary", Available: true, Priority: 1}
	group := model.Group{ID: groupID, Mode: model.GroupModeScore, Items: []model.GroupItem{canary},
		RelayConfig: model.GroupRelayConfig{CanaryItemID: canary.ID, CanaryPercent: 100}}

	decision := routeExplanation(group, canary, true)
	if decision.Reason != "canary_rollout" {
		t.Fatalf("reason = %q, want canary_rollout", decision.Reason)
	}
	if plain := routeExplanation(group, canary, false); plain.Reason == "canary_rollout" {
		t.Fatal("non-canary selection must not be reported as canary_rollout")
	}
}
