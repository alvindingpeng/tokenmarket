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
			fmt.Fprint(w, `{"quota":{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.9}]}}`)
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
