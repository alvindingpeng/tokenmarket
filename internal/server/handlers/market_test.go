package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// marketAPIEnv 起一套最小环境: 临时库 + 签名密钥 + 一个普通用户, 返回该用户的会话 Cookie。
// 广场的核心受众就是普通用户, 故用例一律用它, 顺带覆盖"登录即可、无角色白名单"这条鉴权口径。
func marketAPIEnv(t *testing.T) (model.User, string) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "market-api.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	// 用户名按用例名唯一: 进程级 userNameIndex 在用例之间不清空(InitCache 不重建用户索引),
	// 固定用户名会让第二个用例被判"已存在"。
	username := "market-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	user, err := op.UserCreate(username, "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

// marketSeed 造一条「已发布 + 已过审 + 已上架」的挂牌并刷新缓存, 返回渠道与授权。
func marketSeed(t *testing.T, channelName, shareCode, modelName string, kind model.MediaKind, supply model.LLMPrice, tune func(channel *model.Channel, cm *model.ChannelModel)) (model.Channel, model.ChannelGrant) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: channelName, Enabled: true}}
	channel.Shared = true
	channel.ReviewStatus = model.ChannelReviewApproved
	channel.ShareCode = shareCode
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: channelName + "-key", Key: "secret", Enabled: true}}
	cm := model.ChannelModel{Name: modelName, Listed: true, Kind: kind, SupplyPrice: supply}
	if tune != nil {
		tune(&channel, &cm)
	}
	channelDisabled := !channel.Enabled
	keyDisabled := !key.Enabled
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key.ChannelID = channel.ID
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	// enabled 列带 gorm default:true, 零值 false 会被回填成 true; 反例必须写库后重写一次。
	if channelDisabled {
		_ = db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).Update("enabled", false).Error
	}
	if keyDisabled {
		_ = db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", key.ID).Update("enabled", false).Error
	}
	cm.ChannelID = channel.ID
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: cm.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	// 刷新后不再回读缓存: 用例只需要主键, 渠道实体只用 ChannelConfig 那部分做断言。
	return channel, grant
}

// marketRequest 带 Cookie 打一次广场接口。
func marketRequest(t *testing.T, engine *gin.Engine, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/market/models"+query, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "auth", Value: token})
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// marketPayload 解析广场响应; 失败信息里带上原始 body, 便于定位形状变化。
func marketPayload(t *testing.T, w *httptest.ResponseRecorder) marketCatalogResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		Data marketCatalogResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse market response: %v (%s)", err, w.Body.String())
	}
	return payload.Data
}

// TestMarketRouteRequiresLogin 验证鉴权: 未登录 401, 普通用户 200 —— 广场不给角色白名单。
func TestMarketRouteRequiresLogin(t *testing.T) {
	_, token := marketAPIEnv(t)
	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)

	if w := marketRequest(t, engine, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous access: got %d, want 401", w.Code)
	}
	if w := marketRequest(t, engine, token, ""); w.Code != http.StatusOK {
		t.Fatalf("普通用户访问广场: got %d %s", w.Code, w.Body.String())
	}
}

// TestMarketResponseHidesInternals 是暴露面验收: 响应里不得出现上游渠道真名、凭据名与供货价。
// 广场面向全部登录用户, 字段集只能是分组页候选的子集。
func TestMarketResponseHidesInternals(t *testing.T) {
	_, token := marketAPIEnv(t)
	marketSeed(t, "upstream-real-name", "1001", "gpt-5", "", model.LLMPrice{Input: 10, Output: 20}, nil)

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	w := marketRequest(t, engine, token, "")
	body := w.Body.String()

	for _, leak := range []string{"upstream-real-name", "credential", "supply_price", "\"key_name\""} {
		if strings.Contains(body, leak) {
			t.Fatalf("market response leaks %q: %s", leak, body)
		}
	}
	payload := marketPayload(t, w)
	if len(payload.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(payload.Models))
	}
	if payload.Models[0].Channels[0].ChannelCode != "#1001" {
		t.Fatalf("channel_code = %q, want #1001", payload.Models[0].Channels[0].ChannelCode)
	}
}

// TestMarketSortHidesColdStartAtBottom 验证排序: 无样本的模型不得因为"没数据"排到第一屏,
// 也不得被渲染成 100% 成功 —— metrics_source 必须标 none 且 success/wait_ms 省略。
func TestMarketSortHidesColdStartAtBottom(t *testing.T) {
	_, token := marketAPIEnv(t)
	for _, name := range []string{"alpha-cold", "beta-cold"} {
		marketSeed(t, name, "1001", name, "", model.LLMPrice{Input: 10, Output: 20}, nil)
	}
	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)

	for _, sortBy := range []string{"success", "latency"} {
		payload := marketPayload(t, marketRequest(t, engine, token, "?sort="+sortBy))
		for _, entry := range payload.Models {
			if entry.MetricsSource != marketMetricsNone || entry.Samples != 0 {
				t.Fatalf("sort=%s: cold model %s marked source=%q samples=%d", sortBy, entry.Name, entry.MetricsSource, entry.Samples)
			}
			if entry.Success != 0 || entry.WaitMs != 0 {
				t.Fatalf("sort=%s: cold model %s must not report a number: success=%v wait=%v", sortBy, entry.Name, entry.Success, entry.WaitMs)
			}
		}
	}
}

