package model

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// 解析器守的是什么: 字段名跨平台不一致(Codex snake_case / Kiro camelCase / Cookie 类无 token),
// 而解析错了不会炸 —— 只会静默读到空字段, 然后在转发时以 401 的形式暴露。
// 所以这里逐条钉住"读对字段"和"缺字段要报清楚"。

const accountCodexFlat = `{"access_token":"a.b.c","refresh_token":"rt-1","account_id":"acct-9","email":"u@example.com","type":"codex"}`

const accountCodexWrapped = `{"OPENAI_API_KEY":null,"tokens":{"access_token":"a.b.c","refresh_token":"rt-2","account_id":"acct-9"},"last_refresh":"2026-11-13T22:00:00Z"}`

func TestAccountJSONParseCodexFlat(t *testing.T) {
	credential, err := AccountJSONParse(AccountPlatformCodex, []byte(accountCodexFlat))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if credential.Kind != AccountKindOAuth {
		t.Fatalf("kind = %q, want oauth", credential.Kind)
	}
	if credential.RefreshToken != "rt-1" {
		t.Fatalf("refresh = %q", credential.RefreshToken)
	}
	if credential.AccountID != "acct-9" {
		t.Fatalf("account = %q", credential.AccountID)
	}
	if credential.Email != "u@example.com" {
		t.Fatalf("email = %q", credential.Email)
	}
}

// CLI 导出的 tokens 封装必须能解包: 用户手上的文件多半是这一种, 不支持就等于"贴上来就报错"。
func TestAccountJSONParseCodexWrapped(t *testing.T) {
	credential, err := AccountJSONParse(AccountPlatformCodex, []byte(accountCodexWrapped))
	if err != nil {
		t.Fatalf("parse wrapped: %v", err)
	}
	if credential.RefreshToken != "rt-2" {
		t.Fatalf("refresh = %q, want rt-2 (应从 tokens 层读出)", credential.RefreshToken)
	}
	if credential.AccessToken != "a.b.c" {
		t.Fatalf("access = %q", credential.AccessToken)
	}
}

// 缺 refresh_token 与缺 access_token 是两件事, 报错必须能区分 —— 前者意味着这个账号无法自动续期。
func TestAccountJSONParseCodexMissingTokensReportedDistinctly(t *testing.T) {
	if _, err := AccountJSONParse(AccountPlatformCodex, []byte(`{"refresh_token":"rt"}`)); err == nil {
		t.Fatal("缺少 access_token 时应当报错")
	} else if !containsText(err.Error(), "access_token") {
		t.Fatalf("错误应指明 access_token 缺失, got %v", err)
	}

	if _, err := AccountJSONParse(AccountPlatformCodex, []byte(`{"access_token":"a.b.c"}`)); err == nil {
		t.Fatal("缺少 refresh_token 时应当报错")
	} else if !containsText(err.Error(), "无法自动续期") {
		t.Fatalf("错误应说明无法自动续期, got %v", err)
	}
}

// Kiro 用 camelCase。用喂错平台的方式来钉住"不做字段名归一化"这个决定:
// 给它 Codex 的 snake_case 必须失败, 而不是静默读到空值。
func TestAccountJSONParseKiroUsesCamelCase(t *testing.T) {
	raw := []byte(`{"clientId":"cid","clientSecret":"secret","refreshToken":"rt-kiro"}`)
	credential, err := AccountJSONParse(AccountPlatformKiro, raw)
	if err != nil {
		t.Fatalf("parse kiro: %v", err)
	}
	if credential.RefreshToken != "rt-kiro" || credential.ClientID != "cid" || credential.ClientSecret != "secret" {
		t.Fatalf("kiro 字段读取错误: %+v", credential)
	}

	if _, err := AccountJSONParse(AccountPlatformKiro, []byte(`{"refresh_token":"rt"}`)); err == nil {
		t.Fatal("Kiro 不认 snake_case, 应当报错")
	}
}

// 纯 Cookie 平台没有 refresh_token: 这是协议的既有事实, 不是缺陷 ——
// 解析层必须把 Kind 标成 cookie, 上层的定时任务据此改用"重放验活"而不是刷新。
//
// gemini 不在这一组里: 它同时有 Cookie 与 OAuth 两种形状(见 TestAccountJSONParseGeminiOAuth)。
func TestAccountJSONParseCookiePlatformsHaveNoRefresh(t *testing.T) {
	credential, err := AccountJSONParse(AccountPlatformGemini, []byte(`{"c_ses":"ses-value","csesidx":"idx"}`))
	if err != nil {
		t.Fatalf("parse gemini cookie: %v", err)
	}
	if credential.Kind != AccountKindCookie {
		t.Fatalf("gemini(cookie 形状) kind = %q, want cookie", credential.Kind)
	}
	if credential.RefreshToken != "" {
		t.Fatalf("gemini(cookie 形状) 不应有 refresh_token, got %q", credential.RefreshToken)
	}
	if credential.Cookies["c_ses"] != "ses-value" {
		t.Fatalf("c_ses = %q", credential.Cookies["c_ses"])
	}

	credential, err = AccountJSONParse(AccountPlatformGrok, []byte(`{"sso":"sso-value","email":"u@example.com"}`))
	if err != nil {
		t.Fatalf("parse grok: %v", err)
	}
	if credential.Kind != AccountKindCookie || credential.Cookies["sso"] != "sso-value" {
		t.Fatalf("grok 解析错误: %+v", credential)
	}
	// sso-rw 是孪生键, 不能当成鉴权凭据。
	if _, err := AccountJSONParse(AccountPlatformGrok, []byte(`{"sso-rw":"x"}`)); err == nil {
		t.Fatal("grok 缺少 sso 时应报错")
	}
}

