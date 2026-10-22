package op

import (
	"context"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/dlclark/regexp2"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// autoaddTestDB 建一个内存库并迁移自动添加涉及的几张表。
func autoaddTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // 内存库单连接, 避免各协程拿到独立实例。
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// autoaddSeed 建一个渠道 + 一个凭据 + 已有模型 m1/m0(带授权), 返回各主键。
func autoaddSeed(t *testing.T, conn *gorm.DB) (channelID, keyID, m0ID, m1ID int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "auto", BaseURL: "http://up"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = channel.ID
	key := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	keyID = key.ID
	// m0: 上游未返回的既有模型, 必须原样保留。
	m0 := model.ChannelModel{ChannelID: channelID, Name: "m0"}
	if err := conn.Create(&m0).Error; err != nil {
		t.Fatalf("create m0: %v", err)
	}
	m0ID = m0.ID
	// m1: 已有授权带 Anthropic 位(1<<3), 探测并入时只 OR 不清除。
	m1 := model.ChannelModel{ChannelID: channelID, Name: "m1"}
	if err := conn.Create(&m1).Error; err != nil {
		t.Fatalf("create m1: %v", err)
	}
	m1ID = m1.ID
	grant := model.ChannelGrant{ChannelModelID: m1ID, ChannelKeyID: keyID, Protocols: model.ProtocolAnthropicMessage}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return channelID, keyID, m0ID, m1ID
}

func TestMergeFetchedForKey(t *testing.T) {
	t.Run("or-merge and add and keep", func(t *testing.T) {
		conn := autoaddTestDB(t)
		channelID, keyID, m0ID, m1ID := autoaddSeed(t, conn)
		fetched := []model.ChannelFetchModel{
			{Name: "m1", Protocols: model.ProtocolOpenAIResponse},                                   // 已有: OR 并入 Response 位, Anthropic 位保留。
			{Name: "  m2  ", Protocols: model.ProtocolOpenAIResponse},                               // 新增: 名称两侧空白被 trim。
			{Name: "dall-e-3", Protocols: model.ProtocolOpenAIResponse | model.ProtocolOpenAIImage}, // 新增 + 生图位。
		}
		newCount := 0
		added, truncated, err := mergeFetchedForKey(conn, channelID, keyID, fetched, autoAddModelLimit, &newCount)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		// m1 授权 = Anthropic | Response = 0b1100。
		var g model.ChannelGrant
		if err := conn.Where("channel_model_id = ? AND channel_key_id = ?", m1ID, keyID).First(&g).Error; err != nil {
			t.Fatalf("load m1 grant: %v", err)
		}
		if g.Protocols != model.ProtocolAnthropicMessage|model.ProtocolOpenAIResponse {
			t.Fatalf("m1 grant = %b, want %b", g.Protocols, model.ProtocolAnthropicMessage|model.ProtocolOpenAIResponse)
		}
		// m0 仍在, m2/dall-e-3 已建。
		var count int64
		conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count)
		if count != 4 {
			t.Fatalf("models = %d, want 4 (m0+m1+m2+dall-e-3)", count)
		}
		var m0 model.ChannelModel
		if err := conn.First(&m0, m0ID).Error; err != nil {
			t.Fatalf("m0 removed unexpectedly: %v", err)
		}
		// 新增名单与 trim 语义。
		wantAdded := []string{"m2", "dall-e-3"}
		if len(added) != len(wantAdded) || added[0] != wantAdded[0] || added[1] != wantAdded[1] {
			t.Fatalf("added = %v, want %v", added, wantAdded)
		}
		if newCount != 2 {
			t.Fatalf("newCount = %d, want 2", newCount)
		}
		// kind 重算: dall-e-3 生图位 → image; m1 无生图位 → 文本。
		if err := syncChannelModelKinds(conn, channelID); err != nil {
			t.Fatalf("kind sync: %v", err)
		}
		var dall model.ChannelModel
		if err := conn.Where("channel_id = ? AND name = ?", channelID, "dall-e-3").First(&dall).Error; err != nil {
			t.Fatalf("load dall-e-3: %v", err)
		}
		if dall.Kind != model.MediaKindImage {
			t.Fatalf("dall-e-3 kind = %q, want image", dall.Kind)
		}
		var m1model model.ChannelModel
		if err := conn.First(&m1model, m1ID).Error; err != nil {
			t.Fatalf("load m1: %v", err)
		}
		if m1model.Kind != model.MediaKindText {
			t.Fatalf("m1 kind = %q, want text", m1model.Kind)
		}
	})

	t.Run("cap at limit", func(t *testing.T) {
		conn := autoaddTestDB(t)
		channelID, keyID, _, _ := autoaddSeed(t, conn)
		fetched := make([]model.ChannelFetchModel, 0, 205)
		for i := 0; i < 205; i++ {
			fetched = append(fetched, model.ChannelFetchModel{Name: "m" + string(rune(48+i%10)) + string(rune(97+i/10)) + string(rune(97+i%26)), Protocols: model.ProtocolOpenAIResponse})
		}
		// 名称必须唯一且含 205 个: 用 m-<i> 生成。
		fetched = fetched[:0]
		for i := 0; i < 205; i++ {
			fetched = append(fetched, model.ChannelFetchModel{Name: "cap-" + fmtInt(i), Protocols: model.ProtocolOpenAIResponse})
		}
		newCount := 0
		added, truncated, err := mergeFetchedForKey(conn, channelID, keyID, fetched, autoAddModelLimit, &newCount)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if !truncated {
			t.Fatalf("expected truncation")
		}
		if newCount != autoAddModelLimit {
			t.Fatalf("newCount = %d, want %d", newCount, autoAddModelLimit)
		}
		if len(added) != autoAddModelLimit {
			t.Fatalf("added = %d, want %d", len(added), autoAddModelLimit)
		}
		var count int64
		conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count)
		if count != int64(autoAddModelLimit+2) { // 既有 m0, m1 + 新增 200。
			t.Fatalf("models = %d, want %d", count, autoAddModelLimit+2)
		}
	})
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for i > 0 {
		digits = append([]byte{byte(48 + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// ---- 自动添加条件过滤(B4) ----

// TestAutoAddFilterAllow 覆盖条件的判定顺序与边界。
// 关键不变量:
//   - 空条件(全 0 / 无正则)一律放行 —— 升级到本版不改变既有渠道的并入结果;
//   - 参考价缺失时放行 —— 私有模型不该被价格条件一刀切掉;
//   - 渠道无基线(Baseline=0)时倍率条件跳过 —— 新渠道要能添加进第一个模型。
func TestAutoAddFilterAllow(t *testing.T) {
	exclude, err := regexp2.Compile("^gpt-3", regexp2.ECMAScript)
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}

	cases := []struct {
		name   string
		filter AutoAddFilter
		model  string
		wantOK bool
	}{
		{"空条件全部放行", AutoAddFilter{}, "any-model", true},
		{"排除正则命中", AutoAddFilter{Exclude: exclude}, "gpt-3.5-turbo", false},
		{"排除正则未命中", AutoAddFilter{Exclude: exclude}, "gpt-4o", true},
		{"未登记价格的模型放行", AutoAddFilter{PriceMin: 100, PriceMax: 200}, "totally-unknown-model-xyz", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := tc.filter.Allow(tc.model)
			if ok != tc.wantOK {
				t.Fatalf("Allow(%q) = %v, want %v (reason: %s)", tc.model, ok, tc.wantOK, reason)
			}
			if !ok && reason == "" {
				t.Fatalf("Allow(%q) rejected without reason", tc.model)
			}
		})
	}
}

// TestAutoAddFilterMarkupSkippedWithoutBaseline 验证新渠道(无定价模型)不会被倍率条件挡死。
// 否则倍率条件一开, 空渠道永远添加不进第一个模型, 自动添加形同虚设。
func TestAutoAddFilterMarkupSkippedWithoutBaseline(t *testing.T) {
	filter := AutoAddFilter{MarkupMin: 2.0, MarkupMax: 3.0, Baseline: 0}
	if ok, reason := filter.Allow("totally-unknown-model-xyz"); !ok {
		t.Fatalf("expected pass when baseline is 0, got rejection: %s", reason)
	}
}

// TestAutoAddFilterPriceRange 验证价格区间对已登记模型生效(blended = 输入输出均值)。
func TestAutoAddFilterPriceRange(t *testing.T) {
	llmModelCache.Set("test-auto-add-priced", model.LLMPrice{Input: 100, Output: 200})
	t.Cleanup(func() { llmModelCache.Del("test-auto-add-priced") })

	// blended = (100+200)/2 = 150
	if ok, reason := (AutoAddFilter{PriceMin: 10, PriceMax: 100}).Allow("test-auto-add-priced"); ok {
		t.Fatalf("expected rejection: blended 150 above max 100")
	} else if reason == "" {
		t.Fatalf("rejection without reason")
	}
	if ok, reason := (AutoAddFilter{PriceMin: 10, PriceMax: 200}).Allow("test-auto-add-priced"); !ok {
		t.Fatalf("expected pass for blended 150 in [10,200], got: %s", reason)
	}
	if ok, reason := (AutoAddFilter{PriceMin: 200, PriceMax: 300}).Allow("test-auto-add-priced"); ok {
		t.Fatalf("expected rejection: blended 150 below min 200")
	} else if reason == "" {
		t.Fatalf("rejection without reason")
	}
}

// TestAutoAddFilterMarkupWithBaseline 验证倍率区间: 模型参考价 ÷ 渠道基线。
func TestAutoAddFilterMarkupWithBaseline(t *testing.T) {
	llmModelCache.Set("test-auto-add-markup", model.LLMPrice{Input: 300, Output: 300}) // blended = 300
	t.Cleanup(func() { llmModelCache.Del("test-auto-add-markup") })

	// 基线 100, 倍率 3.0x: 落在 [2,4] 内 → 放行
	if ok, reason := (AutoAddFilter{MarkupMin: 2, MarkupMax: 4, Baseline: 100}).Allow("test-auto-add-markup"); !ok {
		t.Fatalf("expected pass for 3x in [2,4], got: %s", reason)
	}
	// 上限 2.5x → 3x 超限 → 拒绝
	if ok, reason := (AutoAddFilter{MarkupMax: 2.5, Baseline: 100}).Allow("test-auto-add-markup"); ok {
		t.Fatalf("expected rejection for 3x above max 2.5x")
	} else if reason == "" {
		t.Fatalf("rejection without reason")
	}
	// 下限 3.5x → 3x 不足 → 拒绝
	if ok, reason := (AutoAddFilter{MarkupMin: 3.5, Baseline: 100}).Allow("test-auto-add-markup"); ok {
		t.Fatalf("expected rejection for 3x below min 3.5x")
	} else if reason == "" {
		t.Fatalf("rejection without reason")
	}
}

// TestFillSupplyPriceFromReference 验证预填供货价只填四价全 0 的模型, 不覆盖人工定价。
func TestFillSupplyPriceFromReference(t *testing.T) {
	conn := autoaddTestDB(t)
	channelID, _, m0ID, m1ID := autoaddSeed(t, conn)
	llmModelCache.Set("m0", model.LLMPrice{Input: 2, Output: 4})
	llmModelCache.Set("m1", model.LLMPrice{Input: 9, Output: 9})
	t.Cleanup(func() {
		llmModelCache.Del("m0")
		llmModelCache.Del("m1")
	})

	// m1 先手工定价, 预填不该覆盖它。
	// 必须用 Select + 结构体 Updates 让 gorm 走 serializer:json;
	// Update("supply_price", LLMPrice{...}) 会跳过序列化并报 unsupported type。
	manual := model.ChannelModel{SupplyPrice: model.LLMPrice{Input: 7, Output: 8}}
	if err := conn.Model(&model.ChannelModel{}).Where("id = ?", m1ID).
		Select("supply_price").Updates(&manual).Error; err != nil {
		t.Fatalf("set manual price: %v", err)
	}

	if err := fillSupplyPriceFromReference(conn, channelID, []string{"m0", "m1"}); err != nil {
		t.Fatalf("fill: %v", err)
	}

	var m0, m1 model.ChannelModel
	if err := conn.First(&m0, m0ID).Error; err != nil {
		t.Fatalf("load m0: %v", err)
	}
	if err := conn.First(&m1, m1ID).Error; err != nil {
		t.Fatalf("load m1: %v", err)
	}
	if m0.SupplyPrice.Input != 2 || m0.SupplyPrice.Output != 4 {
		t.Fatalf("m0 supply price = %+v, want reference {2,4}", m0.SupplyPrice)
	}
	if m1.SupplyPrice.Input != 7 || m1.SupplyPrice.Output != 8 {
		t.Fatalf("m1 supply price = %+v, want manual {7,8} (must not be overwritten)", m1.SupplyPrice)
	}
}

// TestAutoAddFilterInclude 验证名称白名单: 设了就只放行命中的模型, 且与排除/价格/倍率同取 AND。
// 不变量: 白名单为空(nil)时行为与升级前完全一致 —— 这是「升级不改变既有渠道并入结果」的前提。
func TestAutoAddFilterInclude(t *testing.T) {
	include, err := regexp2.Compile("^gpt-4", regexp2.ECMAScript)
	if err != nil {
		t.Fatalf("compile include: %v", err)
	}
	exclude, err := regexp2.Compile("mini", regexp2.ECMAScript)
	if err != nil {
		t.Fatalf("compile exclude: %v", err)
	}

	t.Run("白名单命中放行", func(t *testing.T) {
		if ok, reason := (AutoAddFilter{Include: include}).Allow("gpt-4o"); !ok {
			t.Fatalf("expected pass, got rejection: %s", reason)
		}
	})
	t.Run("白名单未命中拒绝", func(t *testing.T) {
		ok, reason := (AutoAddFilter{Include: include}).Allow("claude-3-opus")
		if ok {
			t.Fatalf("expected rejection")
		}
		if reason == "" {
			t.Fatalf("rejection without reason")
		}
	})
	t.Run("白名单与排除取交集", func(t *testing.T) {
		filter := AutoAddFilter{Include: include, Exclude: exclude}
		if ok, reason := filter.Allow("gpt-4o-mini"); ok {
			t.Fatalf("expected rejection by exclude, got pass")
		} else if reason == "" {
			t.Fatalf("rejection without reason")
		}
		if ok, reason := filter.Allow("gpt-4o"); !ok {
			t.Fatalf("expected pass, got: %s", reason)
		}
	})
	t.Run("空条件全部放行", func(t *testing.T) {
		// 升级前的既有行为: 无条件时探测到什么就并入什么。
		if ok, reason := (AutoAddFilter{}).Allow("any-model"); !ok {
			t.Fatalf("expected pass, got: %s", reason)
		}
	})
}

// TestAutoAddRunSkipsWhenDisabled 验证后台复查开关关闭时任务直接返回, 不触碰数据库。
// 默认关是这次升级的安全阀: 打开后每轮都会向每个渠道发上游探测请求, 会消耗上游配额。
func TestAutoAddRunSkipsWhenDisabled(t *testing.T) {
	settingCache.Set(model.SettingKeyAutoAddBackground, "false")
	t.Cleanup(func() { settingCache.Del(model.SettingKeyAutoAddBackground) })

	// 不注入工厂也不建库: 开关关闭必须在此之前就返回, 否则会因 nil DB 直接 panic。
	if err := AutoAddChannelsRun(context.Background()); err != nil {
		t.Fatalf("expected nil error when disabled, got %v", err)
	}
}

// TestAutoAddIncludeSettingValidated 验证白名单正则的保存校验: 编不过的正则必须被拒,
// 否则保存成功但自动添加静默变成「永不并入」, 界面上看不出任何异常。
func TestAutoAddIncludeSettingValidated(t *testing.T) {
	bad := model.Setting{Key: model.SettingKeyAutoAddInclude, Value: "([unclosed"}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected validation error for unclosed regex")
	}
	good := model.Setting{Key: model.SettingKeyAutoAddInclude, Value: "^gpt-4"}
	if err := good.Validate(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	// 周期 0 小时等于每秒一轮探测, 必须被拒。
	zero := model.Setting{Key: model.SettingKeyAutoAddInterval, Value: "0"}
	if err := zero.Validate(); err == nil {
		t.Fatalf("expected validation error for 0-hour interval")
	}
}
// TestAutoAddScope 验证复查范围的取值语义: 只有字面 "approved" 才放大到全部已过审渠道。
// 读不到键或值拼错时必须退回 false(只扫已开启的) —— 范围放大等于向上游多发请求,
// 不能因为一次配置读取失败就变成对全部渠道的探测。
func TestAutoAddScope(t *testing.T) {
	t.Cleanup(func() { settingCache.Del(model.SettingKeyAutoAddScope) })

	caseList := []struct {
		name  string
		value string
		want  bool
	}{
		{"未配置时保守取 false", "", false},
		{"flagged 只扫已开启渠道", "flagged", false},
		{"approved 覆盖全部已过审", "approved", true},
		{"大小写不符不算命中", "Approved", false},
		{"拼错的值退回保守", "aproved", false},
	}
	for _, c := range caseList {
		t.Run(c.name, func(t *testing.T) {
			settingCache.Set(model.SettingKeyAutoAddScope, c.value)
			if got := AutoAddScopeAllApproved(); got != c.want {
				t.Fatalf("AutoAddScopeAllApproved(%q) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

// TestAutoAddScopeSettingValidated 验证 scope 保存校验: 拼错的值必须在保存时被拒,
// 否则它会静默退回"只扫已开启渠道", 管理员会以为功能没生效。
func TestAutoAddScopeSettingValidated(t *testing.T) {
	for _, bad := range []string{"", "aproved", "all", "APPROVED"} {
		s := model.Setting{Key: model.SettingKeyAutoAddScope, Value: bad}
		if err := s.Validate(); err == nil {
			t.Fatalf("expected validation error for scope %q", bad)
		}
	}
	for _, good := range []string{"flagged", "approved"} {
		s := model.Setting{Key: model.SettingKeyAutoAddScope, Value: good}
		if err := s.Validate(); err != nil {
			t.Fatalf("scope %q should be valid, got %v", good, err)
		}
	}
}
