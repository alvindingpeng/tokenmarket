package op

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// ledgerFxRate 返回把 currency 折算成账面币种(USD)的汇率。
// 走 FxRateConvert 而不是直接读表: 折算口径(账本币种恒等、牌价快照、缺价报错)
// 只能有一处定义, 否则展示、预扣、结算三条路径会各自算出不同的数。
// 返回 ok=false 表示牌价不可用(无快照、币种未采集、或汇率非正) —— 调用方必须保守处理。
func ledgerFxRate(ctx context.Context, currency string) (rate float64, ok bool) {
	currency = model.NormalizeCurrency(currency)
	if currency == model.LedgerCurrency {
		return 1, true
	}
	// amount=1 只为取汇率: FxRateConvert 返回的 rate 与 amount 无关, 折算金额由调用方自行乘。
	rate, _, _, err := FxRateConvert(ctx, currency, 1)
	if err != nil || rate <= 0 {
		return 0, false
	}
	return rate, true
}

// scaleLLMPrice 按汇率折算文本模型的四类单价(每百万 token)。
// 线性折算与"折算后再按用量计费"数学等价, 因此在单价上折一次即可, 后续 Cost/Scale 都不必再关心币种。
func scaleLLMPrice(price model.LLMPrice, rate float64) model.LLMPrice {
	if rate == 1 {
		return price
	}
	return model.LLMPrice{
		Input:      price.Input * rate,
		Output:     price.Output * rate,
		CacheRead:  price.CacheRead * rate,
		CacheWrite: price.CacheWrite * rate,
	}
}

// scaleMediaPrice 按汇率折算媒体单价(每张/每条/每秒/分档), 与文本价共用同一份汇率。
func scaleMediaPrice(price model.MediaPrice, rate float64) model.MediaPrice {
	if rate == 1 {
		return price
	}
	out := model.MediaPrice{PerImage: price.PerImage * rate, PerVideo: price.PerVideo * rate, PerSecond: price.PerSecond * rate}
	if len(price.Resolution) > 0 {
		out.Resolution = make(map[string]float64, len(price.Resolution))
		for key, value := range price.Resolution {
			out.Resolution[key] = value * rate
		}
	}
	return out
}

// ledgerModelPrices 把一份渠道模型的原币报价折算成账面币种, 供选路、展示与余额预扣共用。
//
// 为什么必须在 op 侧折算: 分组快照是选路(价格优先/评分)与 reserveBalance 的唯一价格来源,
// 若原币价直接进 UserPrice, 一个 CNY 报价的成员会被当成比 USD 报价便宜 7 倍,
// 于是既拿到全部流量、又按 1/7 的价格预扣与结算, 资金口径与流量口径同时错。
//
// 牌价不可用时返回 ok=false 并把价格清零, 由调用方把成员标记为不可用(保守排除):
//   - 不能回退原币价: 那就是上面说的错价路径;
//   - 不能回退 rate=1 当 USD 用: 同上;
//   - 清零是安全的: 该成员此时不可选路, 预扣的 max() 也取不到它,
//     不会出现"按 0 预扣但实际按原币结算"的漏洞。
func ledgerModelPrices(ctx context.Context, supply model.LLMPrice, media model.MediaPrice, currency string) (model.LLMPrice, model.MediaPrice, string, bool) {
	currency = model.NormalizeCurrency(currency)
	// 全零报价在任何币种下都是零, 不需要牌价: 免费模型不应因缺牌价被整体排除,
	// 否则一个纯免费渠道会在牌价断源期间消失, 而它的价格本就无需折算。
	if isZeroLLMPrice(supply) && isZeroMediaPrice(media) {
		return supply, media, currency, true
	}
	rate, ok := ledgerFxRate(ctx, currency)
	if !ok {
		return model.LLMPrice{}, model.MediaPrice{}, currency, false
	}
	return scaleLLMPrice(supply, rate), scaleMediaPrice(media, rate), currency, true
}

// isZeroLLMPrice 判断文本报价是否全为 0(无需折算)。
func isZeroLLMPrice(price model.LLMPrice) bool {
	return price.Input == 0 && price.Output == 0 && price.CacheRead == 0 && price.CacheWrite == 0
}

// isZeroMediaPrice 判断媒体报价是否全为 0(无需折算); 分档价同样要求为 0。
func isZeroMediaPrice(price model.MediaPrice) bool {
	if price.PerImage != 0 || price.PerVideo != 0 || price.PerSecond != 0 {
		return false
	}
	for _, value := range price.Resolution {
		if value != 0 {
			return false
		}
	}
	return true
}
