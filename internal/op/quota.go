package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// QuotaExceeded 配额超限错误。
type QuotaExceeded struct {
	Kind    string // requests/input_tokens/output_tokens
	Current int64
	Limit   int64
}

func (e QuotaExceeded) Error() string {
	return fmt.Sprintf("quota exceeded: %s %d/%d", e.Kind, e.Current, e.Limit)
}

// CheckAPIKeyQuota 检查 API Key 的配额是否超限; 0 表示无限制, 不检查。
// 返回 QuotaExceeded 错误表示超限, 其他错误表示数据库故障。
// CheckTenantQuota 检查租户级配额是否超限; 0 表示无限制, 不检查。
// 返回 QuotaExceeded 错误表示超限, 其他错误表示数据库故障。
func CheckTenantQuota(ctx context.Context, tenantID uint) error {
	if tenantID == 0 {
		return nil
	}
	quota, err := TenantQuotaGet(int(tenantID))
	if err != nil {
		return fmt.Errorf("failed to get tenant quota: %w", err)
	}

	// 全 0 表示无配额限制
	if quota.RequestsLimit == 0 && quota.InputTokensLimit == 0 && quota.OutputTokensLimit == 0 {
		return nil
	}

	// 获取租户当前周期的用量
	usage, err := loadTenantQuotaUsage(ctx, tenantID, tenantQuotaResetTime(time.Now(), quota.ResetDay, quota.Period))
	if err != nil {
		return fmt.Errorf("failed to query tenant quota usage: %w", err)
	}

	// 检查各项配额
	if quota.RequestsLimit > 0 && usage.Requests >= quota.RequestsLimit {
		return QuotaExceeded{Kind: "requests", Current: usage.Requests, Limit: quota.RequestsLimit}
	}
	if quota.InputTokensLimit > 0 && usage.InputTokens >= quota.InputTokensLimit {
		return QuotaExceeded{Kind: "input_tokens", Current: usage.InputTokens, Limit: quota.InputTokensLimit}
	}
	if quota.OutputTokensLimit > 0 && usage.OutputTokens >= quota.OutputTokensLimit {
		return QuotaExceeded{Kind: "output_tokens", Current: usage.OutputTokens, Limit: quota.OutputTokensLimit}
	}

	return nil
}

func CheckAPIKeyQuota(ctx context.Context, apiKeyID int) error {
	apikey, ok := apiKeyCache.Get(apiKeyID)
	if !ok {
		return fmt.Errorf("apikey not found")
	}

	// 全 0 表示无配额限制
	if apikey.QuotaRequests == 0 && apikey.QuotaInputTokens == 0 && apikey.QuotaOutputTokens == 0 {
		return nil
	}

	usage, err := loadAPIKeyQuotaUsage(ctx, apiKeyID, apiKeyQuotaResetTime(time.Now(), apikey.QuotaResetHour))

	if err != nil {
		return fmt.Errorf("failed to query quota usage: %w", err)
	}

	// 检查各项配额
	if apikey.QuotaRequests > 0 && usage.Requests >= apikey.QuotaRequests {
		return QuotaExceeded{Kind: "requests", Current: usage.Requests, Limit: apikey.QuotaRequests}
	}
	if apikey.QuotaInputTokens > 0 && usage.InputTokens >= apikey.QuotaInputTokens {
		return QuotaExceeded{Kind: "input_tokens", Current: usage.InputTokens, Limit: apikey.QuotaInputTokens}
	}
	if apikey.QuotaOutputTokens > 0 && usage.OutputTokens >= apikey.QuotaOutputTokens {
		return QuotaExceeded{Kind: "output_tokens", Current: usage.OutputTokens, Limit: apikey.QuotaOutputTokens}
	}

	return nil
}

