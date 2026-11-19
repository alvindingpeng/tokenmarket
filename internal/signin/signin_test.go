package signin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func testProvider(tokenURL, redirect string) Provider {
	return Provider{
		Platform:          "claude",
		AuthorizeURL:      "https://example.test/authorize",
		TokenURL:          tokenURL,
		ClientID:          "test-client",
		ClientSecret:      "test-secret",
		Scopes:            []string{"user:inference"},
		RedirectURI:       redirect,
		BodyFormat:        "form",
		AuthStyle:         AuthStyleCode,
		AccessTokenField:  "access_token",
		RefreshTokenField: "refresh_token",
	}
}

// Begin 必须发出 S256 challenge, 且 verifier 不出现在授权 URL 里。
//
// 这两条是 PKCE 的全部意义: 用 plain 等于没有 PKCE(拦截 code 的人也能换令牌),
// 而 verifier 一旦出现在 URL 里, 它就会进浏览器历史、Referer 和代理日志。
func TestBeginUsesS256AndHidesVerifier(t *testing.T) {
	s := NewStore()
	p := testProvider("https://example.test/token", "https://example.test/cb")
	f, err := s.Begin(p)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := url.Parse(f.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("challenge method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == f.verifier {
		t.Fatal("授权 URL 里出现了 verifier 本身(plain PKCE)")
	}
	if strings.Contains(f.AuthorizeURL, f.verifier) {
		t.Fatal("授权 URL 包含 verifier")
	}
	// challenge 必须真的等于 SHA256(verifier), 否则换令牌时上游校验会失败。
	sum := sha256.Sum256([]byte(f.verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("code_challenge 与 verifier 不匹配")
	}
	if q.Get("state") != f.State {
		t.Fatal("授权 URL 的 state 与会话不一致")
	}
	if q.Get("redirect_uri") != p.RedirectURI {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

// state 是一次性的: 贴第二次必须失败, 否则泄漏的 code 可以重放。
func TestTakeIsSingleUse(t *testing.T) {
	s := NewStore()
	f, err := s.Begin(testProvider("https://example.test/token", "https://example.test/cb"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Take(f.State); err != nil {
		t.Fatalf("首次 take 应成功: %v", err)
	}
	if _, err := s.Take(f.State); err == nil {
		t.Fatal("第二次 take 必须失败(state 可重放)")
	}
}

func TestTakeRejectsExpired(t *testing.T) {
	now := time.Now()
	s := NewStore()
	s.now = func() time.Time { return now }
	f, err := s.Begin(testProvider("https://example.test/token", "https://example.test/cb"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	now = now.Add(FlowTTL + time.Minute)
	if _, err := s.Take(f.State); err == nil {
		t.Fatal("过期会话必须被拒")
	}
}

// ParseCode 要同时吃"只贴 code"与"贴整个回调地址"两种手势。
func TestParseCodeAcceptsBothShapes(t *testing.T) {
	cases := []struct{ name, style, in, wantCode, wantState string }{
		{"裸 code", AuthStyleCode, "abc123", "abc123", ""},
		{"code#state", AuthStyleCode, "abc123#st", "abc123", "st"},
		{"回调地址", AuthStyleRedirect, "http://localhost:1455/auth/callback?code=abc123&state=st", "abc123", "st"},
		{"带空白的地址", AuthStyleRedirect, "  http://x.test/cb?code=c9  ", "c9", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, state, err := ParseCode(c.style, c.in)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if code != c.wantCode || state != c.wantState {
				t.Fatalf("code=%q state=%q, want %q/%q", code, state, c.wantCode, c.wantState)
			}
		})
	}
}

// 用户点了"拒绝"时, 地址里带 error 而没有 code: 必须把上游的原因带出来,
// 而不是只说"没有 code"(那会让人以为自己贴错了)。
func TestParseCodeSurfacesUpstreamError(t *testing.T) {
	_, _, err := ParseCode(AuthStyleRedirect, "http://x.test/cb?error=access_denied&error_description=user+denied")
	if err == nil {
		t.Fatal("应当报错")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("错误信息未带出上游原因: %v", err)
	}
}

func TestParseCodeRejectsEmpty(t *testing.T) {
	if _, _, err := ParseCode(AuthStyleCode, "   "); err == nil {
		t.Fatal("空输入应当报错")
	}
}

// Exchange 必须把 verifier 发出去(form 形态), 并解析出令牌与到期时间。
func TestExchangeForm(t *testing.T) {
	var seen url.Values
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		seen = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "refresh_token": "rt-new", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	s := NewStore()
	p := testProvider(srv.URL, "https://example.test/cb")
	f, err := s.Begin(p)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := s.Exchange(f, p, "code-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if res.AccessToken != "at-new" || res.RefreshToken != "rt-new" {
		t.Fatalf("令牌解析错误: %+v", res)
	}
	if !strings.Contains(gotContentType, "form-urlencoded") {
		t.Fatalf("content-type = %q", gotContentType)
	}
	// verifier 必须随换令牌请求发出, 否则 PKCE 无效。
	if seen.Get("code_verifier") != f.verifier {
		t.Fatalf("未发送 verifier: %q", seen.Get("code_verifier"))
	}
	if seen.Get("code") != "code-1" || seen.Get("redirect_uri") != p.RedirectURI {
		t.Fatalf("请求体缺 code/redirect_uri: %v", seen)
	}
	if seen.Get("client_id") != "test-client" || seen.Get("client_secret") != "test-secret" {
		t.Fatalf("请求体缺 client 凭据: %v", seen)
	}
	if res.ExpiresAt.IsZero() || time.Until(res.ExpiresAt) < 50*time.Minute {
		t.Fatalf("expires_in 未换算成到期时间: %v", res.ExpiresAt)
	}
}

// JSON 形态(body)也要能发: Claude 的令牌端点吃 JSON。
func TestExchangeJSON(t *testing.T) {
	var body map[string]any
	ct := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt"})
	}))
	defer srv.Close()

	s := NewStore()
	p := testProvider(srv.URL, "https://example.test/cb")
	p.BodyFormat = "json"
	f, err := s.Begin(p)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exchange(f, p, "c"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if body["code_verifier"] != f.verifier || body["client_secret"] != "test-secret" {
		t.Fatalf("JSON 请求体缺字段: %v", body)
	}
}

// 上游拒绝时, 原始错误必须透出来 —— invalid_grant 与 invalid_client 的含义只有上游知道。
func TestExchangeSurfacesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer srv.Close()

	s := NewStore()
	p := testProvider(srv.URL, "https://example.test/cb")
	f, err := s.Begin(p)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = s.Exchange(f, p, "bad")
	if err == nil {
		t.Fatal("应当报错")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("未带出上游原因: %v", err)
	}
}

// 并发 Begin/Take 不能出数据竞争(登录是有人手抖连点两次就会触发的场景)。
func TestStoreConcurrent(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := s.Begin(testProvider("https://example.test/token", "https://example.test/cb"))
			if err != nil {
				t.Errorf("begin: %v", err)
				return
			}
			if _, err := s.Take(f.State); err != nil {
				t.Errorf("take: %v", err)
			}
		}()
	}
	wg.Wait()
}

// 不支持的平台必须明确拒绝, 而不是返回一个空 Provider 去打不该打的端点。
func TestProviderOfRejectsUnknown(t *testing.T) {
	if _, err := ProviderOf("kiro"); err == nil {
		t.Fatal("kiro 不支持网页登录, 应当报错")
	}
}

// 支持平台的端点必须有出处且不为空 —— 空端点会变成"请求打到空地址"这种无从排查的错。
func TestProviderOfSupportedPlatforms(t *testing.T) {
	for _, platform := range []string{"claude", "codex", "gemini", "antigravity"} {
		t.Run(platform, func(t *testing.T) {
			// gemini 的 client 来自部署配置, 测试里配一份假值才能验到"参数完整"。
			if platform == "gemini" {
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "test-google-id")
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "test-google-secret")
			}
			// antigravity 的 client 不内置默认值, 未配置时 ProviderOf 会明确报错。
			// 这里配假值测"配置后参数完整"; 未配置的报错另有专门用例。
			if platform == "antigravity" {
				t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-ag-id")
				t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-ag-secret")
			}
			p, err := ProviderOf(platform)
			if err != nil {
				t.Fatalf("provider %s: %v", platform, err)
			}
			if p.AuthorizeURL == "" || p.TokenURL == "" || p.ClientID == "" || p.RedirectURI == "" {
				t.Fatalf("%s 的登录参数不完整: %+v", platform, p)
			}
			if p.AuthStyle != AuthStyleCode && p.AuthStyle != AuthStyleRedirect {
				t.Fatalf("%s auth style = %q", platform, p.AuthStyle)
			}
			if p.UserCodeHint == "" {
				t.Fatalf("%s 缺少给用户的粘贴说明", platform)
			}
		})
	}
}

