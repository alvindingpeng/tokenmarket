package model

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	// grok 是纯 Cookie 平台, 没有 OAuth 规格 —— 这是正确行为: 它根本不该走刷新。
	//
	// 这里原本断言的是 gemini, 后来 G1 给 gemini 补了 OAuth 支持(CLI 的 oauth_creds.json),
	// 于是改指 grok。改的是"哪个平台是纯 cookie", 不是"要不要有这个断言":
	// 没有这个反向断言, 将来给 grok 随手加一条 spec 也不会有任何测试说话。
	if _, err := AccountOAuthSpecOf(AccountPlatformGrok); err == nil {
		t.Fatal("grok 不应有 OAuth 刷新规格(未核实官方端点)")
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

// Gemini 的刷新规格: 端点与字段名是固定的, client 身份来自部署配置。
//
// 这份断言的作用与 Claude 那份相同: 这些值写错**不会**报错, 只会换不到令牌 ——
// 于是表现为"账号导入成功但永远刷新失败", 而错误信息只说是 invalid_client。
//
// client_id / client_secret 刻意不写死在代码里: 它们是 Google 分配给 Gemini CLI 的身份,
// 我们从发行包提取得出来不代表有权分发与冒用。部署者应使用自己注册的 OAuth 客户端 ——
// 自己的配额与责任, 也避免所有部署挤在同一个 client 上被上游限流。
func TestAccountOAuthGeminiSpecMatchesOfficialClient(t *testing.T) {
	// 配一份假值, 让 spec 能取到 client_id(真实值由部署注入)。
	t.Setenv(geminiOAuthClientIDEnv, "test-client-id")
	t.Setenv(geminiOAuthClientSecretEnv, "test-client-secret")
	spec, err := AccountOAuthSpecOf(AccountPlatformGemini)
	if err != nil {
		t.Fatalf("gemini spec: %v", err)
	}
	if spec.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Fatalf("gemini token url = %q", spec.TokenURL)
	}
	if spec.ClientID != "test-client-id" {
		t.Fatalf("gemini client id = %q, want 配置注入值", spec.ClientID)
	}
	// 用 form 是为了跟随官方 Node 客户端; 实测该端点也接受 JSON, 见规格表注释。
	if spec.BodyFormat != accountBodyFormatForm {
		t.Fatalf("gemini body format = %q, want form", spec.BodyFormat)
	}
	if spec.AccessTokenField != "access_token" || spec.RefreshTokenField != "refresh_token" {
		t.Fatalf("gemini 响应字段名错误: %q / %q", spec.AccessTokenField, spec.RefreshTokenField)
	}
	// 凭据文件里的键名与响应同名(snake_case), 与 Claude 的 camelCase 相反。
	if spec.StoreAccessTokenField != "access_token" || spec.StoreRefreshTokenField != "refresh_token" {
		t.Fatalf("gemini 存储字段名错误: %q / %q", spec.StoreAccessTokenField, spec.StoreRefreshTokenField)
	}

	// client_id 来自运行时配置, 这里只验配置能被正确注入; 具体值由部署决定。
	// 断言的是"注入了配置里的值"而不是"某个写死的常量" —— 后者会让测试变成
	// 一份我们必须替用户保管的凭据副本。
	if spec.ClientID == "" {
		t.Fatal("gemini client id 未从配置注入")
	}
}