// GetAPIKeyQuotaUsage 获取 API Key 当前周期的配额用量, 供界面展示。
func GetAPIKeyQuotaUsage(ctx context.Context, apiKeyID int) (requests, inputTokens, outputTokens int64, err error) {
	apikey, ok := apiKeyCache.Get(apiKeyID)
	if !ok {
		return 0, 0, 0, fmt.Errorf("apikey not found")
	}

	usage, err := loadAPIKeyQuotaUsage(ctx, apiKeyID, apiKeyQuotaResetTime(time.Now(), apikey.QuotaResetHour))

	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query quota usage: %w", err)
	}

	return usage.Requests, usage.InputTokens, usage.OutputTokens, nil
}

// apiKeyQuotaResetTime 使用本地自然日边界, 避免夏令时切换时减去 24 小时偏离重置时刻。
func apiKeyQuotaResetTime(now time.Time, resetHour int) time.Time {
	if resetHour < 0 || resetHour > 23 {
		resetHour = 0
	}
	reset := time.Date(now.Year(), now.Month(), now.Day(), resetHour, 0, 0, 0, now.Location())
	if now.Before(reset) {
		reset = reset.AddDate(0, 0, -1)
	}
	return reset
}

type apiKeyQuotaUsage struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
}

// 与用量写入复用模型映射及 time.Time, 不手写旧表名或 Unix 秒时间参数。
// 这是已落库的用量检查, 不替代在途请求的原子预算预留。
func loadAPIKeyQuotaUsage(ctx context.Context, apiKeyID int, reset time.Time) (apiKeyQuotaUsage, error) {
	var usage apiKeyQuotaUsage
	err := db.GetDB().WithContext(ctx).Model(&model.RateLimitUsageHourly{}).
		Select("COALESCE(SUM(requests), 0) AS requests, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens").
		Where("scope_type = ? AND scope_id = ? AND hour >= ?", model.RateScopeAPIKey, apiKeyID, reset).
		Scan(&usage).Error
	return usage, err
}

// raiseQuotaAlert 在 API Key 配额超限时触发告警。
func raiseQuotaAlert(ctx context.Context, apiKeyID int, keyName, kind string, current, limit int64) {
	subject := quotaSubject(keyName, apiKeyID)
	// reason 是去重键, 因此带固定的身份(API Key 与配额类型)但不带每小时都在涨的用量数字:
	// 数字进明细行, 每次外发都带最新值; 掺进 reason 会让同一把钥匙每轮都刷一条新告警。
	RaiseAlertFacts(ctx, AlertFacts{
		Subject: subject,
		Kind:    "quota_exceeded",
		Reason:  fmt.Sprintf("api key %s (id=%d) quota exceeded: %s", subject, apiKeyID, kind),
		Fields: []alert.Field{
			{Label: "API Key", Value: fmt.Sprintf("%s (#%d)", subject, apiKeyID)},
			{Label: "配额类型", Value: quotaKindLabel(kind)},
			{Label: "当前用量", Value: fmt.Sprintf("%d / %d", current, limit)},
			{Label: "影响", Value: "该 API Key 在下一个重置周期前会被拒绝"},
			{Label: "建议", Value: "确认是本人放量还是外泄滥用; 需要继续服务请上调配额或重置周期"},
		},
	})
}

// quotaSubject 告警主体: 配额挂在 API Key 上, 用钥匙名比笼统的"系统"更有指向性。
func quotaSubject(keyName string, apiKeyID int) string {
	if strings.TrimSpace(keyName) != "" {
		return strings.TrimSpace(keyName)
	}
	return fmt.Sprintf("API Key #%d", apiKeyID)
}

// quotaKindLabel 配额类型的中文说明。
func quotaKindLabel(kind string) string {
	switch kind {
	case "requests":
		return "请求数"
	case "input_tokens":
		return "输入 Token 数"
	case "output_tokens":
		return "输出 Token 数"
	}
	return kind
}

