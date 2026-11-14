package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 这一层守的是"刷新一次不会把账号刷废"。
//
// 上游每次刷新通常都会轮换 refresh_token, 旧值当场失效。若解析/回写有偏差,
// 账号会永久失效, 而表面症状只是"下一次刷新失败", 极易被误判成账号被封。

const codexRefreshOK = `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600}`

func TestAccountOAuthParseRefreshResponseCodex(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformCodex)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	previous := AccountCredential{Platform: AccountPlatformCodex, RefreshToken: "old-refresh"}

	result, err := AccountOAuthParseRefreshResponse(spec, previous, []byte(codexRefreshOK), now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.AccessToken != "new-access" {
		t.Fatalf("access = %q", result.AccessToken)
	}
	// 关键断言: 必须拿到轮换后的新值, 而不是沿用旧值。
	if result.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh = %q, want rotated-refresh", result.RefreshToken)
	}
	if !result.RefreshTokenRotated {
		t.Fatal("应当标记 refresh_token 已轮换")
	}
	if want := now.Add(time.Hour); !result.ExpiresAt.Equal(want) {
		t.Fatalf("expires = %v want %v", result.ExpiresAt, want)
	}
}

// 上游未轮换 refresh_token 是合法情况, 不能当成错误, 也不能把令牌清空。
func TestAccountOAuthParseRefreshResponseWithoutRotation(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	previous := AccountCredential{Platform: AccountPlatformCodex, RefreshToken: "keep-me"}
	raw := []byte(`{"access_token":"new-access","expires_in":60}`)

	result, err := AccountOAuthParseRefreshResponse(spec, previous, raw, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.RefreshToken != "keep-me" {
		t.Fatalf("refresh = %q, want keep-me", result.RefreshToken)
	}
	if result.RefreshTokenRotated {
		t.Fatal("未轮换时不应标记 rotated")
	}
}

// Kiro 的响应字段是 camelCase。用 Codex 的字段名去读会得到空值 ——
// 这里钉住"按平台规格读字段", 而不是写死一套键名。
func TestAccountOAuthParseRefreshResponseKiroCamelCase(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformKiro)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw := []byte(`{"accessToken":"kiro-access","refreshToken":"kiro-rotated","expiresIn":1800}`)
	result, err := AccountOAuthParseRefreshResponse(spec, AccountCredential{RefreshToken: "kiro-old"}, raw, time.Now())
	if err != nil {
		t.Fatalf("parse kiro: %v", err)
	}
	if result.AccessToken != "kiro-access" || result.RefreshToken != "kiro-rotated" {
		t.Fatalf("kiro 字段读错: %+v", result)
	}

	// 反向: 给 Kiro 喂 Codex 的 snake_case 响应必须失败, 证明字段名没有归一化。
	if _, err := AccountOAuthParseRefreshResponse(spec, AccountCredential{RefreshToken: "x"}, []byte(`{"access_token":"a","refresh_token":"b"}`), time.Now()); err == nil {
		t.Fatal("Kiro 不认 snake_case 响应, 应当报错")
	}
}

