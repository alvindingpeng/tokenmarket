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

	// 下面两个是**派生字段**, 不是数据库列(gorm:"-": AutoMigrate 不建列, Create/Updates 不写库)。
	// 存在理由只有一个: 前端按"有没有 payout_currency"决定走「按到账币种展示」还是回落
	// legacy ¥ 口径(web/src/api/withdrawal.ts 的 withdrawalPayoutOf)。后端此前从不发这两个键,
	// 于是**每一张提现单**都走了 ¥ 分支 —— EUR 到账 53.10 显示成 ¥53.10(符号全错), JPY 数值
	// 对但被当人民币读。这是"标错"而不是"少标注"。
	//
	// 取值口径(见 DerivePayout): 都从**已有列**派生, 不新增列、不改任何资金语义。
	//   PayoutCurrency = Currency(申请时钉住的到账币种); Currency 为空串(历史旧行)时**保持空串**,
	//     绝不用 "CNY" 填充 —— 前端靠空串继续走 legacy 分支并附"旧单"提示; 填成 CNY 会把
	//     "库里有记录"和"库里没记录"混为一谈, 等于为旧行伪造历史到账币种。
	//   PayoutAmount   = float64(ActualCents)/100 (到账币种最小单位→金额), 与前端自己的兜底
	//     "payout_amount ?? actual_cents/100" 是同一算法, 所以两者永远不会自相矛盾。
	PayoutCurrency string  `json:"payout_currency" gorm:"-"`
	PayoutAmount   float64 `json:"payout_amount" gorm:"-"`
}

// DerivePayout 由已有列派生 payout_currency / payout_amount(只读计算, 不落库)。
// 调用点只有两处: WithdrawApply(申请接口直接回吐单据) 与 WithdrawList(用户/管理端列表
// 共用同一个口)。导出 CSV 不调用 —— 它走自己的列口径, 已在 cashflow_export.go 里写明。
// 历史旧行(Currency 空串): 两个派生字段都留零值 —— 前端据此回落 ¥ + actual_cents。
func (w *Withdrawal) DerivePayout() {
	if w == nil {
		return
	}
	if w.Currency == "" {
		w.PayoutCurrency = ""
		w.PayoutAmount = 0
		return
	}
	w.PayoutCurrency = w.Currency
	w.PayoutAmount = float64(w.ActualCents) / 100
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
