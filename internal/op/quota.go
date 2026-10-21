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
