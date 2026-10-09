package model

import "time"

// RouteStateRecord 分组路由状态快照: 当前成员、探测占用与亲和截止, 重启后恢复。
type RouteStateRecord struct {
	GroupID       int       `gorm:"primaryKey"`
	CurrentItemID int       `json:"current_item_id"`
	ProbeItemID   int       `json:"probe_item_id"`
	AffinityUntil int64     `json:"affinity_until"`
	AffinityArmed bool      `json:"affinity_armed"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (RouteStateRecord) TableName() string { return "route_states" }

// RouteCooldownRecord 分组成员冷却快照: 持久化后重启不再重放故障成员。
type RouteCooldownRecord struct {
	GroupID       int   `gorm:"primaryKey"`
	ItemID        int   `gorm:"primaryKey"`
	CooldownUntil int64 `json:"cooldown_until"`
}

// TableName 指定表名。
func (RouteCooldownRecord) TableName() string { return "route_cooldowns" }

// RouteMetricRecord 分组成员运行指标快照: 延迟/成功率 EMA 与样本数。
type RouteMetricRecord struct {
	GroupID    int       `gorm:"primaryKey"`
	ItemID     int       `gorm:"primaryKey"`
	EmaWaitMs  float64   `json:"ema_wait_ms"`
	EmaSuccess float64   `json:"ema_success"`
	Samples    int       `json:"samples"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (RouteMetricRecord) TableName() string { return "route_metrics" }
