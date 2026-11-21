package op

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// setupAutoAddGlobalDB 把包级 db 换成临时 SQLite。
//
// AutoAddChannelModels 读的是全局 db.GetDB(), 没有注入参数 —— 所以这条路只能靠
// db.InitDB 换掉真实连接来测(与 internal/relay 的既有做法一致)。
func setupAutoAddGlobalDB(t *testing.T) {
	t.Helper()
	// 换库前清空进程级缓存, 否则 -shuffle 下会读到上一个用例的旧库数据。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "autoadd.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 换掉全局连接后必须同步清缓存: 缓存里留着上一个用例(或别的包)的渠道与模型,
	// 会让 channelPriceBaseline / channelOwnerID 读到不属于这次库的数据,
	// 表现为"单独跑通过、全量跑偶发失败"这类最难查的间歇性失败。
	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	settingCache.Clear()
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
	})
}

// 这个测试覆盖的是**调用路径**, 而不只是 ProbeChannelKey 本身。
//
// 生产故障: 订阅渠道拉模型报
//
//	gemini cli loadCodeAssist: status=401 body={... Invalid Credentials ...}
//
// 根因是 autoadd 把 channelKey.Key(整包账号 JSON)直接当令牌传给了探测链。
//
// 只测 ProbeChannelKey 抓不到这个回退 —— 回退发生在 autoadd 的调用语句里,
// 所以必须让测试走到 AutoAddChannelModels 这条真实路径上。
func TestAutoAddChannelModelsUnwrapsSubscriptionAccountJSON(t *testing.T) {
	const wantToken = "ya29.sub-access-token"
	const accountJSON = `{"access_token":"` + wantToken + `","refresh_token":"r","expiry_date":4102444800000}`

	var probeAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeAuth = r.Header.Get("Authorization")
		if probeAuth != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			fmt.Fprint(w, `{"cloudaicompanionProject":"proj-1","currentTier":{"id":"free-tier"}}`)
		case strings.Contains(r.URL.Path, "retrieveUserQuota"):
			fmt.Fprint(w, `{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.9,"remainingAmount":"100"}]}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()

	setupAutoAddGlobalDB(t)
	conn := db.GetDB()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		Name:         "订阅渠道",
		Dialect:      model.DialectSubscription,
		SubPlatform:  "gemini",
		BaseURL:      srv.URL,
		ModelAutoAdd: true,
	}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	// 订阅渠道的凭据是导入的账号: Key 列存整包 JSON, AccountKind=oauth。
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name:            "acct-1",
		Key:             accountJSON,
		Enabled:         true,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformAntigravity,
	}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := AutoAddChannelModels(context.Background(), channel.ID, channel.ChannelConfig, srv.Client(), 0); err != nil {
		t.Fatalf("账号 JSON 必须被解成 access_token 再探测, 实际报错: %v", err)
	}
	// 反向断言: Authorization 里绝不能带 refresh_token(那意味着整包 JSON 被当令牌了)。
	if strings.Contains(probeAuth, "refresh_token") {
		t.Fatalf("整包账号 JSON 被塞进了 Authorization(必然 401): %q", probeAuth)
	}
	if probeAuth != "Bearer "+wantToken {
		t.Fatalf("Authorization 不是解出来的 access_token: %q", probeAuth)
	}

	// 探测解析出的 project 必须回写渠道: 转发信封要用它。
	// 实测 project 为空时 generateContent 返回 403 SUBSCRIPTION_REQUIRED,
	// 而那个错误说的是"没有有效产品授权", 完全指不到缺的是 project。
	var saved model.Channel
	if err := conn.Where("id = ?", channel.ID).First(&saved).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if saved.GeminiProject != "proj-1" {
		t.Fatalf("gemini_project = %q, want proj-1(探测解析出的 project 未回写)", saved.GeminiProject)
	}
	// 更关键的是缓存: 转发与详情页都读缓存, 只写库的话下一次请求照旧 403 SUBSCRIPTION_REQUIRED,
	// 要等到重启(InitCache 重读整行)才生效 —— 生产上表现为"探测明明成功了, 重启后又好了"。
	cached, ok := channelCache.Get(channel.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", channel.ID)
	}
	if cached.GeminiProject != "proj-1" {
		t.Fatalf("缓存里 gemini_project = %q, want proj-1(库里有、缓存没有, 转发仍会 403)", cached.GeminiProject)
	}
	// 重启形态: 重建缓存后仍是这个值(库内已落上, 重建自然正确; 这里守住"没有被写回冲掉")。
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	if rebuilt, ok := channelCache.Get(channel.ID); !ok || rebuilt.GeminiProject != "proj-1" {
		t.Fatalf("InitCache 后 gemini_project = %q, want proj-1", rebuilt.GeminiProject)
	}
}

// 用户手填的 project 优先: 自动分配不得覆盖人工配置。
func TestAutoAddKeepsManualGeminiProject(t *testing.T) {
	var quotaHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			fmt.Fprint(w, `{"cloudaicompanionProject":"auto-assigned","currentTier":{"id":"free-tier"}}`)
		case strings.Contains(r.URL.Path, "retrieveUserQuota"):
			quotaHits++
			fmt.Fprint(w, `{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":1}]}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()

	setupAutoAddGlobalDB(t)
	conn := db.GetDB()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		Name:          "订阅渠道-手填project",
		Dialect:       model.DialectSubscription,
		SubPlatform:   "gemini",
		BaseURL:       srv.URL,
		GeminiProject: "manual-project",
		ModelAutoAdd:  true,
	}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name:            "acct-1",
		Key:             `{"access_token":"tok","refresh_token":"rt"}`,
		Enabled:         true,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformAntigravity,
	}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := AutoAddChannelModels(context.Background(), channel.ID, channel.ChannelConfig, srv.Client(), 0); err != nil {
		t.Fatalf("auto add: %v", err)
	}
	var saved model.Channel
	if err := conn.Where("id = ?", channel.ID).First(&saved).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if saved.GeminiProject != "manual-project" {
		t.Fatalf("gemini_project = %q, 人工配置被自动分配覆盖了", saved.GeminiProject)
	}
	if quotaHits == 0 {
		t.Fatal("应使用手填 project 调用 retrieveUserQuota")
	}
}