// TestGeminiOAuthClientPriority 验证取值优先级: DB(非默认) > 环境变量 > 内置默认。
//
// 使用内部函数变量 GeminiOAuthSettingGetter 模拟 DB 读取。
// 关键点: DB 值等于内置默认值时不算"用户覆盖", 会回退环境变量。
func TestGeminiOAuthClientPriority(t *testing.T) {
	originalGetter := GeminiOAuthSettingGetter
	defer func() { GeminiOAuthSettingGetter = originalGetter }()

	// 1) DB 存非默认值 -> 取 DB (用户在界面改过)
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		switch key {
		case SettingKeyGeminiOAuthClientID:
			return "user-db-client-id", nil
		case SettingKeyGeminiOAuthClientSecret:
			return "user-db-client-secret", nil
		default:
			return "", fmt.Errorf("not found")
		}
	}
	t.Setenv(geminiOAuthClientIDEnv, "env-client-id")
	t.Setenv(geminiOAuthClientSecretEnv, "env-client-secret")
	if got := geminiOAuthClientID(); got != "user-db-client-id" {
		t.Errorf("DB 非默认值时应取 DB client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != "user-db-client-secret" {
		t.Errorf("DB 非默认值时应取 DB client_secret: got %q", got)
	}
	if src := GeminiOAuthClientSource(); src != "db" {
		t.Errorf("来源应为 db: got %q", src)
	}

	// 2) DB 存默认值 + env 有值 -> 取 env (界面没改, env 生效)
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		switch key {
		case SettingKeyGeminiOAuthClientID:
			return geminiBuiltinClientID, nil // 等于内置默认
		case SettingKeyGeminiOAuthClientSecret:
			return geminiBuiltinClientSecret, nil // 等于内置默认
		default:
			return "", fmt.Errorf("not found")
		}
	}
	if got := geminiOAuthClientID(); got != "env-client-id" {
		t.Errorf("DB 为默认值+env 有值时应取 env client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != "env-client-secret" {
		t.Errorf("DB 为默认值+env 有值时应取 env client_secret: got %q", got)
	}
	if src := GeminiOAuthClientSource(); src != "env" {
		t.Errorf("来源应为 env: got %q", src)
	}

	// 3) DB 存默认值 + env 空 -> 取内置默认
	t.Setenv(geminiOAuthClientIDEnv, "")
	t.Setenv(geminiOAuthClientSecretEnv, "")
	if got := geminiOAuthClientID(); got != geminiBuiltinClientID {
		t.Errorf("DB 默认值+env 空时应回退内置默认 client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != geminiBuiltinClientSecret {
		t.Errorf("DB 默认值+env 空时应回退内置默认 client_secret: got %q", got)
	}
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("来源应为 builtin: got %q", src)
	}

	// 4) DB getter 为 nil + env 有值 -> 取 env (无 DB 时 env 生效)
	GeminiOAuthSettingGetter = nil
	t.Setenv(geminiOAuthClientIDEnv, "env-only-id")
	t.Setenv(geminiOAuthClientSecretEnv, "env-only-secret")
	if got := geminiOAuthClientID(); got != "env-only-id" {
		t.Errorf("无 DB+env 有值时应取 env client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != "env-only-secret" {
		t.Errorf("无 DB+env 有值时应取 env client_secret: got %q", got)
	}
	if src := GeminiOAuthClientSource(); src != "env" {
		t.Errorf("来源应为 env: got %q", src)
	}

	// 5) DB getter 为 nil + env 空 -> 取内置默认
	t.Setenv(geminiOAuthClientIDEnv, "")
	t.Setenv(geminiOAuthClientSecretEnv, "")
	if got := geminiOAuthClientID(); got != geminiBuiltinClientID {
		t.Errorf("无 DB+env 空时应取内置默认 client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != geminiBuiltinClientSecret {
		t.Errorf("无 DB+env 空时应取内置默认 client_secret: got %q", got)
	}
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("来源应为 builtin: got %q", src)
	}

	// 6) DB getter 返回错误 -> 回退 env
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) { return "", fmt.Errorf("db error") }
	t.Setenv(geminiOAuthClientIDEnv, "error-fallback-id")
	t.Setenv(geminiOAuthClientSecretEnv, "error-fallback-secret")
	if got := geminiOAuthClientID(); got != "error-fallback-id" {
		t.Errorf("DB 错误时应回退 env client_id: got %q", got)
	}
	if got := geminiOAuthClientSecret(); got != "error-fallback-secret" {
		t.Errorf("DB 错误时应回退 env client_secret: got %q", got)
	}
}

// TestGeminiOAuthClientSourceConsistency 验证 client_id 与 secret 来源一致:
// 必须同源(要么都 DB, 要么都 env, 要么都 builtin), 防止混搭得到 invalid_client。
//
// 这条测试曾在旧实现下"记录缺陷"(只改一半 -> 得到混搭的一对)。那种行为现已修掉:
// 混搭会在上游回 invalid_client, 而报错看起来像"账号坏了", 排查方向完全错。
// 所以现在的断言是反过来的 —— **不混搭**, 整对回落到能用的一层。
func TestGeminiOAuthClientSourceConsistency(t *testing.T) {
	originalGetter := GeminiOAuthSettingGetter
	defer func() { GeminiOAuthSettingGetter = originalGetter }()

	// DB 只改了 client_id, secret 仍是内置默认; env 有一整套。
	// 期望: 整对用 env, 而不是 id 用 DB + secret 用 env 拼出一对假凭据。
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		if key == SettingKeyGeminiOAuthClientID {
			return "user-db-id", nil
		}
		return geminiBuiltinClientSecret, nil
	}
	t.Setenv(geminiOAuthClientIDEnv, "env-id")
	t.Setenv(geminiOAuthClientSecretEnv, "env-secret")

	id, sec := GeminiOAuthClient()
	if id != "env-id" || sec != "env-secret" {
		t.Errorf("只配了半套时应整对回落到 env: got id=%q sec=%q", id, sec)
	}

	// 反向: env 只配了一半(client_id 有, secret 空) -> 不能采纳这一半, 整对回落内置默认。
	GeminiOAuthSettingGetter = nil
	t.Setenv(geminiOAuthClientIDEnv, "only-id")
	t.Setenv(geminiOAuthClientSecretEnv, "")
	id, sec = GeminiOAuthClient()
	if id != geminiBuiltinClientID || sec != geminiBuiltinClientSecret {
		t.Errorf("env 只配了一半时应整对回落到内置默认: got id=%q sec=%q", id, sec)
	}
}

