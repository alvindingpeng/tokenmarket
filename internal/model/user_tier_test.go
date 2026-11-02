package model

import (
	"encoding/json"
	"testing"
)

func TestIsValidUserTier(t *testing.T) {
	for _, tier := range UserTiers {
		if !IsValidUserTier(tier) {
			t.Fatalf("tier %q must be valid", tier)
		}
	}
	for _, invalid := range []UserTier{"", "gold", "FREE "} {
		if IsValidUserTier(invalid) {
			t.Fatalf("tier %q must be rejected", invalid)
		}
	}
}

func TestEffectiveTierFallsBackToFree(t *testing.T) {
	// 空串是存量账号的列值: 必须回落 free, 不能当成"无限制"。
	if got := (&User{}).EffectiveTier(); got != TierFree {
		t.Fatalf("empty tier = %q, want %q", got, TierFree)
	}
	if got := (&User{Tier: TierPro}).EffectiveTier(); got != TierPro {
		t.Fatalf("explicit tier = %q, want %q", got, TierPro)
	}
	// 库里出现未知值时同样按 free 兜底。
	if got := (&User{Tier: "gold"}).EffectiveTier(); got != TierFree {
		t.Fatalf("unknown tier = %q, want %q", got, TierFree)
	}
}

func TestParseUserTierPolicies(t *testing.T) {
	policies, err := ParseUserTierPolicies(`{"free":{"rpm":10,"tpm":1000,"concurrent":1},"pro":{"rpm":100,"tpm":0,"concurrent":4}}`)
	if err != nil {
		t.Fatalf("parse valid policies: %v", err)
	}
	if policies[TierFree].RPM != 10 || policies[TierPro].Concurrent != 4 {
		t.Fatalf("unexpected parsed policies: %+v", policies)
	}
	if _, ok := policies[TierBasic]; ok {
		t.Fatal("absent tier must not be fabricated")
	}
}

func TestParseUserTierPoliciesRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"not json":         `nope`,
		"unknown tier":     `{"gold":{"rpm":1}}`,
		"negative limit":   `{"free":{"rpm":-1}}`,
		"wrong value type": `{"free":{"rpm":"fast"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseUserTierPolicies(raw); err == nil {
				t.Fatalf("expected error for %q", raw)
			}
		})
	}
}

func TestParseUserTierPoliciesEmptyIsNoop(t *testing.T) {
	policies, err := ParseUserTierPolicies("   ")
	if err != nil {
		t.Fatalf("empty policies must not error: %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("empty policies = %+v, want empty", policies)
	}
}

func TestDefaultUserTierPoliciesJSONRoundTrips(t *testing.T) {
	var wire map[string]TierLimits
	if err := json.Unmarshal([]byte(DefaultUserTierPoliciesJSON), &wire); err != nil {
		t.Fatalf("default JSON must be valid: %v", err)
	}
	parsed, err := ParseUserTierPolicies(DefaultUserTierPoliciesJSON)
	if err != nil {
		t.Fatalf("default JSON must parse: %v", err)
	}
	if len(parsed) != len(UserTiers) {
		t.Fatalf("default policies must cover all tiers, got %d", len(parsed))
	}
	// enterprise 默认不限制, free 必须有限制 —— 默认值写反会让所有新用户无上限。
	if !parsed[TierEnterprise].Zero() {
		t.Fatal("enterprise must be unlimited by default")
	}
	if parsed[TierFree].Zero() {
		t.Fatal("free must be limited by default")
	}
}
