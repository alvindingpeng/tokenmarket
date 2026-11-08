package relay

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// FxSnapshot 一次折算的完整依据: 币种、结算时生效的汇率与牌价采集日。
// 随 BillingRecord 落库 —— 账单复核要能回答"这笔账按什么币种、什么汇率折进 USD",
// 只记一个 rate 数字不够: 汇率会变, 必须把"哪一天的牌价"一起钉住。
type FxSnapshot struct {
	Currency string  `json:"currency"`
	Rate     float64 `json:"rate"`
	Day      string  `json:"day"`
}

// USDIdentity 账面币种自身的恒等快照: 不折算, rate=1。
var USDIdentity = FxSnapshot{Currency: model.LedgerCurrency, Rate: 1, Day: ""}

// fxConvertSupplyPrice 把原币供货价折算成账面币种(USD)。
// 折算按单价逐项乘同一个汇率: 四类单价同币种, 线性折算与"折算后再计费"数学等价,
// 但在单价上折一次, 之后所有 Cost()/Scale() 链路都无需再关心币种。
// 牌价缺失时清零并把 Currency 原样带出 —— 与选路/预扣一致，绝不把原币价格当 USD 使用。
// 这样即使请求状态来自旧快照或测试直接进入结算，也不会出现按错误币种少扣费。
func fxConvertSupplyPrice(price model.LLMPrice, currency string) (model.LLMPrice, FxSnapshot) {
	currency = model.NormalizeCurrency(currency)
	if currency == model.LedgerCurrency {
		return price, FxSnapshot{Currency: model.LedgerCurrency, Rate: 1, Day: ""}
	}
	rate, day, err := op.FxRateLookup(context.Background(), currency, model.LedgerCurrency)
	if err != nil {
		return model.LLMPrice{}, FxSnapshot{Currency: currency, Rate: 0, Day: "unavailable"}
	}
	return model.LLMPrice{
		Input:      price.Input * rate,
		Output:     price.Output * rate,
		CacheRead:  price.CacheRead * rate,
		CacheWrite: price.CacheWrite * rate,
	}, FxSnapshot{Currency: currency, Rate: rate, Day: day}
}

// fxConvertMediaPrice 同 fxConvertSupplyPrice, 作用于媒体价格(每张/每秒/分档)。
func fxConvertMediaPrice(price model.MediaPrice, currency string) model.MediaPrice {
	currency = model.NormalizeCurrency(currency)
	if currency == model.LedgerCurrency {
		return price
	}
	rate, _, err := op.FxRateLookup(context.Background(), currency, model.LedgerCurrency)
	if err != nil {
		return model.MediaPrice{}
	}
	converted := model.MediaPrice{PerImage: price.PerImage * rate, PerVideo: price.PerVideo * rate, PerSecond: price.PerSecond * rate}
	if len(price.Resolution) > 0 {
		converted.Resolution = make(map[string]float64, len(price.Resolution))
		for k, v := range price.Resolution {
			converted.Resolution[k] = v * rate
		}
	}
	return converted
}

// fxConvertMediaPriceWith 按既有快照折算媒体价(setMediaTarget 与 token 折算共用同一份牌价)。
func fxConvertMediaPriceWith(price model.MediaPrice, fx FxSnapshot) model.MediaPrice {
	if fx.Currency == model.LedgerCurrency || (fx.Rate == 1 && fx.Day == "") {
		return price
	}
	if fx.Day == "unavailable" || fx.Rate <= 0 {
		return model.MediaPrice{}
	}
	rate := fx.Rate
	converted := model.MediaPrice{PerImage: price.PerImage * rate, PerVideo: price.PerVideo * rate, PerSecond: price.PerSecond * rate}
	if len(price.Resolution) > 0 {
		converted.Resolution = make(map[string]float64, len(price.Resolution))
		for k, v := range price.Resolution {
			converted.Resolution[k] = v * rate
		}
	}
	return converted
}
