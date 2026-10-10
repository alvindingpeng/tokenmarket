package op

import (
	"context"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
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

	// 计算当前配额周期的起始时间
	now := time.Now()
	resetHour := apikey.QuotaResetHour
	if resetHour < 0 || resetHour > 23 {
		resetHour = 0
	}

	// 今日重置时刻
	resetTime := time.Date(now.Year(), now.Month(), now.Day(), resetHour, 0, 0, 0, now.Location())
	if now.Before(resetTime) {
		// 当前时间早于今日重置时刻, 周期从昨日重置时刻开始
		resetTime = resetTime.Add(-24 * time.Hour)
	}

	// 查询当前周期的用量
	var usage struct {
		Requests     int64
		InputTokens  int64
		OutputTokens int64
	}

	err := db.GetDB().WithContext(ctx).Raw(`
		SELECT 
			COALESCE(SUM(requests), 0) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens
		FROM rate_usages
		WHERE scope = ? AND scope_id = ? AND hour >= ?
	`, model.RateScopeAPIKey, apiKeyID, resetTime.Unix()).Scan(&usage).Error

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

	now := time.Now()
	resetHour := apikey.QuotaResetHour
	if resetHour < 0 || resetHour > 23 {
		resetHour = 0
	}

	resetTime := time.Date(now.Year(), now.Month(), now.Day(), resetHour, 0, 0, 0, now.Location())
	if now.Before(resetTime) {
		resetTime = resetTime.Add(-24 * time.Hour)
	}

	var usage struct {
		Requests     int64
		InputTokens  int64
		OutputTokens int64
	}

	err = db.GetDB().WithContext(ctx).Raw(`
		SELECT 
			COALESCE(SUM(requests), 0) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens
		FROM rate_usages
		WHERE scope = ? AND scope_id = ? AND hour >= ?
	`, model.RateScopeAPIKey, apiKeyID, resetTime.Unix()).Scan(&usage).Error

	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to query quota usage: %w", err)
	}

	return usage.Requests, usage.InputTokens, usage.OutputTokens, nil
}


// raiseQuotaAlert 在 API Key 配额超限时触发告警。
func raiseQuotaAlert(ctx context.Context, apiKeyID int, keyName, kind string, current, limit int64) {
	reason := fmt.Sprintf("api_key %s (id=%d) quota exceeded: %s %d/%d", keyName, apiKeyID, kind, current, limit)
	// 使用统一告警接口，去重逻辑在 RaiseAlert 内部处理
	RaiseAlert(ctx, 0, "system", "quota_exceeded", reason)
	
	// 多渠道告警
	go func() {
		// TODO: 集成 internal/alert 包
		// import "github.com/bestruirui/octopus/internal/alert"
		// alert.Send(ctx, alert.Alert{
		//     Event: alert.EventQuotaExceeded,
		//     Title: "API Key 配额超限",
		//     Message: fmt.Sprintf("API Key: %s\n配额类型: %s\n当前用量: %d\n配额上限: %d",
		//         keyName, kind, current, limit),
		//     Timestamp: time.Now(),
		// })
	}()
}