// TestGeminiOAuthClientSource 验证来源判断, 语义与 geminiOAuthClientPair 一致:
// 整对成对判定, db(两个都非默认) > env(两个都非空) > builtin。
//
// 这条测试必须与取值逻辑同口径: 若两者判定不一致, 界面会显示"用的是你配的客户端"
// 而实际用着内置默认 —— 这比不显示来源更糟。
func TestGeminiOAuthClientSource(t *testing.T) {
	originalGetter := GeminiOAuthSettingGetter
	defer func() { GeminiOAuthSettingGetter = originalGetter }()

	// 无任何配置 -> 内置默认
	GeminiOAuthSettingGetter = nil
	t.Setenv(geminiOAuthClientIDEnv, "")
	t.Setenv(geminiOAuthClientSecretEnv, "")
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("无配置时来源应为 builtin: got %q", src)
	}

	// 只有环境变量、且两个都配了 -> env
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) { return "", fmt.Errorf("not found") }
	t.Setenv(geminiOAuthClientIDEnv, "env-id")
	t.Setenv(geminiOAuthClientSecretEnv, "env-secret")
	if src := GeminiOAuthClientSource(); src != "env" {
		t.Errorf("环境变量成对配置时来源应为 env: got %q", src)
	}

	// 环境变量只配一半 -> 不采纳, 仍算 builtin(避免混搭)
	t.Setenv(geminiOAuthClientSecretEnv, "")
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("环境变量只配一半时来源应为 builtin: got %q", src)
	}
	t.Setenv(geminiOAuthClientIDEnv, "")
	t.Setenv(geminiOAuthClientSecretEnv, "")

	// DB 成对非默认 -> db
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		if key == SettingKeyGeminiOAuthClientID {
			return "user-db-id", nil
		}
		return "user-db-secret", nil
	}
	if src := GeminiOAuthClientSource(); src != "db" {
		t.Errorf("DB 成对非默认时来源应为 db: got %q", src)
	}

	// DB 只有默认值 -> 不算覆盖, 回退 builtin
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		if key == SettingKeyGeminiOAuthClientID {
			return geminiBuiltinClientID, nil
		}
		return geminiBuiltinClientSecret, nil
	}
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("DB 只有默认值时来源应为 builtin: got %q", src)
	}

	// DB 只改了 secret(id 仍是默认) -> 半套, 不采纳, 回退 builtin
	GeminiOAuthSettingGetter = func(key SettingKey) (string, error) {
		if key == SettingKeyGeminiOAuthClientSecret {
			return "user-db-secret", nil
		}
		return geminiBuiltinClientID, nil
	}
	if src := GeminiOAuthClientSource(); src != "builtin" {
		t.Errorf("DB 只改一半时来源应为 builtin: got %q", src)
	}
}

