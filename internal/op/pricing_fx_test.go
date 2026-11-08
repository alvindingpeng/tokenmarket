package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// fxPricingFixture 造一套「已发布 + 已过审 + 已上架」的渠道/凭据/模型/授权, 并按声明的报价币种定价。
func fxPricingFixture(t *testing.T, name, currency string, supply model.LLMPrice, media model.MediaPrice) (model.Channel, model.ChannelGrant) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, Enabled: true}, Shared: true, ReviewStatus: model.ChannelReviewApproved}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: name + "-key", Key: "secret", Enabled: true}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	cm := model.ChannelModel{ChannelID: channel.ID, Name: name + "-model", Listed: true, SupplyPrice: supply, PriceCurrency: currency, MediaSupply: media}
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: cm.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return channel, grant
}

func seedFxRate(t *testing.T, from, to string, rate float64) {
	t.Helper()
	if err := db.GetDB().Create(&model.FxRate{From: from, To: to, Day: time.Now().Format("20060102"), Rate: rate, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx %s->%s: %v", from, to, err)
	}
	FxRateCacheReset()
}

// TestLedgerModelPricesConvertToLedger 覆盖折算本身: 文本价与媒体价共用同一份汇率,
// 且牌价缺失时不回退成"原币当 USD"。
func TestLedgerModelPricesConvertToLedger(t *testing.T) {
	breakerTestSetup(t)
	t.Cleanup(FxRateCacheReset)
	seedFxRate(t, "USD", "CNY", 7.2)

	supply := model.LLMPrice{Input: 72, Output: 144, CacheRead: 36, CacheWrite: 18}
	media := model.MediaPrice{PerImage: 7.2, PerVideo: 72, PerSecond: 1.44, Resolution: map[string]float64{"1024x1024": 14.4}}
	gotSupply, gotMedia, currency, ok := ledgerModelPrices(context.Background(), supply, media, " cny ")
	if !ok || currency != "CNY" {
		t.Fatalf("convert failed: ok=%v currency=%q", ok, currency)
	}
	approx := func(got, want float64, label string) {
		t.Helper()
		if got < want-1e-9 || got > want+1e-9 {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
	approx(gotSupply.Input, 10, "input")
	approx(gotSupply.Output, 20, "output")
	approx(gotSupply.CacheRead, 5, "cache_read")
	approx(gotSupply.CacheWrite, 2.5, "cache_write")
	approx(gotMedia.PerImage, 1, "per_image")
	approx(gotMedia.PerVideo, 10, "per_video")
	approx(gotMedia.PerSecond, 0.2, "per_second")
	approx(gotMedia.Resolution["1024x1024"], 2, "resolution")

	// USD 声明零折算: rate=1, 不查表也不需要牌价。
	if got, _, currency, ok := ledgerModelPrices(context.Background(), model.LLMPrice{Input: 3}, model.MediaPrice{}, ""); !ok || currency != model.LedgerCurrency || got.Input != 3 {
		t.Fatalf("usd identity: got=%+v currency=%q ok=%v", got, currency, ok)
	}

	// 全零报价无需牌价: 免费模型不应因缺价被排除。
	if _, _, _, ok := ledgerModelPrices(context.Background(), model.LLMPrice{}, model.MediaPrice{}, "EUR"); !ok {
		t.Fatal("zero price must not require an fx rate")
	}

	// 非零报价 + 牌价缺失: 必须 ok=false, 且价格清零而不是原样回退。
	gotSupply, gotMedia, _, ok = ledgerModelPrices(context.Background(), model.LLMPrice{Input: 5}, model.MediaPrice{PerImage: 1}, "EUR")
	if ok {
		t.Fatal("missing fx rate must not be treated as convertible")
	}
	if gotSupply.Input != 0 || gotMedia.PerImage != 0 {
		t.Fatalf("missing fx must zero the price, got supply=%+v media=%+v", gotSupply, gotMedia)
	}
}

// TestGroupSnapshotAndCandidatesUseUsdPrice 是本次改动的核心验收: 分组成员价格(选路 + 预扣的唯一来源)
// 与分组页候选价格都必须已是 USD, 且牌价缺失时成员/候选按不可用保守排除。
func TestGroupSnapshotAndCandidatesUseUsdPrice(t *testing.T) {
	breakerTestSetup(t)
	t.Cleanup(func() {
		FxRateCacheReset()
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	seedFxRate(t, "USD", "CNY", 7.2)

	// CNY 报价: 72 CNY / 百万 token = 10 USD / 百万 token; 再按默认 20% 上浮得到用户价 12。
	channel, grant := fxPricingFixture(t, "cny", "CNY",
		model.LLMPrice{Input: 72, Output: 144},
		model.MediaPrice{PerImage: 7.2})
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshot := groupSnapshot(model.Group{Items: []model.GroupItem{{ChannelGrantID: grant.ID}}}, model.Scope{})
	if len(snapshot.Items) != 1 {
		t.Fatalf("snapshot items = %d", len(snapshot.Items))
	}
	item := snapshot.Items[0]
	if item.FxMissing || item.PriceCurrency != "CNY" {
		t.Fatalf("fx flags: missing=%v currency=%q", item.FxMissing, item.PriceCurrency)
	}
	markup := ChannelMarkupFor(channel.ID)
	wantUserInput := 10 * (1 + markup)
	if !item.Available {
		t.Fatalf("converted member must stay available: %+v", item)
	}
	if item.SupplyPrice.Input < 10-1e-9 || item.SupplyPrice.Input > 10+1e-9 {
		t.Fatalf("supply input = %v, want 10 USD", item.SupplyPrice.Input)
	}
	if item.UserPrice.Input < wantUserInput-1e-9 || item.UserPrice.Input > wantUserInput+1e-9 {
		t.Fatalf("user input = %v, want %v", item.UserPrice.Input, wantUserInput)
	}
	if item.MediaSupply.PerImage < 1-1e-9 || item.MediaSupply.PerImage > 1+1e-9 {
		t.Fatalf("media supply per image = %v, want 1 USD", item.MediaSupply.PerImage)
	}

	candidates := ChannelGrantCandidates(model.Scope{})
	var found *model.ChannelGrantCandidate
	for i := range candidates {
		if candidates[i].ID == grant.ID {
			found = &candidates[i]
		}
	}
	if found == nil {
		t.Fatal("candidate missing")
	}
	if found.FxMissing || found.PriceCurrency != "CNY" || found.SupplyPrice.Input < 10-1e-9 || found.SupplyPrice.Input > 10+1e-9 {
		t.Fatalf("candidate not converted: %+v", found)
	}
	if found.UserPrice.Input < wantUserInput-1e-9 || found.UserPrice.Input > wantUserInput+1e-9 {
		t.Fatalf("candidate user price = %v, want %v", found.UserPrice.Input, wantUserInput)
	}

	// 牌价不可用: EUR 报价但库里没有 EUR 牌价 —— 成员与候选都必须不可用, 价格清零, 不回退原币。
	eurChannel, eurGrant := fxPricingFixture(t, "eur", "EUR", model.LLMPrice{Input: 100}, model.MediaPrice{PerImage: 5})
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	blocked := groupSnapshot(model.Group{Items: []model.GroupItem{{ChannelGrantID: eurGrant.ID}}}, model.Scope{})
	if len(blocked.Items) != 1 {
		t.Fatalf("blocked snapshot items = %d", len(blocked.Items))
	}
	got := blocked.Items[0]
	if !got.FxMissing || got.Available || got.SupplyPrice.Input != 0 || got.UserPrice.Input != 0 || got.MediaSupply.PerImage != 0 {
		t.Fatalf("missing fx must exclude member: %+v", got)
	}
	var blockedCandidate *model.ChannelGrantCandidate
	allCandidates := ChannelGrantCandidates(model.Scope{})
	for i := range allCandidates {
		if allCandidates[i].ID == eurGrant.ID {
			blockedCandidate = &allCandidates[i]
			break
		}
	}
	if blockedCandidate == nil {
		t.Fatal("candidate with missing fx must still be listed for diagnosis")
	}
	if !blockedCandidate.FxMissing || blockedCandidate.Available || blockedCandidate.SupplyPrice.Input != 0 {
		t.Fatalf("missing fx candidate not excluded: %+v", blockedCandidate)
	}
	if eurChannel.ID == channel.ID {
		t.Fatal("fixture channels must differ")
	}
}
