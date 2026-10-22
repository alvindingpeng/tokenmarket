package op

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/dlclark/regexp2"
)

// ruleTestDB 建一个带自动添加条件的库, 并清掉缓存, 免得用例之间串。
func ruleTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", t.TempDir()+"/rule.db", false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := db.GetDB().AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.ChannelGrant{}, &model.ModelAutoAddRule{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	modelAutoAddRuleCache.Clear()
	t.Cleanup(func() { modelAutoAddRuleCache.Clear() })
}

// TestAutoAddRuleSaveNormalizeAndEmpty 验证三件事:
// 保存会把名称与三态值归一; 非法值被拒而不是静默回落; 全空条件直接删行而不留死数据。
func TestAutoAddRuleSaveNormalizeAndEmpty(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()

	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 7, Name: "  GPT-4o  ",
		PriceMax: " 12.5 ", Mode: "ON", FillPrice: "Off"}, ctx); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := AutoAddRuleGet(7, "gpt-4o")
	if !ok {
		t.Fatalf("rule not cached after save")
	}
	if got.Name != "gpt-4o" || got.Mode != "on" || got.FillPrice != "off" || got.PriceMax != "12.5" {
		t.Fatalf("normalize failed: %+v", got)
	}

	// 非法三态与非法正则必须在保存时被拒: 悄悄回落全局等于条件形同虚设, 且没有任何提示。
	bad := []model.ModelAutoAddRule{
		{UserID: 7, Name: "x", Mode: "maybe"},
		{UserID: 7, Name: "x", PriceMin: "abc"},
		{UserID: 7, Name: "x", Include: "([unclosed"},
		{UserID: 0, Name: "x", PriceMin: "1"},
		{UserID: 7, Name: "   "},
	}
	for _, rule := range bad {
		if _, err := AutoAddRuleSave(rule, ctx); err == nil {
			t.Fatalf("expected rejection for %+v", rule)
		}
	}

	// 全空条件等价于删除。
	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 7, Name: "gpt-4o"}, ctx); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if _, ok := AutoAddRuleGet(7, "gpt-4o"); ok {
		t.Fatalf("empty rule should have been deleted")
	}
	var rows int64
	if err := db.GetDB().Model(&model.ModelAutoAddRule{}).Count(&rows).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("expected table empty after saving an all-blank rule, got %d rows", rows)
	}
}

// TestAutoAddRuleScopedByUser 验证条件按用户隔离: 甲设的条件影响甲, 不影响乙。
func TestAutoAddRuleScopedByUser(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()

	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 1, Name: "shared", Mode: "off"}, ctx); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := AutoAddRuleGet(1, "shared"); !ok {
		t.Fatalf("user 1 rule missing")
	}
	if _, ok := AutoAddRuleGet(2, "shared"); ok {
		t.Fatalf("user 2 must not see user 1 rule")
	}
	list := AutoAddRuleList(1)
	if len(list) != 1 || list[0].UserID != 1 {
		t.Fatalf("user 1 list = %+v", list)
	}
	if len(AutoAddRuleList(2)) != 0 {
		t.Fatalf("user 2 list should be empty")
	}

	// 删别人的条件不该删掉本人的: 这是「按用户存」最容易被写错的地方。
	if err := AutoAddRuleDelete(2, "shared", ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := AutoAddRuleGet(1, "shared"); !ok {
		t.Fatalf("user 1 rule was deleted by user 2")
	}
	// 幂等: 再删一次仍成功。
	if err := AutoAddRuleDelete(1, "shared", ctx); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if err := AutoAddRuleDelete(1, "shared", ctx); err != nil {
		t.Fatalf("delete missing rule should be a no-op, got: %v", err)
	}
}

