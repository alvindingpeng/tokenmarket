package model

import "testing"

func TestRoutingPreferenceMapsToExistingModes(t *testing.T) {
	inherited := GroupModeFailover
	cases := map[string]GroupMode{
		"": GroupModeFailover, RoutingPreferenceInherit: GroupModeFailover,
		RoutingPreferenceBalanced: GroupModeScore, RoutingPreferencePrice: GroupModePrice,
		RoutingPreferenceLatency: GroupModeLatency, RoutingPreferenceSuccess: GroupModeSuccess,
	}
	for preference, want := range cases {
		if got := GroupModeForRoutingPreference(preference, inherited); got != want {
			t.Fatalf("%q: got %q, want %q", preference, got, want)
		}
	}
	if IsValidRoutingPreference("bogus") {
		t.Fatal("unknown preference accepted")
	}
}

func TestNormalizeGroupRelayConfigClampsUnsafeMemberValues(t *testing.T) {
	config := GroupRelayConfig{MemberMaxAttempts: 1 << 30, MemberRetryIntervalSeconds: 1 << 30, MemberNonStreamResponseTimeoutSeconds: 1 << 30, MemberStreamFirstEventTimeoutSeconds: 1 << 30, MemberCooldownSeconds: 1 << 30, MemberAffinitySeconds: 1 << 30, ReliabilityFloor: 1 << 30}
	NormalizeGroupRelayConfigForMode(&config, GroupModeScore)
	if config.MemberMaxAttempts != 100 || config.MemberRetryIntervalSeconds != 300 || config.MemberNonStreamResponseTimeoutSeconds != 3600 || config.MemberStreamFirstEventTimeoutSeconds != 3600 || config.MemberCooldownSeconds != 86400 || config.MemberAffinitySeconds != 3600 || config.ReliabilityFloor != 100 {
		t.Fatalf("unsafe values were not clamped: %+v", config)
	}
}
