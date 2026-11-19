package model

import "time"

// 租户流水类型: 与用户侧 balance_ledgers 的记账口径对称, 但只记录租户层面的资金进出。
// 请求级的 reserve/settle 不逐条写租户流水 —— 那会把流水表按请求量放大一个数量级,
// 租户层只保留"钱进出这个池子"的可解释事件, 单笔消耗由 billing_records 追溯。
const (
	TenantLedgerKindRecharge = "recharge" // 平台给租户充值(线下打款/预付)
	TenantLedgerKindAdjust   = "adjust"   // 人工调账(退款冲正/纠错)
	TenantLedgerKindWithdraw = "withdraw" // 租户提现(余额退出平台)
)

// TenantLedger 租户资金流水: 每一次租户可用余额的变化逐条留痕, 供客户对账与平台审计。
// 与用户流水的区别: 这里记的是客户与平台之间的资金关系, 不含逐请求的消耗明细。
type TenantLedger struct {
	ID        uint64    `json:"id" gorm:"primaryKey"`
	TenantID  uint      `json:"tenant_id" gorm:"index"`
	Kind      string    `json:"kind" gorm:"size:32;index"`
	Amount    float64   `json:"amount" gorm:"type:decimal(18,6)"` // 可用余额净变动, 负数表示扣减
	Note      string    `json:"note" gorm:"type:text"`
	ActorID   uint      `json:"actor_id" gorm:"index"` // 操作者用户 ID, 0 表示系统自动
	ActorName string    `json:"actor_name" gorm:"size:64"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

// TableName 指定表名。
func (TenantLedger) TableName() string { return "tenant_ledgers" }