// 刷新请求的形态也各不相同: Codex 是 form, Kiro 是 JSON 且必带 clientId/clientSecret。
func TestAccountOAuthRefreshRequestShapes(t *testing.T) {
	codexSpec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	contentType, body, err := AccountOAuthRefreshRequest(codexSpec, AccountCredential{RefreshToken: "rt-1"})
	if err != nil {
		t.Fatalf("codex request: %v", err)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("codex content-type = %q", contentType)
	}
	for _, want := range []string{"grant_type=refresh_token", "refresh_token=rt-1", "client_id="} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("codex form 缺少 %q: %s", want, body)
		}
	}

	kiroSpec, _ := AccountOAuthSpecOf(AccountPlatformKiro)
	contentType, body, err = AccountOAuthRefreshRequest(kiroSpec, AccountCredential{
		RefreshToken: "rt-2", ClientID: "cid", ClientSecret: "sec",
	})
	if err != nil {
		t.Fatalf("kiro request: %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("kiro content-type = %q", contentType)
	}
	var fields map[string]string
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("kiro body 不是 JSON: %v", err)
	}
	// Kiro 的请求体全是 camelCase —— 连 grant_type 都写成 grantType, 续期令牌键是 refreshToken。
	// 这几条必须全断: 抗的是"把 Codex 的 snake_case 套到 Kiro 上"这个极易发生的照抄错误。
	// 写错不会报编译错, 只会换回一个 unsupported_grant_type, 看起来像账号坏了。
	if fields["clientId"] != "cid" || fields["clientSecret"] != "sec" {
		t.Fatalf("kiro 请求体缺少 clientId/clientSecret: %v", fields)
	}
	if fields["grantType"] != "refresh_token" {
		t.Fatalf("kiro 的 grant_type 必须是 camelCase 的 grantType, got %v", fields)
	}
	if fields["refreshToken"] != "rt-2" {
		t.Fatalf("kiro 的续期令牌键必须是 refreshToken, got %v", fields)
	}
	// 反向: 出现 snake_case 的 grant_type/refresh_token 就说明规格被写混了。
	if fields["grant_type"] != "" || fields["refresh_token"] != "" {
		t.Fatalf("kiro 请求体不应含 snake_case 键: %v", fields)
	}

	// 缺 clientSecret 必须当场失败: 拿空值去换令牌只会得到一个 401,
	// 而错误信息会指向"账号失效", 把排查引向错的方向。
	if _, _, err := AccountOAuthRefreshRequest(kiroSpec, AccountCredential{RefreshToken: "rt", ClientID: "cid"}); err == nil {
		t.Fatal("kiro 缺 clientSecret 应当报错")
	}
}

// 空 refresh_token 不能发请求; 未登记的平台不能猜端点。
func TestAccountOAuthGuards(t *testing.T) {
	codexSpec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	if _, _, err := AccountOAuthRefreshRequest(codexSpec, AccountCredential{RefreshToken: "  "}); err == nil {
		t.Fatal("空 refresh_token 应当报错")
	}
	if _, err := AccountOAuthSpecOf("unknown"); err == nil {
		t.Fatal("未登记平台应当报错")
	}
	// Cookie 类平台没有 OAuth 规格 —— 这是正确行为: 它们根本不该走刷新。
	if _, err := AccountOAuthSpecOf(AccountPlatformGemini); err == nil {
		t.Fatal("gemini 不应有 OAuth 刷新规格")
	}
}

// Claude 的刷新规格来自官方发行包(2.1.325)里提取的常量, 不是猜的:
// TOKEN_URL = https://platform.claude.com/v1/oauth/token
// CLIENT_ID = 9d1c250a-e61b-44d9-88ed-5944d1962f5e
// 这份断言的作用是"值一旦被改动就立刻显形" —— 这些常量错了不会报错, 只会换不到令牌。
func TestAccountOAuthClaudeSpecMatchesOfficialClient(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformClaude)
	if err != nil {
		t.Fatalf("claude spec: %v", err)
	}
	if spec.TokenURL != "https://platform.claude.com/v1/oauth/token" {
		t.Fatalf("claude token url = %q", spec.TokenURL)
	}
	if spec.ClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Fatalf("claude client_id = %q", spec.ClientID)
	}
	if spec.BodyFormat != accountBodyFormatJSON {
		t.Fatalf("claude body format = %q, want json", spec.BodyFormat)
	}
	// 响应是 snake_case。
	if spec.AccessTokenField != "access_token" || spec.RefreshTokenField != "refresh_token" {
		t.Fatalf("claude 响应字段应为 snake_case: %+v", spec)
	}
}

