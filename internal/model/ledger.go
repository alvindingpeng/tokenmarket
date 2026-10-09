package model

import "time"

// 余额流水类型: 预扣/结算/释放由计费链路写入, 调账由管理员改余额时写入。
const (
	LedgerKindReserve = "reserve" // 请求开始, 冻结预估费用。
	LedgerKindSettle  = "settle"  // 请求结束, 解冻预估并扣除实际费用。
	LedgerKindRelease = "release" // 驳回: 无用量, 解冻预估并退还。
	LedgerKindAdjust  = "adjust"  // 管理员直接设置余额(充值/扣减)。
)

// BillingLedger 余额流水: 每一次可用余额或冻结额的变化逐条留痕, 供用户自查与运维对账。
// 口径说明: Amount 是可用余额净变动, Frozen 是冻结额变动, Cost 是本次实际费用;
// 预扣记 Amount=-estimate/Frozen=+estimate, 结算记 Amount=+estimate-cost/Frozen=-estimate。
type BillingLedger struct {
	ID           uint64    `json:"id" gorm:"primaryKey"`
	UserID       uint      `json:"user_id" gorm:"index"`
	Kind         string    `json:"kind" gorm:"size:32;index"`
	Amount       float64   `json:"amount" gorm:"type:decimal(18,6)"`
	Frozen       float64   `json:"frozen" gorm:"type:decimal(18,6)"`
	Cost         float64   `json:"cost" gorm:"type:decimal(18,6)"`
	BalanceAfter float64   `json:"balance_after" gorm:"type:decimal(18,6)"` // 变动后的可用余额; 历史回填行为 0 表示未知。
	RequestID    uint64    `json:"request_id" gorm:"index"`
	ModelName    string    `json:"model_name" gorm:"size:128"`
	ActorID      uint      `json:"actor_id"`
	ActorName    string    `json:"actor_name" gorm:"size:64"`
	Note         string    `json:"note" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at" gorm:"index"`
}

// TableName 指定表名。
func (BillingLedger) TableName() string { return "balance_ledgers" }