func TestAccountJSONParseRejectsGarbage(t *testing.T) {
	if _, err := AccountJSONParse(AccountPlatformCodex, []byte("not json")); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if _, err := AccountJSONParse("unknown-platform", []byte("{}")); err == nil {
		t.Fatal("未知平台应报错")
	}
}

// 到期时间的两个来源(显式字段与 JWT exp)都要认; 解析出来的时间决定定时任务何时刷新。
func TestAccountJSONExpirySources(t *testing.T) {
	// 显式字段: 三种真实格式都要认, 认不出就会被当成"永不到期", 永远不刷新。
	for _, text := range []string{
		"2030-01-02T03:04:05Z",
		"2030-01-02T03:04:05+00:00",
		"2030-01-02 03:04:05",
	} {
		raw := []byte(`{"access_token":"tok","refresh_token":"rt","expired":"` + text + `"}`)
		credential, err := AccountJSONParse(AccountPlatformCodex, raw)
		if err != nil {
			t.Fatalf("parse expired=%q: %v", text, err)
		}
		if !credential.ExpiresAt.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) {
			t.Fatalf("expired=%q 解析成 %v", text, credential.ExpiresAt)
		}
	}

	// JWT exp: 构造一个 payload 只含 exp 的假 JWT。
	const expSeconds = 1900000000
	token := "eyJhbGciOiJub25lIn0." + base64URLOfJSON(t, `{"exp":1900000000}`) + ".sig"
	credential, err := AccountJSONParse(AccountPlatformCodex, []byte(`{"access_token":"`+token+`","refresh_token":"rt"}`))
	if err != nil {
		t.Fatalf("parse jwt exp: %v", err)
	}
	if credential.ExpiresAt.Unix() != expSeconds {
		t.Fatalf("JWT exp 解析成 %v", credential.ExpiresAt)
	}
}

// NeedsRefresh 用提前量判断, 边界必须精确: 差一秒决定是否多打一次上游 401。
func TestAccountCredentialNeedsRefreshBoundary(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	lead := 5 * time.Minute

	cases := []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"已过期", now.Add(-time.Minute), true},
		{"恰好提前量边界", now.Add(lead), true},
		{"超出提前量一秒", now.Add(lead + time.Second), false},
		{"到期未知", time.Time{}, false},
	}
	for _, testCase := range cases {
		credential := AccountCredential{ExpiresAt: testCase.expires}
		if got := AccountCredentialNeedsRefresh(credential, now, lead); got != testCase.want {
			t.Fatalf("%s: got %v want %v", testCase.name, got, testCase.want)
		}
	}
}

// containsText 判断错误文本是否包含某段说明。
func containsText(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// base64URLOfJSON 造一个 base64url(无填充) 编码的 JSON 片段, 用来拼假 JWT。
func base64URLOfJSON(t *testing.T, body string) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(body))
}

// Claude Code 的凭据文件把 OAuth 凭据放在 claudeAiOauth 下, 且字段是 camelCase:
//   {"claudeAiOauth":{"accessToken":..,"refreshToken":..,"expiresAt":<毫秒>,...}}
// 早先 Claude 复用了 Codex 的解析分支(只认顶层 snake_case), 结果是从真实凭据文件里
// 读不到任何令牌 —— 而导入是"成功"的, 转发时才以空 Bearer 全部 401 暴露。

const claudeNestedCredentials = `{"claudeAiOauth":{"accessToken":"sk-ant-oat-1","refreshToken":"rt-claude","expiresAt":1900000000000,"scopes":["user:inference"]}}`

func TestAccountJSONParseClaudeNestedCredentials(t *testing.T) {
	credential, err := AccountJSONParse(AccountPlatformClaude, []byte(claudeNestedCredentials))
	if err != nil {
		t.Fatalf("parse claude: %v", err)
	}
	if credential.Kind != AccountKindOAuth {
		t.Fatalf("claude kind = %q, want oauth", credential.Kind)
	}
	if credential.AccessToken != "sk-ant-oat-1" {
		t.Fatalf("access = %q (应从 claudeAiOauth.accessToken 读出)", credential.AccessToken)
	}
	if credential.RefreshToken != "rt-claude" {
		t.Fatalf("refresh = %q", credential.RefreshToken)
	}
	// expiresAt 是毫秒时间戳, 不是 RFC3339 字符串。按字符串解析会永远失败,
	// 于是到期时间恒为零值、定时任务永不刷新 —— 账号到期即静默失效。
	want := time.Unix(0, 1900000000*int64(time.Second)).UTC()
	if !credential.ExpiresAt.Equal(want) {
		t.Fatalf("expires = %v, want %v (毫秒时间戳)", credential.ExpiresAt, want)
	}
}

