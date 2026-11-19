package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// quotaTestSetup 建库 + 用户 + 指定等级, 并注入一个可控的用量 provider。
func quotaUsageTestSetup(t *testing.T, tier model.UserTier) model.User {
	t.Helper()
	breakerTestSetup(t)
	// 用户名索引是进程级缓存: 每个测试各自建库, 不清会让第二个测试撞"用户名已存在"。
	userCache.Clear()
	userNameIndex.Clear()
	if err := SettingUpsert(model.SettingKeyUserTierPolicies, model.DefaultUserTierPoliciesJSON); err != nil {
		t.Fatalf("seed tier policies: %v", err)
	}
	user, err := UserCreate("quota-user", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if tier != "" {
		if err := UserUpdateTier(uint(user.ID), tier); err != nil {
			t.Fatalf("set tier: %v", err)
		}
	}
	return user
}

// stubQuotaUsage 注入固定用量, 测试结束后恢复为未注入状态。
func stubQuotaUsage(t *testing.T, usedRPM, usedTPM, inFlight int64) {
	t.Helper()
	prev := quotaUsageProvider
	quotaUsageProvider = func(uint, string) (int64, int64, int64, time.Time, bool) {
		return usedRPM, usedTPM, inFlight, time.Unix(1700000060, 0), true
	}
	t.Cleanup(func() { quotaUsageProvider = prev })
}

// TestQuotaUsageSourceIsTier 没有显式策略时, 来源必须标成 tier, 额度取等级值。
func TestQuotaUsageSourceIsTier(t *testing.T) {
	user := quotaUsageTestSetup(t, model.TierPro)
	stubQuotaUsage(t, 10, 1000, 2)

	usage, err := UserQuotaUsage(context.Background(), uint(user.ID), "")
	if err != nil {
		t.Fatalf("quota usage: %v", err)
	}
	if usage.Source != model.QuotaSourceTier {
		t.Fatalf("source = %q, want %q", usage.Source, model.QuotaSourceTier)
	}
	limits := model.DefaultUserTierPolicies[model.TierPro]
	if usage.RPM != limits.RPM || usage.TPM != limits.TPM || usage.Concurrent != limits.Concurrent {
		t.Fatalf("limits = %+v, want %+v", usage, limits)
	}
	if usage.Tier != model.TierPro {
		t.Fatalf("tier = %q, want pro", usage.Tier)
	}
}

// TestQuotaUsageSourceIsPolicy 显式策略必须压过等级兜底 —— 否则页面会显示等级额度,
// 而实际限流用的是策略额度, 两者不一致会让排查完全走错方向。
func TestQuotaUsageSourceIsPolicy(t *testing.T) {
	user := quotaUsageTestSetup(t, model.TierPro)
	stubQuotaUsage(t, 5, 500, 1)

	policy := model.RateLimitPolicy{
		ScopeType: model.RateScopeUser, ScopeID: int(user.ID),
		RPM: 7, TPM: 700, Concurrent: 3, Enabled: true,
	}
	if err := db.GetDB().Create(&policy).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := RatePolicyInit(); err != nil {
		t.Fatalf("reload policies: %v", err)
	}

	usage, err := UserQuotaUsage(context.Background(), uint(user.ID), "")
	if err != nil {
		t.Fatalf("quota usage: %v", err)
	}
	if usage.Source != model.QuotaSourcePolicy {
		t.Fatalf("source = %q, want %q", usage.Source, model.QuotaSourcePolicy)
	}
	if usage.RPM != 7 || usage.TPM != 700 || usage.Concurrent != 3 {
		t.Fatalf("policy limits = rpn%d tpm%d conc%d, want 7/700/3", usage.RPM, usage.TPM, usage.Concurrent)
	}
}

// TestQuotaUsageUnregisteredProviderFails 未注入时必须报错, 绝不能返回全零用量。
func TestQuotaUsageUnregisteredProviderFails(t *testing.T) {
	user := quotaUsageTestSetup(t, model.TierFree)
	prev := quotaUsageProvider
	quotaUsageProvider = nil
	t.Cleanup(func() { quotaUsageProvider = prev })

	if _, err := UserQuotaUsage(context.Background(), uint(user.ID), ""); err == nil {
		t.Fatal("missing provider must be an error, not zero usage")
	}
}

// TestQuotaUsageUnknownUserFails 用户不存在时报错, 而不是返回空结构。
func TestQuotaUsageUnknownUserFails(t *testing.T) {
	quotaUsageTestSetup(t, model.TierFree)
	stubQuotaUsage(t, 0, 0, 0)
	if _, err := UserQuotaUsage(context.Background(), 999999, ""); err == nil {
		t.Fatal("unknown user must be an error")
	}
}

// TestQuotaRemainNeverNegative 用量超过额度时剩余量取 0, 不出现负数。
func TestQuotaRemainNeverNegative(t *testing.T) {
	usage := model.QuotaUsage{RPM: 10, TPM: 100, Concurrent: 2, UsedRPM: 25, UsedTPM: 100, InFlight: 5}
	rpm, tpm, concurrent := usage.QuotaRemain()
	if rpm != 0 || tpm != 0 || concurrent != 0 {
		t.Fatalf("remain = %d/%d/%d, want all zero", rpm, tpm, concurrent)
	}
}

// TestQuotaRemainUnlimitedIsZero 额度为 0 表示不限制, 剩余量按 0 表达(前端据此显示"不限制")。
func TestQuotaRemainUnlimitedIsZero(t *testing.T) {
	usage := model.QuotaUsage{RPM: 0, TPM: 0, Concurrent: 0, UsedRPM: 100, UsedTPM: 900, InFlight: 3}
	rpm, tpm, concurrent := usage.QuotaRemain()
	if rpm != 0 || tpm != 0 || concurrent != 0 {
		t.Fatalf("unlimited remain must be 0, got %d/%d/%d", rpm, tpm, concurrent)
	}
}

// TestQuotaRemainNormal 常规情况按 limit-used 计算。
func TestQuotaRemainNormal(t *testing.T) {
	usage := model.QuotaUsage{RPM: 20, TPM: 1000, Concurrent: 4, UsedRPM: 5, UsedTPM: 250, InFlight: 1}
	rpm, tpm, concurrent := usage.QuotaRemain()
	if rpm != 15 || tpm != 750 || concurrent != 3 {
		t.Fatalf("remain = %d/%d/%d, want 15/750/3", rpm, tpm, concurrent)
	}
}
