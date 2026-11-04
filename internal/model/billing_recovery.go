package model

import "time"

const (
	BillingSettlementStatusPending   = "pending"
	BillingSettlementStatusRetry     = "retry"
	BillingSettlementStatusManual    = "manual"
	BillingSettlementStatusCompleted = "completed"
)

// BillingSettlementTask keeps the original economic snapshot; never replace Snapshot on conflict.
type BillingSettlementTask struct {
	ID            uint64 `json:"id" gorm:"primaryKey"`
	ReservationID uint64 `json:"reservation_id" gorm:"uniqueIndex"`
	RequestID     uint64 `json:"request_id,string"`
	UserID        uint   `json:"user_id" gorm:"index"`
	// 复合索引 (status, updated_at) 服务恢复列表: 该列表按状态分桶取"每桶最近更新的若干条"
	// (见 op.BillingRecoveryList), 两列合起来才能让过滤与倒序在索引内一起完成。生产库副本实测
	// (9822 行 completed): 只有单列 status 索引时该桶要 25.4 ms 且带 TEMP B-TREE FOR ORDER BY;
	// 有复合索引后是 0.3~0.6 ms 的纯倒序索引扫描。
	// 单列索引保留: 它被复合索引覆盖, 但按状态计数等查询仍会用它, 且体积只有 168 KB ——
	// 删掉它没有量到的收益, 反而每次都要一次 DROP INDEX。曾为此写过迁移 39, 已撤成空占位
	// (internal/db/migrate/039_reserved.go), 那里写了撤回理由与保留版本号的原因。
	Status        string    `json:"status" gorm:"index;index:idx_billing_settlement_tasks_status_updated,priority:1"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at" gorm:"index"`
	LastError     string    `json:"last_error" gorm:"type:text"`
	Snapshot      string    `json:"-" gorm:"type:text"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"index:idx_billing_settlement_tasks_status_updated,priority:2"`
}
