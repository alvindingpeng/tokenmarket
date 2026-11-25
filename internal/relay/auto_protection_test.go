package relay

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestAutoTaskRampAndPerformanceIsolation(t *testing.T) {
	group := model.Group{ID: 904, RelayConfig: model.GroupRelayConfig{
		AutoTaskRampPercent: map[string]int{AutoTaskCoding: 0}, AutoRampPercent: 100,
	}}
	t.Cleanup(func() { ResetAutoState(group.ID) })
	if autoTaskRampHit(group, AutoTaskCoding, 1, 1) || !autoTaskRampHit(group, AutoTaskGeneral, 1, 1) {
		t.Fatal("explicit zero must be local to the configured task")
	}
	task := autoTask{name: AutoTaskCoding, input: 20_000, stream: true, protocol: model.ProtocolOpenAIResponse}
	for sample := 0; sample < 5; sample++ {
		recordAutoPerformance(group, 1, task, "", true, 31*time.Second)
	}
	if got := autoPerformancePenalty(group.ID, 1, task); got >= 1 || got < 0.5 {
		t.Fatalf("slow stream penalty = %v", got)
	}
	recordAutoPerformance(group, 1, task, ErrorClassContext, false, 0)
	if got := autoPerformancePenalty(group.ID, 1, task); got != 0.5 {
		t.Fatalf("context failure penalty = %v", got)
	}
	if autoPerformancePenalty(group.ID, 2, task) != 1 {
		t.Fatal("unrelated member inherited a penalty")
	}
	task.input = 100
	if autoPerformancePenalty(group.ID, 1, task) != 1 {
		t.Fatal("short requests inherited a long-input penalty")
	}
}

func TestAutoRampZeroPreservesLegacyActiveBehaviorAndBucketsSafely(t *testing.T) {
	// Existing active configurations predate auto_ramp_percent. Their omitted
	// zero value must remain a full rollout.
	if !autoRampHit(37, 0, 1, 0) || !autoRampHit(37, 0, 1, 100) {
		t.Fatal("legacy or 100 percent active routing must serve every request")
	}
	if autoRampHit(37, 0, 1, 1) == autoRampHit(37, 0, 1, 100) {
		t.Fatal("one percent unexpected full rollout")
	}
	// Stable API-key buckets prevent one client from alternating routes.
	if autoRampHit(37, 9, 1, 25) != autoRampHit(37, 9, 1, 25) {
		t.Fatal("ramp bucket is not stable")
	}
}

func TestAutoCircuitTripsThenClearsOnSuccess(t *testing.T) {
	task := AutoTaskGeneral
	groupID := 88001
	autoCircuitRecordSuccess(groupID, task)
	group := model.Group{ID: groupID, RelayConfig: model.GroupRelayConfig{
		AutoFallbackFailures: 2,
		AutoFallbackSeconds:  60,
	}}
	trip := autoCircuitRecordFailure(group, task)
	if trip || autoCircuitOpen(groupID, task) {
		t.Fatal("first failure must not open the circuit")
	}
	trip = autoCircuitRecordFailure(group, task)
	if !trip || !autoCircuitOpen(groupID, task) {
		t.Fatal("configured failure threshold must open the circuit")
	}
	autoCircuitRecordSuccess(groupID, task)
	if autoCircuitOpen(groupID, task) {
		t.Fatal("success must clear the circuit")
	}
}

func TestAutoContextOverrideCanQualifyUnknownContext(t *testing.T) {
	item := model.GroupItem{ID: 9, ChannelGrantID: 9, Available: true, Kind: model.MediaKindText, Protocols: model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse}
	task := autoTask{name: AutoTaskGeneral, input: 100, output: 100}
	// A fake grant is rejected before context matching. The integration path
	// in auto_test covers the real override ordering with a persisted grant.
	if reason := autoExclusion(item, task, true, true, 0); reason == "" {
		t.Fatal("missing grant unexpectedly qualified")
	}
}

