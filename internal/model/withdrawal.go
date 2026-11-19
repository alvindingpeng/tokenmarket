package model

import "time"

// WithdrawStatus 提现单状态。
// 状态机: pending --审核--> approved --打款--> paid
//
//	|                |              |
//	├─用户取消──> cancelled         └─打款回滚──> approved
//	└─管理员拒绝──> rejected(解冻)
type WithdrawStatus string

const (
	WithdrawStatusPending   WithdrawStatus = "pending"
	WithdrawStatusApproved  WithdrawStatus = "approved"
	WithdrawStatusPaid      WithdrawStatus = "paid"
	WithdrawStatusRejected  WithdrawStatus = "rejected"
	WithdrawStatusCancelled WithdrawStatus = "cancelled"
)

// 提现方式: v1 人工打款的三条通道, 后续自动打款按同一字段对接。
const (
	WithdrawMethodAlipay = "alipay"
	WithdrawMethodWechat = "wechat"
	WithdrawMethodBank   = "bank"
)

// Withdrawal 提现单。
// amount/fee/actual 与 users 收益池三字段同介质(浮点池口径), 池迁移精确无换算误差;
// rate/actual_cents 沿用 CNY 语义(rate=1 USD 折 CNY, actual_cents=CNY 分), 旧行为完全不变;
// currency/rate_day/rate_source 是申请时钉住的汇率快照(哪天、哪份牌价、折成哪种币),
// actual_currency 是打款实际执行的币种(默认回落 currency, 管理员换币打款时改写)。
// 旧行三列为零值/空串: 读取侧回落 CNY 静态口径, 绝不为存量行伪造历史汇率。
type Withdrawal struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	UserID         uint           `json:"user_id" gorm:"index;not null"`
	Amount         float64        `json:"amount" gorm:"type:decimal(18,6);not null"`
	Fee            float64        `json:"fee" gorm:"type:decimal(18,6);not null"`
	Actual         float64        `json:"actual" gorm:"type:decimal(18,6);not null"`
	Rate           float64        `json:"rate" gorm:"type:decimal(18,6);not null"`
	ActualCents    int64          `json:"actual_cents" gorm:"not null"`
	Currency       string         `json:"currency" gorm:"size:8;not null;default:CNY"`       // 快照口径: rate 的目标币种(1 USD 折多少); 旧行为 CNY
	RateDay        string         `json:"rate_day" gorm:"size:8;not null;default:''"`        // 牌价采集日 YYYYMMDD; CNY 静态价或旧行为空
	RateSource     string         `json:"rate_source" gorm:"size:32;not null;default:''"`    // static=设置价, identity=恒等, 其余=牌价源名; 旧行空
	ActualCurrency string         `json:"actual_currency" gorm:"size:8;not null;default:''"` // 实际打款币种; 空=未改判, 按 Currency 打款
	RateID         *uint64        `json:"rate_id" gorm:"index"`                              // fx_rates 行 ID; CNY 静态价或旧行为 NULL
	Method         string         `json:"method" gorm:"size:16;not null"`
	AccountName    string         `json:"account_name" gorm:"size:64;not null"`
	AccountNo      string         `json:"account_no" gorm:"size:255;not null"`
	Status         WithdrawStatus `json:"status" gorm:"size:12;not null;default:'pending';index"`
	ReviewNote     string         `json:"review_note" gorm:"size:255;not null;default:''"`
	ReviewedBy     uint           `json:"reviewed_by" gorm:"not null;default:0"`
	ReviewedAt     *time.Time     `json:"reviewed_at"`
	PaidRef        string         `json:"paid_ref" gorm:"size:128;not null;default:''"`
	PaidAt         *time.Time     `json:"paid_at"`
	AppliedAt      time.Time      `json:"applied_at"`
}

// WithdrawFee 手续费 = max(最低费, 申请额×费率), 且不超过申请额(实付不为负)。
func WithdrawFee(amount, feeRate, feeMin float64) float64 {
	fee := feeMin
	if proportional := amount * feeRate; proportional > fee {
		fee = proportional
	}
	if fee > amount {
		fee = amount
	}
	return fee
}

// MaskAccount 账号脱敏: 只露尾 4 位, 中间打码。
func MaskAccount(accountNo string) string {
	runes := []rune(accountNo)
	if len(runes) <= 4 {
		return string(runes)
	}
	masked := make([]rune, len(runes))
	for index := range runes {
		if index < len(runes)-4 {
			masked[index] = '*'
		} else {
			masked[index] = runes[index]
		}
	}
	return string(masked)
}