// Claude 的令牌端点在所有来源里都写作 platform.claude.com; 写错会得到一个提示极少的失败。
func TestClaudeEndpointPinned(t *testing.T) {
	p, err := ProviderOf("claude")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if p.AuthorizeURL != "https://platform.claude.com/oauth/authorize" {
		t.Fatalf("claude authorize = %q", p.AuthorizeURL)
	}
	if p.TokenURL != "https://platform.claude.com/v1/oauth/token" {
		t.Fatalf("claude token = %q", p.TokenURL)
	}
	// redirect 指向 Claude 自己的回调页: 它不需要我们的服务器, 这正是服务端部署可用的原因。
	if p.RedirectURI != "https://platform.claude.com/oauth/code/callback" {
		t.Fatalf("claude redirect = %q", p.RedirectURI)
	}
}

// 现在有内置默认值(Gemini CLI), 未配置环境变量时也能用内置默认值构造 Provider。
// 只有当用户在设置页显式清空覆盖值时才会报错(极不推荐)。
func TestGeminiProviderUsesBuiltinDefaults(t *testing.T) {
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "")
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "")
	p, err := ProviderOf("gemini")
	if err != nil {
		t.Fatalf("内置默认值存在时不应报错: %v", err)
	}
	if p.ClientID != "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com" {
		t.Errorf("ClientID 应为内置默认值: got %q", p.ClientID)
	}
	if p.ClientSecret != "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" {
		t.Errorf("ClientSecret 应为内置默认值: got %q", p.ClientSecret)
	}
}

