package op

import (
	"github.com/bestruirui/octopus/internal/model"
	"testing"
)

func TestRateScopeModelsSharedAndSpecific(t *testing.T) {
	ratePolicyMu.Lock()
	old := ratePolicyCache
	ratePolicyCache = []model.RateLimitPolicy{{ScopeType: "user", ScopeID: 1, Enabled: true, RPM: 5}, {ScopeType: "user", ScopeID: 1, ModelName: "a", Enabled: true, RPM: 2}, {ScopeType: "system", Enabled: true, RPM: 10}}
	ratePolicyMu.Unlock()
	defer func() { ratePolicyMu.Lock(); ratePolicyCache = old; ratePolicyMu.Unlock() }()
	a := RateScopeModels("user", 1, "a")
	if len(a) != 2 || a[0] != "" || a[1] != "a" {
		t.Fatalf("shared+specific=%v", a)
	}
	b := RateScopeModels("user", 1, "b")
	if len(b) != 1 || b[0] != "" {
		t.Fatalf("global not shared: %v", b)
	}
	c := RateScopeModels("user", 2, "b")
	if len(c) != 1 || c[0] != "" {
		t.Fatalf("fallback not shared: %v", c)
	}
	rpm, _, _ := RateLimitsFor("user", 1, "")
	if rpm != 5 {
		t.Fatal("shared limit")
	}
	rpm, _, _ = RateLimitsFor("user", 1, "a")
	if rpm != 2 {
		t.Fatal("specific limit")
	}
}
