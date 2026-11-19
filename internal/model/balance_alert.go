package model

import "time"

// BalanceAlert 用户的余额预警配置。
//
// 每个用户至多一条配置(user_id 唯一)。阈值用绝对金额而不是"初始余额的百分比":
// 用户理解"低于 X 元提醒"远比"低于初始充值的 20%"容易, 也避免初始余额被后续
// 充值不断稀释导致百分比口径漂移。ThresholdCNY 为 0 表示禁用预警。
type BalanceAlert struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// UserID 唯一索引: 一人一份配置, 保存语义是整体覆盖(upsert)。
	UserID uint `json:"user_id" gorm:"uniqueIndex;not null"`
	// ThresholdCNY 可用余额阈值(元): 可用余额(Balance - Frozen)低于该值时提醒; 0 表示禁用。
	ThresholdCNY float64 `json:"threshold_cny" gorm:"type:decimal(18,6);not null;default:0"`
	// Enabled 预警开关: 关闭后保留阈值配置, 重新打开即恢复。
	Enabled bool `json:"enabled" gorm:"not null;default:true"`
	// LastTriggeredAt 上次触发时间: 触发去重窗口的起点, 防止每次结算都重复提醒。
	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// BalanceAlertUpsert 设置余额预警的请求。
type BalanceAlertUpsert struct {
	// ThresholdCNY 阈值(元); 0 表示禁用。
	ThresholdCNY float64 `json:"threshold_cny" binding:"gte=0,lte=999999"`
	// Enabled 开关; 缺省视为 true。
	Enabled *bool `json:"enabled"`
}

// BalanceAlertView 预警配置的响应视图。
type BalanceAlertView struct {
	ThresholdCNY    float64    `json:"threshold_cny"`
	Enabled         bool       `json:"enabled"`
	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
}