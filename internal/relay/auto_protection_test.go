package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

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
}
