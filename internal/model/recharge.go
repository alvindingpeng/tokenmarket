package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 充值订单状态: pending 待支付, paid 已支付, failed 支付失败, expired 超时关闭, refunded 已退款。
const (
	RechargeStatusPending  = "pending"
	RechargeStatusPaid     = "paid"
	RechargeStatusFailed   = "failed"
	RechargeStatusExpired  = "expired"
	RechargeStatusRefunded = "refunded"
)

// 收款渠道(P4 多渠道, 对齐 New API 的 payment_provider 口径):
// 易支付/支付宝按面值收 CNY, Stripe 按设置币种换算, Creem 按商品档位定价。
const (
	PayProviderEpay   = "epay"
	PayProviderAlipay = "alipay"
	PayProviderStripe = "stripe"
	PayProviderCreem  = "creem"
)

// RechargeOrder 充值订单: 金额一律"最小货币单位 + 币种"(D1 外币支付预留),
// 入账余额在下单时按汇率定格, 回调只做验签与金额核对, 不再重算。
type RechargeOrder struct {
	ID          uint64     `json:"id" gorm:"primaryKey"`
	OrderNo     string     `json:"order_no" gorm:"size:32;not null;uniqueIndex"` // 业务单号 yyyymmddHHMMSS+随机
	UserID      uint       `json:"user_id" gorm:"index"`
	Provider    string     `json:"provider" gorm:"size:16;not null"`
	Method      string     `json:"method" gorm:"size:16;not null"`                     // alipay|wechat|qq|空=收银台自选
	AmountCents int64      `json:"amount_cents" gorm:"not null"`                       // CNY 分基准(展示与入账口径)
	Currency    string     `json:"currency" gorm:"size:8;not null;default:CNY"`        // 基准币种, 恒为 CNY
	PayCents    int64      `json:"pay_cents" gorm:"not null;default:0"`                // 渠道实付额(渠道币种分); 0=商品定价不核金额
	PayCurrency string     `json:"pay_currency" gorm:"size:8;not null;default:CNY"`    // 渠道收款币种
	Credits     float64    `json:"credits" gorm:"type:decimal(18,6);not null"`         // 入账余额(不含赠送)
	Bonus       float64    `json:"bonus" gorm:"type:decimal(18,6);not null;default:0"` // 满赠送的余额
	Subject     string     `json:"subject" gorm:"size:128;not null"`
	Status      string     `json:"status" gorm:"size:16;not null;index"`
	TradeNo     *string    `json:"trade_no" gorm:"size:64;uniqueIndex"` // 第三方流水号(幂等键); 未支付为空, 唯一索引只对非空生效
	FailReason  string     `json:"fail_reason" gorm:"size:255"`
	NotifyRaw   string     `json:"notify_raw" gorm:"type:text"` // 回调原文(审计/纠纷取证)
	CreatedAt   time.Time  `json:"created_at" gorm:"index"`
	ExpiredAt   time.Time  `json:"expired_at"`
	PaidAt      *time.Time `json:"paid_at"`
}

// TableName 指定表名。
func (RechargeOrder) TableName() string { return "recharge_orders" }

// BonusRule 满赠规则(D6): 单笔充满 MinCNY 元送 BonusPercent% 的余额。
type BonusRule struct {
	MinCNY       float64
	BonusPercent float64
}

// ParseBonusRules 解析紧凑规则串 "100:5,500:10"(满 100 元送 5%, 满 500 元送 10%);
// 空串返回空规则。格式错误给出可读原因, 供设置校验直接透出。
func ParseBonusRules(value string) ([]BonusRule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	rules := []BonusRule{}
	for _, piece := range strings.Split(value, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		parts := strings.Split(piece, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("bonus rules must look like 100:5,500:10")
		}
		minCNY, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil || minCNY <= 0 {
			return nil, fmt.Errorf("bonus rule threshold must be a positive number")
		}
		percent, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil || percent < 0 || percent > 100 {
			return nil, fmt.Errorf("bonus rule percent must be between 0 and 100")
		}
		rules = append(rules, BonusRule{MinCNY: minCNY, BonusPercent: percent})
	}
	return rules, nil
}

// ParseAmountTiers 解析充值弹窗快捷金额 "10,50,100"(元); 空串返回空列表。
func ParseAmountTiers(value string) ([]float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	tiers := []float64{}
	for _, piece := range strings.Split(value, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		amount, err := strconv.ParseFloat(piece, 64)
		if err != nil || amount <= 0 {
			return nil, fmt.Errorf("amount tiers must be positive numbers separated by commas")
		}
		tiers = append(tiers, amount)
	}
	return tiers, nil
}

// MatchBonusRule 返回命中最高档的满赠比例; 无命中返回 0。
func MatchBonusRule(rules []BonusRule, amountCNY float64) float64 {
	percent := 0.0
	for _, rule := range rules {
		if amountCNY >= rule.MinCNY && rule.BonusPercent > percent {
			percent = rule.BonusPercent
		}
	}
	return percent
}
