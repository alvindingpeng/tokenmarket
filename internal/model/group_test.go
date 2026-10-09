package model

import "testing"

func TestDefaultGroupRelayConfigForMode(t *testing.T) {
	tests := []struct {
		mode                       GroupMode
		affinity, cooldown, window int
	}{
		{GroupModeManual, 0, 60, 20},
		{GroupModeFailover, 60, 60, 20},
		{GroupModePrice, 0, 60, 20},
		{GroupModeLatency, 0, 60, 10},
		{GroupModeSuccess, 0, 60, 30},
		{GroupModeScore, 0, 60, 20},
		{GroupModeRandom, 0, 30, 20},
	}
	for _, tt := range tests {
		config := DefaultGroupRelayConfigForMode(tt.mode)
		if config.MemberMaxAttempts != 1 || config.MemberRetryIntervalSeconds != 1 {
			t.Errorf("%s retry defaults = %d/%d, want 1/1", tt.mode, config.MemberMaxAttempts, config.MemberRetryIntervalSeconds)
		}
		if config.MemberAffinitySeconds != tt.affinity || config.MemberCooldownSeconds != tt.cooldown || config.MetricWindowSize != tt.window {
			t.Errorf("%s defaults = affinity %d cooldown %d window %d", tt.mode, config.MemberAffinitySeconds, config.MemberCooldownSeconds, config.MetricWindowSize)
		}
	}
}

func TestNormalizeGroupRelayConfigForModePreservesExplicitValues(t *testing.T) {
	config := GroupRelayConfig{MemberAffinitySeconds: 12, MetricWindowSize: 15}
	NormalizeGroupRelayConfigForMode(&config, GroupModeLatency)
	if config.MemberAffinitySeconds != 12 || config.MetricWindowSize != 15 || config.MemberMaxAttempts != 1 {
		t.Fatalf("normalized config did not preserve explicit values: %+v", config)
	}
}
