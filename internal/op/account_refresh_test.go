
package op

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 刷新层守的是三件事, 每一件出事都是"账号悄悄失效":
//   1. 该刷的时候刷(提前量边界), 不该刷的时候不刷;
//   2. refresh_token 轮换后必须完整回写(键名按平台规格, 不写死);
//   3. 写库失败必须放大成专用错误类型 —— 上游已经作废旧令牌, 新值没落库就是永久失效。

func accountTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "account.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	channelKeyCache.Clear()
}

// seedAccountKey 建渠道与账号凭据并刷缓存。
func seedAccountKey(t *testing.T, platform, rawJSON string, expiresAt *time.Time) model.ChannelKey {
	t.Helper()
	channel := model.Channel{
		UserID: 1,
		ChannelConfig: model.ChannelConfig{Name: "acc-channel-" + fmt.Sprint(time.Now().UnixNano()), Enabled: true, BaseURL: "https://api.example.com"},
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{
		ChannelID: channel.ID,
		ChannelKeyConfig: model.ChannelKeyConfig{
			Name: "acc-key", Key: rawJSON, Enabled: true,
			AccountKind:     model.AccountKindOAuth,
			AccountPlatform: platform,
			TokenExpiresAt:   expiresAt,
		},
	}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	channelKeyCache.Set(key.ID, key)
	return key
}

// 提前量边界: 已有实现里这个边界曾经写反(用 After 而不是 !Before), 导致恰好落在边界上不刷新,
// 那一轮必然拿过期令牌打上游吃 401。
func TestAccountNeedsRefreshBoundary(t *testing.T) {
	accountTestDB(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	lead := accountRefreshLead

	rawJSON := `{"access_token":"a.b.c","refresh_token":"rt"}`
	cases := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		{"恰好提前量边界", ptrTime(now.Add(lead)), true},
		{"超出提前量一秒", ptrTime(now.Add(lead + time.Second)), false},
		{"已过期", ptrTime(now.Add(-time.Minute)), true},
		{"到期未知", nil, false},
	}
	for _, testCase := range cases {
		key := seedAccountKey(t, model.AccountPlatformCodex, rawJSON, testCase.expiresAt)
		if got := AccountNeedsRefresh(key, now); got != testCase.want {
			t.Fatalf("%s: got %v want %v", testCase.name, got, testCase.want)
		}
		channelKeyCache.Clear()
	}
}

// 静态凭据(非账号)永远不该被刷新任务选中。
func TestAccountNeedsRefreshIgnoresStaticKeys(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-plain"}}
	if AccountNeedsRefresh(key, time.Now()) {
		t.Fatal("静态 Key 不应进入刷新集合")
	}
	if _, ok := AccountCredentialOf(key); ok {
		t.Fatal("静态 Key 不应被当作账号凭据")
	}
}

// 账号 JSON 坏掉时: 刷新集合不应把它当成"到期待刷新"而反复打上游, 但也不能当成普通 Key 静默放行。
func TestAccountCredentialOfRejectsBrokenJSON(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key: "not-json",
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
	}}
	if _, ok := AccountCredentialOf(key); ok {
		t.Fatal("坏 JSON 不应解析成功")
	}
}

// AccountRefreshesDue 只收启用的 oauth 凭据: 停用的不刷(它不参与转发, 刷了也用不上)。
func TestAccountRefreshesDueSkipsDisabledAndCookie(t *testing.T) {
	accountTestDB(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	rawJSON := `{"access_token":"a","refresh_token":"rt"}`

	enabledCfg := model.ChannelKeyConfig{
		Name: "e", Key: rawJSON, Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
		TokenExpiresAt: &past,
	}
	disabledCfg := model.ChannelKeyConfig{
		Name: "d", Key: rawJSON, Enabled: false,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
		TokenExpiresAt: &past,
	}
	cookieCfg := model.ChannelKeyConfig{
		Name: "c", Key: `{"sso":"x"}`, Enabled: true,
		AccountKind: model.AccountKindCookie, AccountPlatform: model.AccountPlatformGrok,
	}
	enabled := model.ChannelKey{ChannelKeyConfig: enabledCfg}
	disabled := model.ChannelKey{ChannelKeyConfig: disabledCfg}
	cookieKey := model.ChannelKey{ChannelKeyConfig: cookieCfg}
	channelKeyCache.Set(1, enabled)
	channelKeyCache.Set(2, disabled)
	channelKeyCache.Set(3, cookieKey)

	due := AccountRefreshesDue(now)
	if len(due) != 1 || due[0].Name != "e" {
		t.Fatalf("due = %+v, want only 启用 oauth 的 e", due)
	}
}

// 键名回归: 回写必须用平台规格里的字段名。Kiro 的 refreshToken 是 camelCase,
// 写死成 refresh_token 不会报错, 但下一次刷新读不到 —— 症状与账号被封一样。
func TestAccountRefreshWritesPlatformFieldNames(t *testing.T) {
	// 这条只校验"写回哪个键"这件事本身, 不发网络请求: 直接验规格字段名的映射。
	spec, err := model.AccountOAuthSpecOf(model.AccountPlatformKiro)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if spec.RefreshTokenField != "refreshToken" {
		t.Fatalf("kiro refresh 字段 = %q, want refreshToken", spec.RefreshTokenField)
	}
	codexSpec, err := model.AccountOAuthSpecOf(model.AccountPlatformCodex)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if codexSpec.RefreshTokenField != "refresh_token" {
		t.Fatalf("codex refresh 字段 = %q, want refresh_token", codexSpec.RefreshTokenField)
	}

	// 用规格字段名组出来的 JSON 必须能被解析层读回 —— 这是回写与解析之间的契约。
	// Kiro 的解析要求 clientId/clientSecret 齐全, 所以回写往返必须带上它们 ——
	// 这也说明回写不能只写 token 两列, 原始 JSON 的其它键必须原样保留(下面用合并而非覆盖)。
	payload := map[string]any{
		spec.AccessTokenField:  "new-access",
		spec.RefreshTokenField: "new-refresh",
		"clientId":             "cid",
		"clientSecret":         "sec",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	credential, err := model.AccountJSONParse(model.AccountPlatformKiro, encoded)
	if err != nil {
		t.Fatalf("parse roundtrip: %v", err)
	}
	if credential.RefreshToken != "new-refresh" || credential.AccessToken != "new-access" {
			t.Fatalf("roundtrip = %+v", credential)
	}
}

// 持久化失败必须单独成类: 它意味着上游已作废旧 refresh_token 而新值没落库, 是"账号即将永久失效",
// 不能被当成普通刷新失败重试掉。
func TestAccountRefreshPersistErrorIsDistinct(t *testing.T) {
	persistErr := &AccountRefreshPersistError{ChannelKeyID: 7, Err: errTest}
	if _, ok := error(persistErr).(*AccountRefreshError); ok {
		t.Fatal("持久化失败不应被当成上游拒绝")
	}
	upstreamErr := &AccountRefreshError{Status: 400, Body: "invalid_grant"}
	if _, ok := error(upstreamErr).(*AccountRefreshPersistError); ok {
		t.Fatal("上游拒绝不应被当成持久化失败")
	}
}

var errTest = &testError{}

type testError struct{}

func (e *testError) Error() string { return "test" }
