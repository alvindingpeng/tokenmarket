package relay

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestRouteStateIsScopedByGroupAndManualMode(t *testing.T) {
	ResetRouteState(9201)
	ResetRouteState(9202)
	t.Cleanup(func() { ResetRouteState(9201); ResetRouteState(9202) })

	routeMu.Lock()
	routes[9201] = &RouteState{GroupID: 9201, CurrentItemID: 4, Cooldowns: map[int]int64{4: time.Now().Add(time.Minute).UnixMilli()}}
	routeMu.Unlock()
	state := RouteStateOf(model.Group{ID: 9202, Mode: model.GroupModePrice})
	if state.CurrentItemID != 0 || len(state.Cooldowns) != 0 {
		t.Fatalf("route state leaked across groups: %+v", state)
	}
	manual := RouteStateOf(model.Group{ID: 9201, Mode: model.GroupModeManual, ActiveItemID: 7})
	if manual.CurrentItemID != 7 || len(manual.Cooldowns) != 0 {
		t.Fatalf("manual route must derive only from persisted selection: %+v", manual)
	}
}

func TestResetAllRouteStatesClearsEveryGroup(t *testing.T) {
	ResetRouteState(9211)
	ResetRouteState(9212)
	defer ResetRouteState(9211)
	defer ResetRouteState(9212)
	routeMu.Lock()
	routes[9211] = &RouteState{GroupID: 9211, Cooldowns: map[int]int64{1: time.Now().Add(time.Minute).UnixMilli()}}
	routes[9212] = &RouteState{GroupID: 9212, Cooldowns: map[int]int64{2: time.Now().Add(time.Minute).UnixMilli()}}
	routeMu.Unlock()
	ResetAllRouteStates()
	for _, id := range []int{9211, 9212} {
		state := RouteStateOf(model.Group{ID: id, Mode: model.GroupModePrice})
		if state.CurrentItemID != 0 || len(state.Cooldowns) != 0 {
			t.Fatalf("route state %d survived global reset: %+v", id, state)
		}
	}
}

func TestEgressStatesAreIndependentAndResettable(t *testing.T) {
	resetEgressStates()
	t.Cleanup(resetEgressStates)
	coolEgress(9301, 60)
	if !egressCooling(9301) {
		t.Fatal("first egress should cool")
	}
	if egressCooling(9302) {
		t.Fatal("cooldown must not leak to a different egress ID")
	}
	resetEgressStates()
	if egressCooling(9301) || egressCooling(9302) {
		t.Fatal("reset must clear all egress runtime state")
	}
}
