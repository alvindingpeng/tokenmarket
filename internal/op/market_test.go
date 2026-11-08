package op

import (
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// marketSetup 建独立库并清空渠道/分组缓存, 与其它 op 用例一致的隔离方式。
func marketSetup(t *testing.T) {
	t.Helper()
	breakerTestSetup(t)
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
		groupCache.Clear()
		groupNameIndex.Clear()
	})
}

// marketFixture 造一条「已发布 + 已过审 + 已上架」的挂牌并刷新缓存, 返回渠道与授权。
// tune 用于构造反例(未发布/未过审/未上架/停用/熔断/零价/非 USD)。
func marketFixture(t *testing.T, channelName, modelName string, tune func(channel *model.Channel, key *model.ChannelKey, cm *model.ChannelModel)) (model.Channel, model.ChannelGrant) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: channelName, Enabled: true}}
	channel.Shared = true
	channel.ReviewStatus = model.ChannelReviewApproved
	channel.ShareCode = "1001"
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: channelName + "-key", Key: "secret", Enabled: true}}
	cm := model.ChannelModel{Name: modelName, Listed: true, SupplyPrice: model.LLMPrice{Input: 10, Output: 20}}
	if tune != nil {
		tune(&channel, &key, &cm)
	}
	// gorm 在带上 default 标记的列上会"先取默认值再写库, 并把默认值回填进结构体"(channels/keys 的
	// enabled 默认 true)。因此零值 false 一 Create 就被改回 true, 反例必须在写库后用原始意图重写一次;
	// 判定要在 Create 之前取, 库回填之后 channel.Enabled 已是 true, 再看就永远进不了这个分支。
	channelDisabled := !channel.Enabled
	keyDisabled := !key.Enabled
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key.ChannelID = channel.ID
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	// channels/keys 的 enabled 列带 gorm default:true, 零值 false 在 Create 时会被跳过并落成默认 true;
	// 想造"停用"反例必须再显式写一次, 否则用例自己就把反例悄悄改回了可用。
	if channelDisabled {
		if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable channel: %v", err)
		}
	}
	if keyDisabled {
		if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", key.ID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable key: %v", err)
		}
	}
	cm.ChannelID = channel.ID
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: cm.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if err := channelRefreshCache(t.Context()); err != nil {
		t.Fatalf("refresh channel cache: %v", err)
	}
	// 刷新后再读一次: 上面返回的实体不带数据库分配的主键, 用例要用真实主键。
	stored, ok := channelCache.Get(channel.ID)
	if !ok {
		t.Fatal("channel not in cache after refresh")
	}
	return stored, grant
}

// TestMarketCatalogSupplyGate 验证广场的供给侧闸口与对外候选完全一致:
// 未发布 / 未过审 / 未上架 三种情形都不得出现在广场, 且对管理员也不例外。
//
// 这三条正是"已发布在用"的定义, 漏一条就会让广场列出用户其实买不到的东西。
func TestMarketCatalogSupplyGate(t *testing.T) {
	marketSetup(t)
	admin := model.Scope{Role: model.RoleAdmin}
	_, okGrant := marketFixture(t, "good", "market-ok", nil)
	marketFixture(t, "unshared", "market-unshared", func(c *model.Channel, _ *model.ChannelKey, _ *model.ChannelModel) {
		c.Shared = false
	})
	marketFixture(t, "draft", "market-draft", func(c *model.Channel, _ *model.ChannelKey, _ *model.ChannelModel) {
		c.ReviewStatus = model.ChannelReviewDraft
	})
	marketFixture(t, "unlisted", "market-unlisted", func(_ *model.Channel, _ *model.ChannelKey, cm *model.ChannelModel) {
		cm.Listed = false
	})

	catalog := MarketCatalog(admin)
	if len(catalog) != 1 {
		t.Fatalf("catalog models = %d, want 1 (only the approved+shared+listed one)", len(catalog))
	}
	if catalog[0].Name != "market-ok" {
		t.Fatalf("catalog model = %q, want market-ok", catalog[0].Name)
	}
	if len(catalog[0].Channels) != 1 || catalog[0].Channels[0].ChannelGrantID != okGrant.ID {
		t.Fatalf("catalog channels = %+v, want the single ok grant %d", catalog[0].Channels, okGrant.ID)
	}
}

