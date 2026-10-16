package model

import "time"

// SettlementStatement 渠道商按日/区间的结算单
type SettlementStatement struct {
	ID              uint64    `json:"id" gorm:"primaryKey"`
	StatementNo     string    `json:"statement_no" gorm:"size:32;uniqueIndex;not null"` // 结算单号 STM-YYYYMMDD-ownerID
	OwnerID         uint      `json:"owner_id" gorm:"index;not null"`                   // 渠道商用户 ID
	Period          string    `json:"period" gorm:"size:16;index;not null"`             // 账期 YYYYMMDD 或 YYYY-MM
	RequestCount    int64     `json:"request_count"`                                    // 计费请求次数
	InputToken      int64     `json:"input_token"`
	OutputToken     int64     `json:"output_token"`
	GrossRevenue    float64   `json:"gross_revenue" gorm:"type:decimal(18,6);not null"` // 供货应收毛额
	RefundDeduction float64   `json:"refund_deduction" gorm:"type:decimal(18,6);not null;default:0"` // 退款扣减
	NetRevenue      float64   `json:"net_revenue" gorm:"type:decimal(18,6);not null"`   // 最终净收益
	Status          string    `json:"status" gorm:"size:16;not null;default:'settled';index"` // settled | audited
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (SettlementStatement) TableName() string { return "settlement_statements" }

// ProfitMarginSummary 经营毛利分析结果
type ProfitMarginSummary struct {
	Period          string  `json:"period"`
	UserCost        float64 `json:"user_cost"`        // 用户消费额 (GMV/收入)
	OwnerRevenue    float64 `json:"owner_revenue"`    // 供货成本 (渠道商收入)
	PlatformRevenue float64 `json:"platform_revenue"` // 平台毛利
	MarginRate      float64 `json:"margin_rate"`      // 毛利率 (0~100 %)
	RequestCount    int64   `json:"request_count"`
	RefundAmount    float64 `json:"refund_amount"`    // 期间退款总额
	RechargeAmount  float64 `json:"recharge_amount"`  // 期间充值现金流入(元)
}