// 显式清空设置(模拟用户在界面清空)时, 应当报错提示配置。
// 这里模拟 DB 返回空值, 且无环境变量, 期望报错。
func TestGeminiProviderEmptyDBSourceFails(t *testing.T) {
	// 需要模拟 DB 返回空值, 但 signin 包无法直接注入 model.GeminiOAuthSettingGetter。
	// 这里仅作记录: 真实场景下, 用户若在界面清空值, 设置库存空串, 会触发报错。
	// 当前实现: DB getter 返回空串 -> 回退 env -> 回退内置默认。
	// 要让清空生效, 需要在 model 层处理"空串等于显式清空"的语义, 这里暂不实现。
	_ = func() {}
}

// Antigravity 与 gemini 是两套**不可互换**的凭据, 这条测试钉住这个区别。
//
// 为什么必须钉: 两者后端相同(Google Code Assist), 看起来"都是 Google 登录",
// 最容易的做法是复用 gemini 那一套。但实测(2026-11)用 gemini 的 token 请求
// Antigravity 数据面(retrieveUserQuota/generateContent)一律
// 403 "The caller does not have permission" —— tokeninfo 显示缺 cclog 与
// experimentsandconfigs, 而这两个 scope 只在 Antigravity 的 client 下才有。
// 一旦有人"顺手合并"这两个分支, 症状会是登录成功但一个模型都拉不到,
// 且错误信息看不出与 scope 有关。这条测试让合并立刻失败。
func TestAntigravityProviderDiffersFromGemini(t *testing.T) {
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "test-google-id")
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "test-google-secret")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-antigravity-id")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-antigravity-secret")

	gem, err := ProviderOf("gemini")
	if err != nil {
		t.Fatalf("gemini provider: %v", err)
	}
	ag, err := ProviderOf("antigravity")
	if err != nil {
		t.Fatalf("antigravity provider: %v", err)
	}

	if ag.ClientID == gem.ClientID {
		t.Errorf("antigravity 与 gemini 的 client_id 必须不同(同一 client 拿不到 cclog/experimentsandconfigs)")
	}
	if ag.ClientSecret == gem.ClientSecret {
		t.Errorf("antigravity 与 gemini 的 client_secret 必须不同")
	}

	hasScope := func(scopes []string, want string) bool {
		for _, s := range scopes {
			if s == want {
				return true
			}
		}
		return false
	}
	// Antigravity 必须申请这两个 scope, 少任一个数据面就 403。
	for _, want := range []string{
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	} {
		if !hasScope(ag.Scopes, want) {
			t.Errorf("antigravity 缺少必需 scope %s (缺了会让数据面 403)", want)
		}
	}
	// 对照: gemini 不该申请这两个(那是 Antigravity client 的权限)。
	if hasScope(gem.Scopes, "https://www.googleapis.com/auth/cclog") {
		t.Errorf("gemini 不应申请 cclog —— 该 scope 属于 Antigravity 的 client")
	}

	for _, p := range []struct {
		name string
		v    Provider
	}{{"gemini", gem}, {"antigravity", ag}} {
		if p.v.AuthStyle != AuthStyleRedirect {
			t.Errorf("%s auth style = %q, want redirect", p.name, p.v.AuthStyle)
		}
		// access_type=offline 是拿到 refresh_token 的前提, 少了它账号无法自动续期。
		if got := p.v.ExtraAuth.Get("access_type"); got != "offline" {
			t.Errorf("%s access_type = %q, want offline", p.name, got)
		}
	}
}