// TestMarketCatalogStatusReason 验证状态原因的判定优先级与四种不可用形态。
// 徽章优先级写进 model 常量: fx_missing > disabled > key_disabled > breaker > unpriced。
func TestMarketCatalogStatusReason(t *testing.T) {
	marketSetup(t)
	scope := model.Scope{Role: model.RoleAdmin}
	breakerUntil := time.Now().Add(5 * time.Minute)
	marketFixture(t, "healthy", "market-healthy", nil)
	marketFixture(t, "off", "market-off", func(c *model.Channel, _ *model.ChannelKey, _ *model.ChannelModel) {
		c.Enabled = false
	})
	marketFixture(t, "key-off", "market-key-off", func(_ *model.Channel, k *model.ChannelKey, _ *model.ChannelModel) {
		k.Enabled = false
	})
	marketFixture(t, "broken", "market-broken", func(_ *model.Channel, _ *model.ChannelKey, cm *model.ChannelModel) {
		cm.DisabledUntil = &breakerUntil
	})
	marketFixture(t, "free", "market-free", func(_ *model.Channel, _ *model.ChannelKey, cm *model.ChannelModel) {
		cm.SupplyPrice = model.LLMPrice{}
	})

	reasons := make(map[string]string, 5)
	available := make(map[string]bool, 5)
	for _, entry := range MarketCatalog(scope) {
		reasons[entry.Name] = entry.Channels[0].StatusReason
		available[entry.Name] = entry.Channels[0].Available
	}
	want := map[string]string{
		"market-healthy": "",
		"market-off":     model.MarketStatusDisabled,
		"market-key-off": model.MarketStatusKeyDisabled,
		"market-broken":  model.MarketStatusBreaker,
		"market-free":    model.MarketStatusUnpriced,
	}
	for name, reason := range want {
		if reasons[name] != reason {
			t.Fatalf("%s status_reason = %q, want %q", name, reasons[name], reason)
		}
	}
	// 未定价不是"不可用": 免费模型照常能跑, 只是价格位要显示未定价而不是 0 元。
	if !available["market-free"] {
		t.Fatal("zero-priced model must stay available")
	}
	for _, name := range []string{"market-off", "market-key-off", "market-broken"} {
		if available[name] {
			t.Fatalf("%s must be unavailable", name)
		}
	}
}

// TestMarketCatalogPriceMatchesGroup 是本次改动最要紧的一处一致性: 广场显示的用户价
// 必须与分组页同一授权显示的价格逐个相等。两侧现在共用一个单点(grantLedgerPrices),
// 本用例把这条约束锁住 —— 一旦有人另写一份折算/上浮, 这里立刻红。
func TestMarketCatalogPriceMatchesGroup(t *testing.T) {
	marketSetup(t)
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	// USD 零折算 + 非 USD 折算两条, 覆盖"要不要查牌价"两种路径。
	seedFxRate(t, "USD", "CNY", 7.2)
	t.Cleanup(FxRateCacheReset)
	_, usdGrant := marketFixture(t, "usd", "market-usd", nil)
	_, cnyGrant := marketFixture(t, "cny", "market-cny", func(_ *model.Channel, _ *model.ChannelKey, cm *model.ChannelModel) {
		cm.SupplyPrice = model.LLMPrice{Input: 72, Output: 144}
		cm.PriceCurrency = "CNY"
	})
	if err := channelRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}

	catalog := MarketCatalog(scope)
	prices := make(map[int]model.LLMPrice, len(catalog))
	for _, entry := range catalog {
		for _, channel := range entry.Channels {
			prices[channel.ChannelGrantID] = channel.UserPrice
		}
	}

	candidates := ChannelGrantCandidates(scope)
	candidatePrices := make(map[int]model.LLMPrice, len(candidates))
	for _, candidate := range candidates {
		candidatePrices[candidate.ID] = candidate.UserPrice
	}

	snapshot := groupSnapshot(model.Group{
		UserID: 1,
		Items: []model.GroupItem{
			{ChannelGrantID: usdGrant.ID},
			{ChannelGrantID: cnyGrant.ID},
		},
	}, scope)

	for _, item := range snapshot.Items {
		want, ok := prices[item.ChannelGrantID]
		if !ok {
			t.Fatalf("grant %d missing from market catalog", item.ChannelGrantID)
		}
		if want != item.UserPrice {
			t.Fatalf("grant %d: market price %+v != group member price %+v", item.ChannelGrantID, want, item.UserPrice)
		}
	}
	for grantID, marketPrice := range prices {
		if candidatePrice, ok := candidatePrices[grantID]; ok && candidatePrice != marketPrice {
			t.Fatalf("grant %d: market price %+v != candidate price %+v", grantID, marketPrice, candidatePrice)
		}
	}
	// CNY 那条必须已折算: 72 CNY 按 7.2 → 10 USD, 上浮后与 USD 那条口径一致。
	if got := prices[cnyGrant.ID].Input; got < 10*(1+ChannelMarkupFor(0))-1e-9 {
		t.Fatalf("cny market price input = %v, looks unconverted", got)
	}
}

