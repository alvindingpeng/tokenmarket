package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/ratelimit"
)

// setupRecoveryTest 换上限流宽松的限流器并清空指定分组的路由状态, 返回还原函数。
func setupRecoveryTest(t *testing.T, groupID int) func() {
	t.Helper()
	original := limiter
	limiter = ratelimit.New(func(ratelimit.Scope) ratelimit.Limits {
		return ratelimit.Limits{RPM: 1 << 30, TPM: 1 << 40, Concurrent: 1 << 20}
	})
	ResetRouteState(groupID)
	return func() {
		limiter = original
		ResetRouteState(groupID)
	}
}

// TestExpiredCooldownMemberReturnsToPool 校验冷却到期的成员重新参与选路。
//
// 冷却条目只在探测成功或成员被移除时才删除; 早先把冷却到期的成员一律挡在候选之外、
// 又只在它优先级压过全部健康成员时才放一个探测, 于是优先级最低的成员失败一次后
// 就再也拿不到流量, 价格优先策略形同虚设(实测: 最便宜的渠道冷却到期后一小时零流量)。
func TestExpiredCooldownMemberReturnsToPool(t *testing.T) {
	const groupID = 910
	defer setupRecoveryTest(t, groupID)()

	cheap := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "cheap", Available: true, Priority: 5, UserPrice: model.LLMPrice{Input: 1, Output: 1}}
	dear := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "dear", Available: true, Priority: 1, UserPrice: model.LLMPrice{Input: 9, Output: 9}}
	group := model.Group{ID: groupID, Mode: model.GroupModePrice, Items: []model.GroupItem{dear, cheap},
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModePrice)}

	// 最便宜的成员优先级最低, 且它的冷却在 10 秒前就已到期。
	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{
		cheap.ID: time.Now().Add(-10 * time.Second).UnixMilli(),
	}}
	routeMu.Unlock()

	if chosen := pickGroupItem(group); chosen.ID != cheap.ID {
		t.Fatalf("冷却到期的最便宜成员未回到候选池, 选中了 %d", chosen.ID)
	}
}

// TestActiveCooldownMemberIsStillSkipped 校验冷却尚未到期的成员依旧被跳过, 避免上面的修复放行真正的故障成员。
func TestActiveCooldownMemberIsStillSkipped(t *testing.T) {
	const groupID = 911
	defer setupRecoveryTest(t, groupID)()

	cheap := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "cheap", Available: true, Priority: 5, UserPrice: model.LLMPrice{Input: 1, Output: 1}}
	dear := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "dear", Available: true, Priority: 1, UserPrice: model.LLMPrice{Input: 9, Output: 9}}
	group := model.Group{ID: groupID, Mode: model.GroupModePrice, Items: []model.GroupItem{dear, cheap},
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModePrice)}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{
		cheap.ID: time.Now().Add(time.Minute).UnixMilli(),
	}}
	routeMu.Unlock()

	if chosen := pickGroupItem(group); chosen.ID != dear.ID {
		t.Fatalf("冷却中的成员不应被选中, 选中了 %d", chosen.ID)
	}
}

// TestRecoveredTopPriorityMemberIsProbedFirst 校验故障转移语义未被破坏:
// 优先级最高的成员冷却到期时仍先放一个恢复探测, 而不是直接吃满流量。
func TestRecoveredTopPriorityMemberIsProbedFirst(t *testing.T) {
	const groupID = 912
	defer setupRecoveryTest(t, groupID)()

	top := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "top", Available: true, Priority: 1}
	other := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "other", Available: true, Priority: 2}
	group := model.Group{ID: groupID, Mode: model.GroupModeFailover, Items: []model.GroupItem{top, other},
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{
		top.ID: time.Now().Add(-time.Second).UnixMilli(),
	}}
	routeMu.Unlock()

	chosen := pickGroupItem(group)
	if chosen.ID != top.ID {
		t.Fatalf("优先级最高的恢复成员应拿到探测, 选中了 %d", chosen.ID)
	}
	routeMu.Lock()
	probe := routes[groupID].ProbeItemID
	routeMu.Unlock()
	if probe != top.ID {
		t.Fatalf("探测占用应落在恢复成员上, 实际 %d", probe)
	}
}

// TestRecordRouteSuccessClearsCooldown 校验一次成功即清掉残留的冷却条目,
// 否则该成员会一直挂在冷却表里反复占用恢复探测名额。
func TestRecordRouteSuccessClearsCooldown(t *testing.T) {
	const groupID = 913
	defer setupRecoveryTest(t, groupID)()

	item := model.GroupItem{ID: 7, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "m", Available: true, Priority: 1}
	group := model.Group{ID: groupID, Mode: model.GroupModePrice, Items: []model.GroupItem{item},
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModePrice)}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{item.ID: time.Now().Add(-time.Second).UnixMilli()}}
	routeMu.Unlock()

	recordRouteSuccess(group, item.ID)

	routeMu.Lock()
	_, cooling := routes[groupID].Cooldowns[item.ID]
	routeMu.Unlock()
	if cooling {
		t.Fatal("成功后冷却条目应被清除")
	}
}

// TestRouteExplanationMatchesRouting 校验选路决策里记录的排除原因与真实选路一致:
// 冷却中的成员记 cooldown, 冷却到期的成员不再是"看似可用却被跳过"。
func TestRouteExplanationMatchesRouting(t *testing.T) {
	const groupID = 914
	defer setupRecoveryTest(t, groupID)()

	cooling := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "cooling", Available: true, Priority: 1}
	recovered := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "recovered", Available: true, Priority: 2}
	group := model.Group{ID: groupID, Mode: model.GroupModeFailover, Items: []model.GroupItem{cooling, recovered},
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)}

	routeMu.Lock()
	routes[groupID] = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{
		cooling.ID:   time.Now().Add(time.Minute).UnixMilli(),
		recovered.ID: time.Now().Add(-time.Minute).UnixMilli(),
	}}
	routeMu.Unlock()

	chosen := pickGroupItem(group)
	decision := routeExplanation(group, chosen)
	reasons := map[int]string{}
	for _, c := range decision.Candidates {
		reasons[c.ItemID] = c.Reason
	}
	if reasons[cooling.ID] != "cooldown" {
		t.Fatalf("冷却中的成员应记 cooldown, 实际 %q", reasons[cooling.ID])
	}
	if reasons[recovered.ID] != "selected" {
		t.Fatalf("冷却到期的成员应参与选路并被选中, 实际 %q", reasons[recovered.ID])
	}
}