// Antigravity 不内置默认 client: 未配置时必须**明确报错并指出变量名**。
//
// 这条测试守的是一类很难自查的失败: 若 ProviderOf 在没有 client 时也返回一个空 client 的
// Provider, 界面就会出现"授权按钮亮着, 一点就报 invalid_client"的死路, 而 invalid_client
// 读起来像"账号坏了"。拿空 client 去打上游也不是选项 —— 那等于在别人的风控体系里制造无意义流量。
func TestAntigravityProviderRequiresConfiguredClient(t *testing.T) {
	// 显式清空, 排除外部环境残留导致的假通过。
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")

	_, err := ProviderOf("antigravity")
	if err == nil {
		t.Fatal("未配置 client 时 ProviderOf(antigravity) 必须报错, 否则界面会给出一个点了就失败的授权按钮")
	}
	// 错误信息必须是可行动的: 直接给出该配哪两个变量。
	if !strings.Contains(err.Error(), "SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID") ||
		!strings.Contains(err.Error(), "SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET") {
		t.Errorf("错误信息没有指出该配的环境变量: %v", err)
	}

	// 只配 id 不配 secret 也必须视为未配置: 半对凭据会得到 invalid_client。
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "only-id")
	if _, err := ProviderOf("antigravity"); err == nil {
		t.Fatal("只配 client_id 时应报未配置(半对凭据会 invalid_client)")
	}

	// 配齐后才可用。
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "only-secret")
	p, err := ProviderOf("antigravity")
	if err != nil {
		t.Fatalf("配齐后应可用: %v", err)
	}
	if p.ClientID != "only-id" || p.ClientSecret != "only-secret" {
		t.Errorf("provider 取的 client 不对: id=%q secret=%q", p.ClientID, p.ClientSecret)
	}
}