// TestAutoAddRuleRefreshCache 验证启动时能把库里的条件读进缓存。
func TestAutoAddRuleRefreshCache(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()
	if err := db.GetDB().Create(&model.ModelAutoAddRule{UserID: 9, Name: "cached", PriceMin: "3"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	modelAutoAddRuleCache.Clear()
	if err := autoAddRuleRefreshCache(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, ok := AutoAddRuleGet(9, "CACHED") // 名称大小写不敏感。
	if !ok || got.PriceMin != "3" {
		t.Fatalf("cache refresh missed the row: %+v ok=%v", got, ok)
	}
}

// TestAutoAddFilterForModelLayering 验证逐字段回落:
// 填了的字段覆盖全局, 留空的字段继续跟全局走, 而不是被清空。
func TestAutoAddFilterForModelLayering(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()

	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{
		UserID: 4, Name: "layered", PriceMax: "9", Include: "^layered",
	}, ctx); err != nil {
		t.Fatalf("save: %v", err)
	}

	global := AutoAddFilter{
		PriceMin: 1, PriceMax: 100, MarkupMin: 2, MarkupMax: 4,
		Exclude: mustRegexp(t, "^old"), FillPrice: false, Baseline: 10,
	}
	out := global.forModel(4, "layerED")
	if out.PriceMax != 9 {
		t.Fatalf("model PriceMax = %v, want 9", out.PriceMax)
	}
	// 留空的字段必须保持全局值 —— 这是「只给某个贵模型加一条上限」能成立的前提。
	if out.PriceMin != 1 || out.MarkupMin != 2 || out.MarkupMax != 4 || out.Baseline != 10 {
		t.Fatalf("blank fields did not fall back to global: %+v", out)
	}
	if out.Include == nil || out.Exclude == nil {
		t.Fatalf("model include must be replaced while global exclude stays: %+v", out)
	}
	if ok, _ := out.Allow("layered"); !ok {
		t.Fatalf("model with include ^layered should pass")
	}

	// 没设条件的模型原样拿到全局条件。
	plain := global.forModel(4, "other")
	if plain.PriceMax != 100 || plain.PriceMin != 1 || plain.Mode != "" {
		t.Fatalf("model without rule must inherit global untouched: %+v", plain)
	}
	// 别的用户的同名条件不生效。
	stranger := global.forModel(5, "layered")
	if stranger.PriceMax != 100 {
		t.Fatalf("another user's rule leaked: %+v", stranger)
	}
}

// TestAutoAddFilterModeOverridesConditions 验证三态开关直接定结论: on 跳过其余条件, off 无条件拒绝。
func TestAutoAddFilterModeOverridesConditions(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()

	// 全局条件极严: 白名单只放行 "^never", 价格区间把人挡在外面。
	strict := AutoAddFilter{PriceMin: 1000, PriceMax: 2000, Include: mustRegexp(t, "^never$")}

	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 3, Name: "vip", Mode: "on"}, ctx); err != nil {
		t.Fatalf("save on: %v", err)
	}
	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 3, Name: "junk", Mode: "off"}, ctx); err != nil {
		t.Fatalf("save off: %v", err)
	}

	if ok, reason := strict.forModel(3, "vip").Allow("vip"); !ok {
		t.Fatalf("mode=on must bypass every condition, got: %s", reason)
	}
	if ok, reason := strict.forModel(3, "junk").Allow("junk"); ok {
		t.Fatalf("mode=off must reject unconditionally")
	} else if !strings.Contains(reason, "model rule") {
		t.Fatalf("rejection reason should mention the model rule, got %q", reason)
	}
	// 开关优先于白名单, 也优先于价格: 三者都拦不住 on。
	if ok, _ := strict.forModel(3, "vip").Allow("vip"); !ok {
		t.Fatalf("mode=on must win over include regex and price range")
	}
}

