package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// QuotaUsageProvider 由 relay 包注入的限流器用量查询。
//
// 用注入而不是直接 import relay: op 是 relay 的下游, 反过来引用会形成循环依赖。
// 注入前(snapshot 为 nil)查询会明确报错而不是返回全零 ——
// 把"查不到"显示成"用了 0", 用户会以为自己还有全部额度。
type QuotaUsageProvider func(userID uint, modelName string) (usedRPM, usedTPM, inFlight int64, resetAt time.Time, ok bool)

var quotaUsageProvider QuotaUsageProvider

// RegisterQuotaUsageProvider 由 relay 在启动时注册用量查询实现。
func RegisterQuotaUsageProvider(provider QuotaUsageProvider) {
	quotaUsageProvider = provider
}

// QuotaUsageProviderReady 报告限流用量注入是否就绪。
//
// 供健康检查与测试使用: 注入在 relay 的 init 里完成, 一旦这条接缝断了,
// 接口会 500 而不是给出错误数字 —— 这个判定让问题在一处暴露, 而不是等到用户点开页面。
func QuotaUsageProviderReady() bool {
	return quotaUsageProvider != nil
}

// UserQuotaUsage 汇总某个用户的限流额度与当前用量。
//
// modelName 为空时按"全模型共享额度"口径查询, 与限流器构造 scope 的方式一致。
func UserQuotaUsage(ctx context.Context, userID uint, modelName string) (model.QuotaUsage, error) {
	user, err := UserGetByID(userID)
	if err != nil {
		return model.QuotaUsage{}, fmt.Errorf("查询用户失败: %w", err)
	}

	usage := model.QuotaUsage{
		UserID:   user.ID,
		Username: user.Username,
		Tier:     user.EffectiveTier(),
		Model:    modelName,
	}

	// 额度解析与限流器完全同一逻辑: 先取显式策略, 没有才用等级兜底。
	// 这里若另写一套, 页面显示的额度会和实际限流不一致 —— 那比不显示更糟。
	rpm, tpm, concurrent := RateLimitsFor(model.RateScopeUser, int(userID), modelName)
	source := model.QuotaSourceNone
	if rpm != 0 || tpm != 0 || concurrent != 0 {
		source = model.QuotaSourcePolicy
	} else if limits, ok := UserTierLimitsForUser(userID); ok && !limits.Zero() {
		rpm, tpm, concurrent = limits.RPM, limits.TPM, limits.Concurrent
		source = model.QuotaSourceTier
	}
	usage.RPM, usage.TPM, usage.Concurrent = rpm, tpm, concurrent
	usage.Source = source

	if quotaUsageProvider == nil {
		return model.QuotaUsage{}, fmt.Errorf("限流用量查询未就绪")
	}
	usedRPM, usedTPM, inFlight, resetAt, ok := quotaUsageProvider(userID, modelName)
	if !ok {
		return model.QuotaUsage{}, fmt.Errorf("限流用量查询失败")
	}
	usage.UsedRPM, usage.UsedTPM, usage.InFlight = usedRPM, usedTPM, inFlight
	if !resetAt.IsZero() {
		usage.WindowResetAt = resetAt.Format(time.RFC3339)
	}
	return usage, nil
}

// UserQuotaUsageSelf 用户查询自己的配额用量。
func UserQuotaUsageSelf(ctx context.Context, userID uint, modelName string) (model.QuotaUsage, error) {
	return UserQuotaUsage(ctx, userID, modelName)
}
