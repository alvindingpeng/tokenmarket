package model

import "time"

// 限流范围类型; 越具体越优先。
const (
	RateScopeSystem       string = "system"        // 系统默认兜底。
	RateScopeUser         string = "user"          // 按用户。
	RateScopeAPIKey       string = "api_key"       // 按 API Key。
	RateScopeGroup        string = "group"         // 按分组。
	RateScopeChannel      string = "channel"       // 按渠道。
	RateScopeChannelKey   string = "channel_key"   // 按渠道凭据(上游侧)。
	RateScopeChannelModel string = "channel_model" // 按渠道模型(上游侧)。
)

// IsValidRateScope 判断限流范围标识是否受支持。
func IsValidRateScope(scope string) bool {
	switch scope {
	case RateScopeSystem, RateScopeUser, RateScopeAPIKey, RateScopeGroup, RateScopeChannel, RateScopeChannelKey, RateScopeChannelModel:
		return true
	}
	return false
}

// RateLimitPolicy 限流策略: RPM(请求/分钟)与 TPM(token/分钟), 0 表示不限。
type RateLimitPolicy struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	ScopeType  string    `json:"scope_type" gorm:"index:idx_rate_policy_scope,priority:1;size:32"`
	ScopeID    int       `json:"scope_id" gorm:"index:idx_rate_policy_scope,priority:2"`
	ModelName  string    `json:"model_name" gorm:"index:idx_rate_policy_scope,priority:3;size:128"` // 空表示全部模型。
	RPM        int64     `json:"rpm"`
	TPM        int64     `json:"tpm"`
	Concurrent int64     `json:"concurrent"` // 0 表示不限制并发数。
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (RateLimitPolicy) TableName() string { return "rate_limit_policies" }

// RateLimitUsageHourly 限流用量聚合(按小时): 供监控页面展示触顶情况。
type RateLimitUsageHourly struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	ScopeType    string    `json:"scope_type" gorm:"index:idx_rate_usage,priority:1;size:32"`
	ScopeID      int       `json:"scope_id" gorm:"index:idx_rate_usage,priority:2"`
	Hour         time.Time `json:"hour" gorm:"index:idx_rate_usage,priority:3"`
	Requests     int64     `json:"requests"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Rejected     int64     `json:"rejected"`
}

// TableName 指定表名。
func (RateLimitUsageHourly) TableName() string { return "rate_limit_usage_hourly" }
