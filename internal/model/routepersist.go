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

// EgressMetricRecord 出口的运行指标快照: 与 RouteMetricRecord 同一套 EMA 字段, 主键换成出口 ID。
//
// 出口成绩此前只存进程内, 重启即清零: 之后每个出口都要重新"被选中一次"才有样本,
// 而这期间率先被选中的出口按"零样本"参与排序 —— 一个早已不通的线路可能因为
// 权重高而被优先挑中, 让用户在重启后的头几次请求里吃 502。
//
// 冷却一并落库: 重启不该立刻重试刚刚被判定为不可用的线路。cooldown_until 是绝对毫秒,
// 过期即自然失效, 恢复时无需额外判断。
type EgressMetricRecord struct {
	EgressID      int       `gorm:"primaryKey"`
	EmaWaitMs     float64   `json:"ema_wait_ms"`
	EmaSuccess    float64   `json:"ema_success"`
	Samples       int       `json:"samples"`
	CooldownUntil int64     `json:"cooldown_until"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (EgressMetricRecord) TableName() string { return "egress_metrics" }
