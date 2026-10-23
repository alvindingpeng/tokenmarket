package model

import "time"

// 对账状态。
const (
	ReconcileStatusBalanced  = "balanced"  // 三方账目对平。
	ReconcileStatusMismatch  = "mismatch"  // 发现差异, 待人工核查, 不自动改余额。
	ReconcileStatusReview    = "review"    // 存在待核查项(重复/漏结算/长期预扣)。
	ReconcileStatusExplained = "explained" // 差异已查清并留存说明, 数字本身不动。
)

// BillingReconcile 每日对账结果: 按自然日核对计费明细与余额预扣。
// 只登记结论与差异, 绝不自动修改用户余额, 差异一律进入待核查状态。
type BillingReconcile struct {
	ID           uint64  `json:"id" gorm:"primaryKey"`
	Day          string  `json:"day" gorm:"uniqueIndex:idx_reconcile_day;size:16"` // 自然日 YYYYMMDD。
	Records      int64   `json:"records"`                                          // 计费明细笔数。
	UserCost     float64 `json:"user_cost" gorm:"type:decimal(18,6)"`
	OwnerRevenue float64 `json:"owner_revenue" gorm:"type:decimal(18,6)"`
	PlatformRev  float64 `json:"platform_revenue" gorm:"type:decimal(18,6)"`
	Duplicated   int64   `json:"duplicated"` // 重复结算(同一请求多条明细)。
	Missing      int64   `json:"missing"`    // 有用量但缺少计费明细的请求。
	StaleHold    int64   `json:"stale_hold"` // 超过时限仍未结算且已回滚的预扣。
	Unsettled    int64   `json:"unsettled"`  // 仍未结算的预扣。
	Status       string  `json:"status"`
	Detail       string  `json:"detail" gorm:"type:text"`
	// Note 是人工对差异的处置说明: 查清了「为什么差」但数字本身不可改(证据已过保留窗口,
	// 或本就源于当时的豁免策略)时, 把结论钉在记录上, 下次不必重新推演一遍。
	// 它是唯一允许人工改写的字段, 且只允许写在 explained 状态上 —— 其余字段一律由对账逻辑掌管。
	Note      string     `json:"note" gorm:"type:text"`
	NotedAt   *time.Time `json:"noted_at"`
	NotedBy   uint       `json:"noted_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (BillingReconcile) TableName() string { return "billing_reconciles" }