// TestMarketCatalogFoldsNamesAndGroups 验证同名折叠与"已加入"角标:
// 大小写不同的两条必须合成一行(与 relay.ModelScoreBriefs 的 strings.ToLower 归并口径一致),
// 且 in_groups 只反映自有分组。
func TestMarketCatalogFoldsNamesAndGroups(t *testing.T) {
	marketSetup(t)
	owner := model.Scope{ID: 1, Role: model.RoleUser}
	if err := db.GetDB().Create(&model.User{Username: "owner", Role: model.RoleUser, Status: model.StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	_, lowerGrant := marketFixture(t, "c1", "minimax-m3", nil)
	marketFixture(t, "c2", "MiniMax-M3", nil)
	marketFixture(t, "c3", "gpt-5", nil)

	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  "mine",
		Mode:  model.GroupModeManual,
		Items: []model.GroupItemInput{{ChannelGrantID: lowerGrant.ID}},
	}, owner, t.Context())
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := groupRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}

	catalog := MarketCatalog(owner)
	byName := make(map[string]model.MarketModel, len(catalog))
	for _, entry := range catalog {
		byName[strings.ToLower(entry.Name)] = entry
	}
	folded, ok := byName["minimax-m3"]
	if !ok {
		t.Fatal("folded minimax-m3 missing")
	}
	if len(folded.Channels) != 2 {
		t.Fatalf("folded minimax-m3 channels = %d, want 2", len(folded.Channels))
	}
	if len(folded.InGroups) != 1 || folded.InGroups[0] != group.ID {
		t.Fatalf("folded minimax-m3 in_groups = %v, want [%d]", folded.InGroups, group.ID)
	}
	// 未加入的模型不给角标; 空集合也要是非 nil 的空数组。
	if notIn := byName["gpt-5"]; len(notIn.InGroups) != 0 || notIn.InGroups == nil {
		t.Fatalf("gpt-5 in_groups = %v, want empty non-nil", notIn.InGroups)
	}
}

// TestMarketCatalogHidesInternals 验证暴露面: 广场响应里不得出现上游渠道真名与凭据名。
// 渠道编码是 #1001 形式, 渠道名 "upstream-real-name" 与凭据名一律不得出现在任何字符串字段里。
func TestMarketCatalogHidesInternals(t *testing.T) {
	marketSetup(t)
	scope := model.Scope{Role: model.RoleAdmin}
	marketFixture(t, "upstream-real-name", "market-secret-model", func(_ *model.Channel, k *model.ChannelKey, _ *model.ChannelModel) {
		k.Name = "credential-xyz"
	})
	for _, entry := range MarketCatalog(scope) {
		for _, channel := range entry.Channels {
			if channel.ChannelCode != "#1001" {
				t.Fatalf("channel_code = %q, want #1001 (published channel must hide upstream name)", channel.ChannelCode)
			}
			joined := channel.ChannelCode
			if strings.Contains(joined, "upstream-real-name") || strings.Contains(joined, "credential-xyz") {
				t.Fatalf("market row leaks internal identity: %+v", channel)
			}
		}
	}
}

// TestMarketBrandOf 验证品牌解析的三段式: family 优先, 关键词兜底, 都不中给 other。
func TestMarketBrandOf(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4":         model.MarketBrandAnthropic,
		"gpt-5":                   model.MarketBrandOpenAI,
		"gemini-3.7-flash":        model.MarketBrandGoogle,
		"deepseek-v4-flash":       model.MarketBrandDeepSeek,
		"qwen3.8-flash":           model.MarketBrandAlibaba,
		"glm-5.3-flash":           model.MarketBrandZhipu,
		"minimax-m3":              model.MarketBrandMiniMax,
		"mimo-v2.5":               model.MarketBrandXiaomi,
		"grok-4.7":                model.MarketBrandXAI,
		"kimi-k3":                 model.MarketBrandMoonshot,
		"mistral-large":           model.MarketBrandMistral,
		"llama-3":                 model.MarketBrandMeta,
		"step-5":                  model.MarketBrandStepFun, // family 为空, 落关键词兜底。
		"deepseek-v4.1-flash-eco": model.MarketBrandDeepSeek,
		"n1":                      model.MarketBrandOther, // 内部代号, 两段都解析不出。
		"wuwen":                   model.MarketBrandOther,
	}
	for name, want := range cases {
		if got := MarketBrandOf(name); got != want {
			t.Fatalf("MarketBrandOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestValidateGroupItemAccessRequiresApproval 锁定"新增成员必须过审"这条闸口。
// 此前 validateGroupItemAccess 只查 shared+listed, 于是 draft/stale 渠道的授权虽然不出现在候选里,
// 却能被直接提交主键加进分组 —— 审核闸口被绕开。本用例把缺口堵上。
func TestValidateGroupItemAccessRequiresApproval(t *testing.T) {
	marketSetup(t)
	owner := model.Scope{ID: 1, Role: model.RoleUser}
	if err := db.GetDB().Create(&model.User{Username: "u", Role: model.RoleUser, Status: model.StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	_, approved := marketFixture(t, "ok", "gate-ok", nil)
	_, draft := marketFixture(t, "draft", "gate-draft", func(c *model.Channel, _ *model.ChannelKey, _ *model.ChannelModel) {
		c.ReviewStatus = model.ChannelReviewDraft
	})
	_, stale := marketFixture(t, "stale", "gate-stale", func(c *model.Channel, _ *model.ChannelKey, _ *model.ChannelModel) {
		c.ReviewStatus = model.ChannelReviewStale
	})

	if err := validateGroupItemAccess(approved.ID, owner); err != nil {
		t.Fatalf("approved grant rejected: %v", err)
	}
	for label, grantID := range map[string]int{"draft": draft.ID, "stale": stale.ID} {
		if err := validateGroupItemAccess(grantID, owner); err == nil {
			t.Fatalf("%s channel grant must be rejected (review gate bypass)", label)
		}
	}
}
