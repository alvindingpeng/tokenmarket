package model

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// 账面币种(LedgerCurrency): 全平台计费、余额、账单的唯一记账口径, 定为 USD。
// 为什么是 USD: 模型参考价(models.dev, presets.go 的数据源)本身就是 USD 单价,
// 账面跟它同源 = 参考价零折算; 渠道商用其它币种报价时在渠道模型上声明 price_currency,
// 结算时按汇率快照折算成 USD 入账。余额三字段、UserCost/OwnerRevenue/PlatformRevenue
// 的单位从此恒为 USD, 不存在"账面里混着两种货币"的行。
const LedgerCurrency = "USD"

// ValidPriceCurrency 渠道模型供货价的声明币种白名单。
// 只收主流结算币种, 防自由文本: 币种错了折算就是错价, 白名单比正则可靠。
func ValidPriceCurrency(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "USD", "CNY", "EUR", "GBP", "JPY", "HKD", "SGD":
		return true
	}
	return false
}

// NormalizeCurrency 币种代码归一: 大写、去空白。空串按 USD(存量数据与默认)。
func NormalizeCurrency(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	if c == "" {
		return LedgerCurrency
	}
	return c
}

// FxRate 是一条汇率快照: 1 单位 From 币种折多少 To 币种。
type FxRate struct {
	ID        uint64    `json:"id" gorm:"primaryKey"`
	From      string    `json:"from" gorm:"size:8;not null;uniqueIndex:idx_fx_pair_day,priority:1"`
	To        string    `json:"to" gorm:"size:8;not null;uniqueIndex:idx_fx_pair_day,priority:2"`
	Day       string    `json:"day" gorm:"size:8;not null;uniqueIndex:idx_fx_pair_day,priority:3"` // YYYYMMDD, 采集日。
	Rate      float64   `json:"rate" gorm:"not null"`
	Source    string    `json:"source" gorm:"size:32;default:''"` // 牌价来源, 如 exchangerate-api。
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (FxRate) TableName() string { return "fx_rates" }

// MaxFxRate 单条汇率的量级上限。JPY 对 USD 在 150 量级, 上限 100000 已远超真实牌价;
// 越界几乎必然是填成了"100 USD 折多少 CNY"之类的复合单位, 直接拒绝。
const MaxFxRate = 100_000.0

// ValidateFxRate 汇率合理性: 正数、有限、不超量级。
func ValidateFxRate(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || rate > MaxFxRate {
		return fmt.Errorf("fx rate must be a finite positive number not exceeding %g", MaxFxRate)
	}
	return nil
}
