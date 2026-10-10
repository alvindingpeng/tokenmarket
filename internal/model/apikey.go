package model

type APIKey struct {
	ID              int      `json:"id" gorm:"primaryKey"`
	UserID          uint     `json:"user_id" gorm:"index;not null;default:0"` // 归属用户; 计费与数据隔离的归属依据。
	Name            string   `json:"name" gorm:"not null"`
	APIKey          string   `json:"api_key" gorm:"not null"`
	Enabled         bool     `json:"enabled" gorm:"default:true"`
	ExpireAt        int64    `json:"expire_at,omitempty"`
	MaxCost         float64  `json:"max_cost,omitempty"`
	SupportedModels []string `json:"supported_models" gorm:"serializer:json"` // 允许访问的分组名称, 空表示不限制; 以 JSON 数组存储, 读写两侧都无需再拆分隔符。
	// 配额限制: 0 表示无限制, 按每日重置。
	QuotaRequests     int64 `json:"quota_requests" gorm:"default:0"`      // 每日请求数配额。
	QuotaInputTokens  int64 `json:"quota_input_tokens" gorm:"default:0"`  // 每日输入 token 配额。
	QuotaOutputTokens int64 `json:"quota_output_tokens" gorm:"default:0"` // 每日输出 token 配额。
	QuotaResetHour    int   `json:"quota_reset_hour" gorm:"default:0"`    // 配额重置时刻(0-23), 默认 0 点。
}