// 静态 Key 的渠道必须维持原样: 修订阅渠道的令牌来源不能把普通渠道改坏。
func TestAutoAddChannelModelsKeepsPlainKeyForGenericChannel(t *testing.T) {
	const token = "sk-generic-static"
	// 记录本次探测里出现过的所有 Authorization 值: 通用方言会并行探 OpenAI 与 Anthropic 两侧,
	// 只留最后一个会看到空值(某侧可能没带凭据或已被并发覆盖)。
	var mu sync.Mutex
	seen := map[string]struct{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("Authorization")] = struct{}{}
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":401}}`)
	}))
	defer srv.Close()

	setupAutoAddGlobalDB(t)
	conn := db.GetDB()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		Name:         "普通渠道",
		Dialect:      model.DialectGeneric,
		BaseURL:      srv.URL,
		ModelAutoAdd: true,
	}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "k1", Key: token, Enabled: true,
	}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}

	// 上游必然 401, 这里只关心"发出去的令牌没被改造" —— autoadd 会记 warn 并继续。
	_ = AutoAddChannelModels(context.Background(), channel.ID, channel.ChannelConfig, srv.Client(), 0)
	// 必须有某一侧带上了原样透传的 Bearer: 静态 Key 不能被改造成空串或 JSON。
	mu.Lock()
	_, ok := seen["Bearer "+token]
	mu.Unlock()
	if !ok {
		t.Fatalf("静态 Key 没有原样透传, 实际见过: %v", seen)
	}
}