// 配置齐全时, 请求体必须真的带上 client_id 与 client_secret。
func TestAccountOAuthGeminiSendsClientCredentials(t *testing.T) {
	t.Setenv(geminiOAuthClientIDEnv, "deploy-client-id")
	t.Setenv(geminiOAuthClientSecretEnv, "deploy-client-secret")
	if !GeminiOAuthClientConfigured() {
		t.Fatal("配置齐全时 GeminiOAuthClientConfigured 应为 true")
	}
	spec, err := AccountOAuthSpecOf(AccountPlatformGemini)
	if err != nil {
		t.Fatalf("gemini spec: %v", err)
	}
	_, body, err := AccountOAuthRefreshRequest(spec, AccountCredential{RefreshToken: "rt-value"})
	if err != nil {
		t.Fatalf("构造 gemini 刷新请求: %v", err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("解析 gemini 请求体: %v", err)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt-value" {
		t.Fatalf("gemini 请求体缺少 grant_type/refresh_token: %s", string(body))
	}
	// 这两个字段缺任何一个, 上游都会回 invalid_client。
	if form.Get("client_id") != "deploy-client-id" {
		t.Fatalf("gemini 请求体 client_id = %q, want 配置值", form.Get("client_id"))
	}
	if form.Get("client_secret") != "deploy-client-secret" {
		t.Fatalf("gemini 请求体 client_secret 未取配置值")
	}
}

// Google 的刷新响应是标准 snake_case OAuth 响应, 解析后必须能续期。
func TestAccountOAuthGeminiRefreshResponse(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformGemini)
	if err != nil {
		t.Fatalf("gemini spec: %v", err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	// 真实响应形状(Google token 端点): access_token + expires_in + 可能不带 refresh_token。
	raw := []byte(`{"access_token":"new-at","expires_in":3599,"scope":"openid","token_type":"Bearer"}`)
	result, err := AccountOAuthParseRefreshResponse(spec, AccountCredential{RefreshToken: "old-rt"}, raw, now)
	if err != nil {
		t.Fatalf("解析 gemini 刷新响应: %v", err)
	}
	if result.AccessToken != "new-at" {
		t.Fatalf("access_token = %q", result.AccessToken)
	}
	// Google 通常不轮换 refresh_token: 此时必须沿用旧值, 否则下次刷新就没令牌了。
	if result.RefreshToken != "old-rt" {
		t.Fatalf("refresh_token = %q, want 沿用 old-rt", result.RefreshToken)
	}
	if result.RefreshTokenRotated {
		t.Fatal("未轮换时 RefreshTokenRotated 应为 false")
	}
	if want := now.Add(3599 * time.Second); !result.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", result.ExpiresAt, want)
	}
}

// Antigravity 的刷新规格必须带完整 scope, 否则新令牌会退回默认 scope,
// 表现为"刷新成功但数据面 403" —— 这是本平台最难查的一类静默失效。
func TestAntigravitySpecRequestsScopes(t *testing.T) {
	// Antigravity 的 client 不内置默认值, 必须由部署配置注入(与 gemini 的"内置可覆盖"不同)。
	// 未配置时规格里没有 client_id, 刷新会当场报错 —— 那个行为由另一个用例钉住。
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-antigravity-id")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-antigravity-secret")

	spec, err := AccountOAuthSpecOf(AccountPlatformAntigravity)
	if err != nil {
		t.Fatalf("antigravity 必须登记刷新规格: %v", err)
	}
	if spec.TokenURL == "" || spec.ClientID == "" {
		t.Fatalf("antigravity 规格不完整: %+v", spec)
	}
	if len(spec.Scopes) == 0 {
		t.Fatal("antigravity 刷新必须提交 scope, 否则新令牌会丢失 cclog/experimentsandconfigs")
	}
	has := func(want string) bool {
		for _, s := range spec.Scopes {
			if s == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	} {
		if !has(want) {
			t.Errorf("antigravity 刷新 scope 缺少 %s", want)
		}
	}

	// 光有 spec.Scopes 不算数: 必须真的落在刷新请求体里。
	_, body, err := AccountOAuthRefreshRequest(spec, AccountCredential{
		Platform:     AccountPlatformAntigravity,
		RefreshToken: "rt",
	})
	if err != nil {
		t.Fatalf("构造刷新请求: %v", err)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("刷新请求体不是合法表单: %v (%s)", err, body)
	}
	scope := form.Get("scope")
	if scope == "" {
		t.Fatal("刷新表单里没有 scope 字段: Scopes 没有被落在请求体上")
	}
	for _, want := range []string{"cclog", "experimentsandconfigs"} {
		if !strings.Contains(scope, want) {
			t.Errorf("刷新表单 scope 里缺 %s, 实际=%q", want, scope)
		}
	}
	if got := form.Get("refresh_token"); got != "rt" {
		t.Errorf("refresh_token = %q, want rt", got)
	}
	if got := form.Get("client_id"); got == "" {
		t.Error("刷新表单缺 client_id")
	}
}

// 刷新路径同样必须在未配置时拦住, 而不是发出一个必然 invalid_client 的请求。
func TestAntigravityRefreshRequiresConfiguredClient(t *testing.T) {
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")

	spec, err := AccountOAuthSpecOf(AccountPlatformAntigravity)
	if err != nil {
		t.Fatalf("规格应能取到: %v", err)
	}
	_, _, err = AccountOAuthRefreshRequest(spec, AccountCredential{
		Platform:     AccountPlatformAntigravity,
		RefreshToken: "rt",
	})
	if err == nil {
		t.Fatal("未配置 client 时刷新必须报错")
	}
	if !strings.Contains(err.Error(), "SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID") {
		t.Errorf("刷新错误没指出该配的变量: %v", err)
	}
}