// Claude 的刷新请求必须带 scope: 漏发会拿到一个没有推理权限的令牌,
// 表现为"刷新成功但调用 403", 比刷新失败更难查。
func TestAccountOAuthClaudeRefreshRequestCarriesScope(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformClaude)
	contentType, body, err := AccountOAuthRefreshRequest(spec, AccountCredential{RefreshToken: "rt-claude"})
	if err != nil {
		t.Fatalf("claude request: %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("claude content-type = %q", contentType)
	}
	var fields map[string]string
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("claude body 不是 JSON: %v", err)
	}
	if fields["grant_type"] != "refresh_token" {
		t.Fatalf("claude 应为 snake_case grant_type: %v", fields)
	}
	if fields["refresh_token"] != "rt-claude" {
		t.Fatalf("claude 续期令牌字段错误: %v", fields)
	}
	if fields["client_id"] == "" {
		t.Fatalf("claude 请求必须带 client_id: %v", fields)
	}
	if fields["scope"] == "" {
		t.Fatalf("claude 请求必须带 scope, 否则新令牌缺推理权限: %v", fields)
	}
	if fields["scope"] != "user:inference user:profile" {
		t.Fatalf("claude scope = %q", fields["scope"])
	}
}

// Kiro 的 AWS OIDC 端点要求 SDK 系列头。少了它请求会被拒且响应不说明原因,
// 所以这里断"头确实带上了", 而不是只断请求体。
func TestAccountOAuthKiroCarriesSDKHeaders(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformKiro)
	headers := AccountOAuthHeaders(spec)
	if headers["User-Agent"] == "" {
		t.Fatal("kiro 缺少 User-Agent 头")
	}
	if headers["x-amz-user-agent"] == "" {
		t.Fatal("kiro 缺少 x-amz-user-agent 头")
	}
	// 返回的必须是副本: 调用方设置 header 时不该改动全局规格表。
	headers["User-Agent"] = "mutated"
	if again := AccountOAuthHeaders(spec); again["User-Agent"] == "mutated" {
		t.Fatal("AccountOAuthHeaders 返回了共享 map, 全局规格被污染")
	}
	// Codex/Claude 不需要这些头 —— 反过来套上去只会让请求带一串无关的 SDK 标识。
	if codexSpec, _ := AccountOAuthSpecOf(AccountPlatformCodex); len(AccountOAuthHeaders(codexSpec)) != 0 {
		t.Fatal("codex 不应带 AWS SDK 头")
	}
}

// 回写必须写到"解析层读的那一层", 否则令牌换了但自己读不回来 —— 账号静默失效。
// 这三条往返测试是这一层唯一的护栏: 覆盖 顶层 / tokens 封装 / claudeAiOauth 封装。

func TestAccountOAuthStoreFieldsRoundTripClaude(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformClaude)
	accessField, refreshField, container := AccountOAuthStoreFields(spec)
	if accessField != "accessToken" || refreshField != "refreshToken" {
		t.Fatalf("claude 存储键名应为 camelCase, got %s/%s", accessField, refreshField)
	}
	if container != "claudeAiOauth" {
		t.Fatalf("claude 存储层 = %q", container)
	}
}

// Kiro 的存储键名也必须显式是 camelCase —— 若有人把它"统一成 snake_case",
// 下一次刷新读不到 refreshToken, 账号就静默失效。
func TestAccountOAuthStoreFieldsRoundTripKiro(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformKiro)
	accessField, refreshField, _ := AccountOAuthStoreFields(spec)
	if accessField != "accessToken" || refreshField != "refreshToken" {
		t.Fatalf("kiro 存储键名 = %s/%s", accessField, refreshField)
	}
}

// Codex 没有 StoreXxx 声明, 应回退到响应字段名(snake_case)。
func TestAccountOAuthStoreFieldsFallBackToResponseNames(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	accessField, refreshField, container := AccountOAuthStoreFields(spec)
	if accessField != "access_token" || refreshField != "refresh_token" {
		t.Fatalf("codex 回退字段 = %s/%s", accessField, refreshField)
	}
	if container != "" {
		t.Fatalf("codex 不应有存储容器, got %q", container)
	}
}