func TestAutoFallbackDefaultsAreBounded(t *testing.T) {
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	config.AutoFallbackFailures = 0
	config.AutoFallbackSeconds = 0
	config.AutoRampPercent = -1
	model.NormalizeGroupRelayConfigForMode(&config, model.GroupModeScore)
	if config.AutoFallbackFailures != 0 || config.AutoFallbackSeconds != 0 || config.AutoRampPercent != 0 {
		t.Fatalf("unexpected normalized config: %+v", config)
	}
	config.AutoFallbackFailures = 101
	config.AutoFallbackSeconds = 9999
	model.NormalizeGroupRelayConfigForMode(&config, model.GroupModeScore)
	if config.AutoFallbackFailures != 100 || config.AutoFallbackSeconds != 3600 {
		t.Fatalf("configured fallback bounds were not applied: %+v", config)
	}
	config.AutoStickySeconds = 9999
	config.AutoExplorePercent = 99
	model.NormalizeGroupRelayConfigForMode(&config, model.GroupModeScore)
	if config.AutoStickySeconds != 3600 || config.AutoExplorePercent != 20 {
		t.Fatalf("new routing bounds were not applied: %+v", config)
	}
}

func TestAutoStickyUsesCurrentKeyAndTaskAndClears(t *testing.T) {
	ResetAutoState(99101)
	group := model.Group{ID: 99101, RelayConfig: model.GroupRelayConfig{AutoStickySeconds: 10}}
	autoStickyRecordSuccess(group, 7, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion, 42)
	if itemID, ok := autoStickyGet(group, 7, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion); !ok || itemID != 42 {
		t.Fatalf("sticky hit=(%d,%t), want (42,true)", itemID, ok)
	}
	if _, ok := autoStickyGet(group, 8, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion); ok {
		t.Fatal("different API key inherited another session's sticky member")
	}
	if _, ok := autoStickyGet(group, 7, 3, AutoTaskSummary, model.ProtocolOpenAIChatCompletion); ok {
		t.Fatal("different task inherited another task's sticky member")
	}
	if _, ok := autoStickyGet(group, 7, 3, AutoTaskCoding, model.ProtocolAnthropicMessage); ok {
		t.Fatal("different protocol inherited another protocol's sticky member")
	}
	autoStickyRecordSuccess(group, 7, 3, AutoTaskCoding, model.ProtocolAnthropicMessage, 84)
	if itemID, ok := autoStickyGet(group, 7, 3, AutoTaskCoding, model.ProtocolAnthropicMessage); !ok || itemID != 84 {
		t.Fatalf("anthropic sticky hit=(%d,%t), want (84,true)", itemID, ok)
	}
	autoStickyClear(group.ID, 7, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion)
	if _, ok := autoStickyGet(group, 7, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion); ok {
		t.Fatal("failure clear left sticky member behind")
	}
	if itemID, ok := autoStickyGet(group, 7, 3, AutoTaskCoding, model.ProtocolAnthropicMessage); !ok || itemID != 84 {
		t.Fatalf("clearing chat sticky cleared anthropic sticky: (%d,%t)", itemID, ok)
	}
}

func TestAutoStickyDisabledPreservesLegacyBehavior(t *testing.T) {
	group := model.Group{ID: 99102, RelayConfig: model.GroupRelayConfig{}}
	autoStickyRecordSuccess(group, 7, 3, AutoTaskGeneral, model.ProtocolOpenAIChatCompletion, 42)
	if _, ok := autoStickyGet(group, 7, 3, AutoTaskGeneral, model.ProtocolOpenAIChatCompletion); ok {
		t.Fatal("disabled sticky routing created session state")
	}
}

func TestAutoStickyMemberStillRequiresCurrentCandidateEligibility(t *testing.T) {
	eligible := []model.GroupItem{{ID: 1}, {ID: 2}}
	if item, ok := autoItemByID(eligible, 2); !ok || item.ID != 2 {
		t.Fatal("eligible sticky member was not found")
	}
	if _, ok := autoItemByID(eligible, 99); ok {
		t.Fatal("sticky member bypassed candidate eligibility")
	}
}

func TestAutoExploreIsStableAndBounded(t *testing.T) {
	if autoExploreHit(99103, 9, 1, 0, AutoTaskCoding) {
		t.Fatal("zero exploration unexpectedly selected a member")
	}
	if got := autoExploreHit(99103, 9, 1, 99, AutoTaskCoding); got != autoExploreHit(99103, 9, 1, 20, AutoTaskCoding) {
		t.Fatal("exploration above the cap should behave as 20 percent")
	}
	if autoExploreHit(99103, 9, 1, 20, AutoTaskCoding) != autoExploreHit(99103, 9, 1, 20, AutoTaskCoding) {
		t.Fatal("exploration bucket is not stable")
	}
}
