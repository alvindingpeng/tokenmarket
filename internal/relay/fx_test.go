package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// USD 报价零折算: 绝大多数模型的路径, 不能因折算引入浮点噪声。
func TestFxConvertUSDIdentity(t *testing.T) {
	price := model.LLMPrice{Input: 3, Output: 15, CacheRead: 0.15}
	converted, fx := fxConvertSupplyPrice(price, "")
	if converted != price || fx.Currency != model.LedgerCurrency || fx.Rate != 1 {
		t.Fatalf("identity: %+v fx=%+v", converted, fx)
	}
}

// 牌价缺失时回退原价且 Currency 原样带出(对账按 Currency != USD 识别未折算行),
// 绝不允许按 rate=0 折算把账单清零。
func TestFxConvertMissingRateFallsBack(t *testing.T) {
	price := model.LLMPrice{Input: 3}
	converted, fx := fxConvertSupplyPrice(price, "CNY")
	if converted.Input != 3 || fx.Currency != "CNY" || fx.Day != "unavailable" || fx.Rate != 1 {
		t.Fatalf("fallback: %+v fx=%+v", converted, fx)
	}
}

// 媒体价按快照同率折算, 分档表逐项缩放。
func TestFxConvertMediaPriceWith(t *testing.T) {
	media := model.MediaPrice{PerImage: 10, PerSecond: 1, Resolution: map[string]float64{"1080p": 20}}
	out := fxConvertMediaPriceWith(media, FxSnapshot{Currency: "CNY", Rate: 0.14, Day: "20261106"})
	if out.PerImage < 1.399 || out.PerImage > 1.401 || out.Resolution["1080p"] < 2.79 || out.Resolution["1080p"] > 2.81 {
		t.Fatalf("media convert: %+v", out)
	}
	same := fxConvertMediaPriceWith(media, FxSnapshot{Currency: model.LedgerCurrency, Rate: 1})
	if same.PerImage != 10 || same.Resolution["1080p"] != 20 {
		t.Fatalf("identity: %+v", same)
	}
}