// TestAutoAddRuleEndToEnd 端到端验证「按模型区别设置」真的作用在探测并入上:
// 同一份上游清单, 甲按自己的条件只收便宜的, 乙把某个模型强制并入并关掉另一个。
func TestAutoAddRuleEndToEnd(t *testing.T) {
	ruleTestDB(t)
	ctx := context.Background()

	// 参考价表: cheap=1, pricey=500, vip=900(远超甲的全局上限)。
	llmModelCache.Set("cheap", model.LLMPrice{Input: 1, Output: 1})
	llmModelCache.Set("pricey", model.LLMPrice{Input: 500, Output: 500})
	llmModelCache.Set("vip", model.LLMPrice{Input: 900, Output: 900})
	t.Cleanup(func() {
		llmModelCache.Del("cheap")
		llmModelCache.Del("pricey")
		llmModelCache.Del("vip")
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(model.OpenAIModelList{Object: "list", Data: []model.OpenAIModel{
			{ID: "cheap", Object: "model"},
			{ID: "pricey", Object: "model"},
			{ID: "vip", Object: "model"},
		}})
	}))
	t.Cleanup(upstream.Close)

	// 甲乙各一个渠道, 其余配置完全一致, 只有归属者不同。
	mkChannel := func(owner uint, name string) int {
		ch := model.Channel{UserID: owner, ChannelConfig: model.ChannelConfig{
			Name: name, BaseURL: upstream.URL, ModelAutoAdd: true,
		}}
		ch.ReviewStatus = model.ChannelReviewApproved
		ch.TrustLevel = model.ChannelTrustTrusted
		if err := db.GetDB().Create(&ch).Error; err != nil {
			t.Fatalf("create channel %s: %v", name, err)
		}
		key := model.ChannelKey{ChannelID: ch.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk"}}
		if err := db.GetDB().Create(&key).Error; err != nil {
			t.Fatalf("create key: %v", err)
		}
		channelCache.Set(ch.ID, ch)
		t.Cleanup(func() { channelCache.Del(ch.ID) })
		return ch.ID
	}
	alice, bob := mkChannel(101, "alice"), mkChannel(202, "bob")

	// 全局默认只收 100 以下的模型(甲乙共用这一份全局条件)。
	settingCache.Set(model.SettingKeyAutoAddPriceMax, "100")
	t.Cleanup(func() { settingCache.Del(model.SettingKeyAutoAddPriceMax) })

	// 甲: 没有模型级条件, 于是只收到 cheap。
	// 乙: 给 vip 开强制并入, 给 cheap 单独关掉 —— 两人同一份上游清单, 结果必须不一样。
	settingCache.Set(model.SettingKeyAutoAddFillPrice, "false")
	t.Cleanup(func() { settingCache.Del(model.SettingKeyAutoAddFillPrice) })
	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 202, Name: "vip", Mode: "on"}, ctx); err != nil {
		t.Fatalf("save vip: %v", err)
	}
	if _, err := AutoAddRuleSave(model.ModelAutoAddRule{UserID: 202, Name: "cheap", Mode: "off"}, ctx); err != nil {
		t.Fatalf("save cheap: %v", err)
	}

	client := upstream.Client()
	cfg := func(name string) model.ChannelConfig {
		return model.ChannelConfig{Name: name, BaseURL: upstream.URL}
	}
	if err := AutoAddChannelModels(ctx, alice, cfg("alice"), client, 101); err != nil {
		t.Fatalf("auto add alice: %v", err)
	}
	if err := AutoAddChannelModels(ctx, bob, cfg("bob"), client, 202); err != nil {
		t.Fatalf("auto add bob: %v", err)
	}

	names := func(channelID int) []string {
		var models []model.ChannelModel
		if err := db.GetDB().Where("channel_id = ?", channelID).Order("name").Find(&models).Error; err != nil {
			t.Fatalf("load models: %v", err)
		}
		out := make([]string, 0, len(models))
		for _, m := range models {
			out = append(out, m.Name)
		}
		return out
	}

	if got := strings.Join(names(alice), ","); got != "cheap" {
		t.Fatalf("alice got [%s], want [cheap] — global condition only", got)
	}
	if got := strings.Join(names(bob), ","); got != "vip" {
		t.Fatalf("bob got [%s], want [vip] — his own rule overrides the global one", got)
	}
}

// mustRegexp 编译一个测试用正则; 编译失败即时失败, 不让用例带着 nil 继续跑。
func mustRegexp(t *testing.T, pattern string) *regexp2.Regexp {
	t.Helper()
	re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		t.Fatalf("compile %q: %v", pattern, err)
	}
	return re
}
