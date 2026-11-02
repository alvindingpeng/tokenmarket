package op

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func tierTestSetup(t *testing.T, rawPolicies string) {
	t.Helper()
	breakerTestSetup(t)
	if err := SettingUpsert(model.SettingKeyUserTierPolicies, rawPolicies); err != nil {
		t.Fatalf("seed tier policies: %v", err)
	}
}

func TestUserTierLimitsForUserResolvesEffectiveTier(t *testing.T) {
	tierTestSetup(t, `{"free":{"rpm":5,"tpm":500,"concurrent":1},"pro":{"rpm":50,"tpm":5000,"concurrent":5}}`)
	user, err := UserCreate("tier-pro", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// 默认创建时 tier 为空 -> 生效 free。
	limits, ok := UserTierLimitsForUser(user.ID)
	if !ok {
		t.Fatal("user must resolve")
	}
	if limits.RPM != 5 {
		t.Fatalf("default tier rpm=%d, want 5 (free fallback)", limits.RPM)
	}
	if err := UserUpdateTier(user.ID, model.TierPro); err != nil {
		t.Fatalf("update tier: %v", err)
	}
	limits, _ = UserTierLimitsForUser(user.ID)
	if limits.RPM != 50 || limits.Concurrent != 5 {
		t.Fatalf("pro tier limits = %+v, want rpm=50 concurrent=5", limits)
	}
}

func TestUserTierLimitsFallsBackToDefaultsWhenSettingCorrupt(t *testing.T) {
	tierTestSetup(t, `not-json`)
	// 配置写坏时必须回落到出厂默认, 不能返回空表(那等于全平台无限制)。
	limits := UserTierLimits(model.TierFree)
	if limits.RPM != model.DefaultUserTierPolicies[model.TierFree].RPM {
		t.Fatalf("corrupt setting must fall back to defaults, got %+v", limits)
	}
}

func TestUserUpdateTierRejectsInvalid(t *testing.T) {
	tierTestSetup(t, model.DefaultUserTierPoliciesJSON)
	user, err := UserCreate("tier-bad", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := UserUpdateTier(user.ID, "gold"); err == nil {
		t.Fatal("invalid tier must be rejected")
	}
	// 空串表示清空等级, 允许。
	if err := UserUpdateTier(user.ID, ""); err != nil {
		t.Fatalf("clearing tier must be allowed: %v", err)
	}
	fetched, _ := UserGetByID(user.ID)
	if fetched.EffectiveTier() != model.TierFree {
		t.Fatalf("cleared tier effective=%q, want free", fetched.EffectiveTier())
	}
}
