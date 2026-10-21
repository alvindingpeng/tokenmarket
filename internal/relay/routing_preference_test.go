package relay

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"testing"
	"time"
)

func TestEffectivePreferencePreservesManualAndOriginalGroup(t *testing.T) {
	original := model.Group{Mode: model.GroupModeManual, ActiveItemID: 3}
	got := effectiveGroupForRequest(original, model.RoutingPreferencePrice)
	if got.Mode != original.Mode || got.ActiveItemID != 3 {
		t.Fatal("preference overrode manual pin")
	}
	original.Mode = model.GroupModeFailover
	got = effectiveGroupForRequest(original, model.RoutingPreferencePrice)
	if got.Mode != model.GroupModePrice || original.Mode != model.GroupModeFailover {
		t.Fatal("preference mutated original group or was not applied")
	}
}

func TestExplicitPreferenceDoesNotInheritOtherStrategyAffinity(t *testing.T) {
	const id = 9811
	defer setupRecoveryTest(t, id)()
	dear := model.GroupItem{ID: 1, Available: true, Priority: 1, UserPrice: model.LLMPrice{Input: 9, Output: 9}}
	cheap := model.GroupItem{ID: 2, Available: true, Priority: 2, UserPrice: model.LLMPrice{Input: 1, Output: 1}}
	group := model.Group{ID: id, Mode: model.GroupModeFailover, Items: []model.GroupItem{dear, cheap}, RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)}
	routeMu.Lock()
	routes[id] = &RouteState{GroupID: id, CurrentItemID: dear.ID, AffinityUntil: time.Now().Add(time.Minute).UnixMilli(), Cooldowns: map[int]int64{}}
	routeMu.Unlock()
	if got := pickGroupItem(effectiveGroupForRequest(group, model.RoutingPreferencePrice)); got.ID != cheap.ID {
		t.Fatalf("shared affinity overrode explicit preference: got %d", got.ID)
	}
}