// TestMarketFilterByBrandNameKind 验证三种筛选: 品牌、名称关键词、形态, 以及"只看可用"的裁剪语义。
func TestMarketFilterByBrandNameKind(t *testing.T) {
	_, token := marketAPIEnv(t)
	marketSeed(t, "c-openai", "1001", "gpt-5", "", model.LLMPrice{Input: 10, Output: 20}, nil)
	marketSeed(t, "c-deepseek", "1002", "deepseek-v4-flash", "", model.LLMPrice{Input: 5, Output: 10}, nil)
	marketSeed(t, "c-image", "1003", "flux-pro", model.MediaKindImage, model.LLMPrice{}, func(_ *model.Channel, cm *model.ChannelModel) {
		cm.MediaSupply = model.MediaPrice{PerImage: 0.5}
	})
	// 一条渠道停用的挂牌: 默认可见(带 disabled 徽章), only_available=1 时必须被裁掉。
	marketSeed(t, "c-off", "1005", "gpt-6-luna", "", model.LLMPrice{Input: 10, Output: 20}, func(channel *model.Channel, _ *model.ChannelModel) {
		channel.Enabled = false
	})

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)

	brandPayload := marketPayload(t, marketRequest(t, engine, token, "?brand=deepseek"))
	if len(brandPayload.Models) != 1 || brandPayload.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("brand=deepseek: %+v", brandPayload.Models)
	}
	// 多选语义是并集。
	both := marketPayload(t, marketRequest(t, engine, token, "?brand=deepseek&brand=openai"))
	if len(both.Models) != 3 {
		t.Fatalf("brand=deepseek&openai: %d models, want 3 (gpt-5/gpt-6-luna/deepseek-v4-flash)", len(both.Models))
	}
	if keyword := marketPayload(t, marketRequest(t, engine, token, "?q=LUNA")); len(keyword.Models) != 1 || keyword.Models[0].Name != "gpt-6-luna" {
		t.Fatalf("q=LUNA (case-insensitive): %+v", keyword.Models)
	}
	if image := marketPayload(t, marketRequest(t, engine, token, "?kind=image")); len(image.Models) != 1 || image.Models[0].Name != "flux-pro" {
		t.Fatalf("kind=image: %+v", image.Models)
	}
	available := marketPayload(t, marketRequest(t, engine, token, "?only_available=1"))
	for _, entry := range available.Models {
		if entry.Name == "gpt-6-luna" {
			t.Fatal("only_available must drop the disabled channel row and its model")
		}
	}
	// 品牌清单来自响应本身: 供前端下拉, 不写死。
	if len(brandPayload.Brands) == 0 {
		t.Fatal("brands must be reported for the frontend dropdown")
	}
}

// TestMarketMetricsSourceThreshold 锁定成绩口径的三档判定。
//
// 这一档是实测逼出来的: 生产上一个只有 1 次调用的模型以 43ms 排到了"延迟最优"第一位,
// 而它恰好只成功过那一次。样本不足与无样本在展示上是同一件事(都不给数字), 但必须可区分 ——
// 界面要能说清"还没跑几次"而不是"还没跑过", 排查时完全是两回事。
func TestMarketMetricsSourceThreshold(t *testing.T) {
	cases := []struct {
		samples int
		want    string
	}{
		{0, marketMetricsNone},
		{-3, marketMetricsNone},
		{1, marketMetricsInsufficient},
		{marketMinSamples - 1, marketMetricsInsufficient},
		{marketMinSamples, marketMetricsLive},
		{marketMinSamples + 500, marketMetricsLive},
	}
	for _, c := range cases {
		if got := marketMetricsSourceOf(c.samples); got != c.want {
			t.Fatalf("samples=%d: source=%q, want %q", c.samples, got, c.want)
		}
	}
	// 门槛必须与质量评级的样本下限同源: 两个阈值各改各的, 会让"广场可信"与"评级可信"分叉。
	if marketMinSamples != int(model.QualityMinSamples) {
		t.Fatalf("marketMinSamples=%d, want model.QualityMinSamples=%d", marketMinSamples, model.QualityMinSamples)
	}
}

// TestMarketPriceSortPutsUnpricedLast 验证"零价不霸榜": 未定价的模型必须沉底,
// 而不是以 0 元排成最便宜。
func TestMarketPriceSortPutsUnpricedLast(t *testing.T) {
	_, token := marketAPIEnv(t)
	marketSeed(t, "c-cheap", "1001", "a-cheap", "", model.LLMPrice{Input: 1, Output: 1}, nil)
	marketSeed(t, "c-free", "1002", "b-unpriced", "", model.LLMPrice{}, nil)
	marketSeed(t, "c-expensive", "1003", "c-expensive", "", model.LLMPrice{Input: 100, Output: 200}, nil)

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	payload := marketPayload(t, marketRequest(t, engine, token, "?sort=price"))
	if len(payload.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(payload.Models))
	}
	if payload.Models[0].Name != "a-cheap" {
		t.Fatalf("cheapest first: got %q", payload.Models[0].Name)
	}
	if payload.Models[len(payload.Models)-1].Name != "b-unpriced" {
		t.Fatalf("unpriced must sink to the bottom: got %q last", payload.Models[len(payload.Models)-1].Name)
	}
	if !payload.Models[len(payload.Models)-1].Channels[0].Unpriced {
		t.Fatal("bottom row must carry unpriced=true")
	}
}

// TestMarketUpdatedAtIsRecent 验证响应里的 updated_at 是本轮生成时刻(RFC3339 可解析且接近当前)。
func TestMarketUpdatedAtIsRecent(t *testing.T) {
	_, token := marketAPIEnv(t)
	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	payload := marketPayload(t, marketRequest(t, engine, token, ""))
	if time.Since(payload.UpdatedAt) > time.Minute || payload.UpdatedAt.IsZero() {
		t.Fatalf("updated_at = %v, want a fresh timestamp", payload.UpdatedAt)
	}
}