// tenantQuotaResetTime 计算租户配额重置时间。
func tenantQuotaResetTime(now time.Time, resetDay int, period string) time.Time {
	if resetDay <= 0 || resetDay > 28 {
		resetDay = 1
	}

	switch period {
	case "daily":
		reset := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if now.Before(reset) {
			reset = reset.AddDate(0, 0, -1)
		}
		return reset
	case "weekly":
		// 周重置: resetDay 为 0-6 (周日=0)
		weekday := int(now.Weekday())
		daysDiff := (weekday - resetDay + 7) % 7
		reset := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if daysDiff == 0 && now.Hour() == 0 {
			// 今天是重置日且已经过零点
			return reset
		}
		reset = reset.AddDate(0, 0, -daysDiff)
		return reset
	default: // monthly
		reset := time.Date(now.Year(), now.Month(), resetDay, 0, 0, 0, 0, now.Location())
		if now.Before(reset) {
			reset = reset.AddDate(0, -1, 0)
		}
		return reset
	}
}

type tenantQuotaUsage struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
}

// loadTenantQuotaUsage 从 RateLimitUsageHourly 聚合租户用量。
//
// 口径必须同时覆盖两类行, 且只覆盖本租户:
//  1. scope_type='tenant' 且 scope_id=租户 ID 的行 —— 新数据, 与用户 ID 空间无关;
//  2. scope_type='user' 且 scope_id 属于本租户用户的行 —— 升级前的存量数据。
//
// 绝不能用租户 ID 去查 scope_type='user' 的 scope_id: 那等于把租户 ID 当用户 ID,
// 除非两个 ID 空间恰好撞上, 否则恒为 0(配额形同虚设), 撞上时还会把别的租户的用户算进来。
//
// 双计口径(按小时去重): 升级后同一个请求会同时写 user 行与 tenant 行, 两类行若都汇总,
// 同一笔用量会被算两次 —— 配额会提前一半触发, 实际放行量只有配额的一半。
// 因此用户行只在该小时「还没有 tenant 行」时计入: 那个小时的全部用量都来自升级前,
// tenant 行不可能覆盖它。一旦某小时有了 tenant 行, 该小时的用量一律以 tenant 行为准。
// 这样既保住升级前的存量, 又不会长期系统性双计; 代价只有升级当小时里
// 「升级前那部分」会被 tenant 行口径覆盖(至多一小时, 且每租户只发生一次)。
func loadTenantQuotaUsage(ctx context.Context, tenantID uint, reset time.Time) (tenantQuotaUsage, error) {
	var usage tenantQuotaUsage
	where := `hour >= ? AND (
		(scope_type = ? AND scope_id = ?)
		OR (scope_type = ? AND scope_id IN (SELECT id FROM users WHERE tenant_id = ?)
			AND hour NOT IN (SELECT hour FROM rate_limit_usage_hourly WHERE scope_type = ? AND scope_id = ?))
	)`
	args := []any{
		reset,
		model.RateScopeTenant, tenantID,
		model.RateScopeUser, tenantID,
		model.RateScopeTenant, tenantID,
	}
	err := db.GetDB().WithContext(ctx).Model(&model.RateLimitUsageHourly{}).
		Select("COALESCE(SUM(requests), 0) AS requests, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens").
		Where(where, args...).
		Scan(&usage).Error
	return usage, err
}

// GetTenantQuotaUsage 获取租户当前周期的配额用量, 供界面展示。
func GetTenantQuotaUsage(ctx context.Context, tenantID uint) (requests, inputTokens, outputTokens int64, err error) {
	quota, err := TenantQuotaGet(int(tenantID))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to get tenant quota: %w", err)
	}
	usage, err := loadTenantQuotaUsage(ctx, tenantID, tenantQuotaResetTime(time.Now(), quota.ResetDay, quota.Period))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query tenant quota usage: %w", err)
	}
	return usage.Requests, usage.InputTokens, usage.OutputTokens, nil
}
