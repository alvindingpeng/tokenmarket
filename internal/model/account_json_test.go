
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

// Cookie 类平台没有 refresh_token: 这是协议的既有事实, 不是缺陷 ——
// 解析层必须把 Kind 标成 cookie, 上层的定时任务据此改用"重放验活"而不是刷新。
func TestAccountJSONParseCookiePlatformsHaveNoRefresh(t *testing.T) {
	credential, err := AccountJSONParse(AccountPlatformGemini, []byte(`{"c_ses":"ses-value","csesidx":"idx"}`))
	if err != nil {
		t.Fatalf("parse gemini: %v", err)
	}
	if credential.Kind != AccountKindCookie {
		t.Fatalf("gemini kind = %q, want cookie", credential.Kind)
	}
	if credential.RefreshToken != "" {
		t.Fatalf("gemini 不应有 refresh_token, got %q", credential.RefreshToken)
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