// 也容忍顶层 snake_case(令牌端点响应的形状), 因为有人直接导出的就是那个。
func TestAccountJSONParseClaudeFlatShape(t *testing.T) {
	raw := []byte(`{"access_token":"flat-1","refresh_token":"rt-flat","email":"u@example.com"}`)
	credential, err := AccountJSONParse(AccountPlatformClaude, []byte(raw))
	if err != nil {
		t.Fatalf("parse claude flat: %v", err)
	}
	if credential.AccessToken != "flat-1" || credential.RefreshToken != "rt-flat" {
		t.Fatalf("flat shape 读错: %+v", credential)
	}
	if credential.Email != "u@example.com" {
		t.Fatalf("email = %q", credential.Email)
	}
}

// Claude 缺令牌时的报错要指到真正的位置(claudeAiOauth.accessToken),
// 否则使用者会去翻一个不存在的顶层字段。
func TestAccountJSONParseClaudeMissingTokenPointsAtNestedPath(t *testing.T) {
	_, err := AccountJSONParse(AccountPlatformClaude, []byte(`{"claudeAiOauth":{"refreshToken":"rt"}}`))
	if err == nil {
		t.Fatal("缺 accessToken 应当报错")
	}
	if !containsText(err.Error(), "claudeAiOauth.accessToken") {
		t.Fatalf("错误应指明 claudeAiOauth.accessToken, got %v", err)
	}
}

// Kiro 的 accessToken 曾经漏读 —— 后果是转发拿到空 Bearer 全部 401 且无任何报错。
// 这条测试就是为那个缺陷留的。
func TestAccountJSONParseKiroReadsAccessToken(t *testing.T) {
	raw := []byte(`{"accessToken":"kiro-at","refreshToken":"rt","clientId":"cid","clientSecret":"sec"}`)
	credential, err := AccountJSONParse(AccountPlatformKiro, raw)
	if err != nil {
		t.Fatalf("parse kiro: %v", err)
	}
	if credential.AccessToken != "kiro-at" {
		t.Fatalf("kiro accessToken = %q, want kiro-at", credential.AccessToken)
	}
}

// Gemini 有两种凭据形状, 必须按内容分流而不是按平台名定死。
//
// 这类缺陷的特征: 分流判错时不会"崩", 只会给出一个指向错误方向的报错 ——
// CLI 用户贴了合法的 oauth_creds.json, 却被告知"缺少 c_ses"。
func TestAccountJSONParseGeminiOAuth(t *testing.T) {
	// 形状取自 Gemini CLI 官方发行包(0.68.0)的凭据处理代码:
	//   {access_token, refresh_token, expiry_date}, expiry_date 是毫秒时间戳。
	// 注意它**不是** CLIENT_ID/CLIENT_SECRET 自带的: client_secret 是全局常量,
	// 因此解析层不该要求文件里有它(要求了就等于把 CLI 用户的合法文件判成非法)。
	raw := `{"access_token":"at-value","refresh_token":"rt-value","expiry_date":1900000000000,"scope":"openid"}`
	credential, err := AccountJSONParse(AccountPlatformGemini, []byte(raw))
	if err != nil {
		t.Fatalf("parse gemini oauth: %v", err)
	}
	if credential.Kind != AccountKindOAuth {
		t.Fatalf("gemini(oauth 形状) kind = %q, want oauth", credential.Kind)
	}
	if credential.AccessToken != "at-value" || credential.RefreshToken != "rt-value" {
		t.Fatalf("令牌读取错误: %+v", credential)
	}

	// expiry_date 必须被读成毫秒时间戳。
	// 读不出来的后果是到期时间恒为零 -> 定时任务永不刷新 -> 账号到期即静默失效,
	// 而这中间不会有任何一步报错。
	want := time.Unix(0, 1900000000000*int64(time.Millisecond)).UTC()
	if !credential.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", credential.ExpiresAt, want)
	}

	// 有 refresh_token 却缺 access_token: 这是残缺文件, 要明说缺的是哪一个。
	if _, err := AccountJSONParse(AccountPlatformGemini, []byte(`{"refresh_token":"rt"}`)); err == nil {
		t.Fatal("gemini 有 refresh_token 但缺 access_token 应当报错")
	}

	// Cookie 形状仍必须走 cookie 分支(上面的用例已覆盖), 这里再钉一次反向:
	// 有 c_ses 但无 refresh_token 时, kind 绝不能是 oauth。
	cookieOnly, err := AccountJSONParse(AccountPlatformGemini, []byte(`{"c_ses":"ses"}`))
	if err != nil {
		t.Fatalf("parse gemini cookie: %v", err)
	}
	if cookieOnly.Kind != AccountKindCookie {
		t.Fatalf("gemini(仅 c_ses) kind = %q, want cookie", cookieOnly.Kind)
	}
}
