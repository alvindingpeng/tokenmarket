
package op

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 导入与刷新的完整闭环测试: 用本地假令牌端点, 不碰真实上游。
// 之所以要建假端点而不是直接 mock AccountRefresh: 真正出问题的地方在"刷新之后回写的键名与形状",
// mock 掉这一层就测不到了。

// accountFakeTokenServer 起一个假令牌端点。
// responds 决定返回什么(用它模拟成功/轮换/上游拒绝)。
func accountFakeTokenServer(t *testing.T, status int, payload map[string]any, seenRequest *map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenRequest != nil {
			_ = r.ParseForm()
			captured := map[string]string{"content_type": r.Header.Get("Content-Type")}
			// 平台要求的固定头也要捕获: Kiro 少了 x-amz-user-agent 会被 AWS 拒,
			// 而那种失败在响应里看不出原因。
			for _, name := range []string{"User-Agent", "x-amz-user-agent", "amz-sdk-request"} {
				if value := r.Header.Get(name); value != "" {
					captured["header:"+name] = value
				}
			}
			for key := range r.Form {
				captured[key] = r.Form.Get(key)
			}
			if r.Header.Get("Content-Type") == "application/json" {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				for key, value := range body {
					if text, ok := value.(string); ok {
						captured[key] = text
					}
				}
			}
			*seenRequest = captured
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)
	return server
}

func seedImportChannel(t *testing.T) model.Channel {
	t.Helper()
	channel := model.Channel{
		UserID: 1,
		ChannelConfig: model.ChannelConfig{
			Name: "import-" + time.Now().Format("150405.000000000"), Enabled: true,
			BaseURL: "https://api.example.com",
		},
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return channel
}

func TestAccountImportRejectsUnreadableJSON(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)

	// 非法 JSON / 缺必填字段 / 不是对象: 都不能落库 —— 落库就会变成
	// "看起来存在但从不能转发"的凭据, 而使用者只看到导入成功。
	for _, raw := range []string{"not json", `{"refresh_token":"rt"}`, `["array"]`} {
		_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
			Platform: model.AccountPlatformCodex, Name: "k", Raw: raw, Enabled: true,
		}, time.Now())
		if err == nil {
			t.Fatalf("raw=%q 应当拒绝", raw)
		}
	}
	var count int64
	db.GetDB().Model(&model.ChannelKey{}).Where("channel_id = ?", channel.ID).Count(&count)
	if count != 0 {
		t.Fatalf("非法 JSON 不应落库, 实际写入 %d 条", count)
	}
}

func TestAccountImportRequiresName(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	if _, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "   ",
		Raw: `{"access_token":"a","refresh_token":"b"}`,
	}, time.Now()); err == nil {
		t.Fatal("空凭据名应当拒绝")
	}
}

// cookie 类账号: 能转发, 但没有续期能力 —— kind 必须是 cookie, 且不尝试刷新。
func TestAccountImportCookieKindHasNoRefresh(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)

	result, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformGrok, Name: "grok-1",
		Raw: `{"sso":"sso-value","email":"u@example.com"}`, Enabled: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("import grok: %v", err)
	}
	if result.Kind != model.AccountKindCookie {
		t.Fatalf("kind = %q, want cookie", result.Kind)
	}
	if result.RefreshedNow {
		t.Fatal("cookie 类账号不应触发刷新")
	}
	if result.Email != "u@example.com" {
		t.Fatalf("email = %q", result.Email)
	}
}

// 成功导入 oauth 账号: 首刷真的发生了, 且轮换后的 refresh_token 落库 ——
// 这是整条链路里最容易出错的一环(回写丢令牌=账号永久失效)。
func TestAccountImportRefreshesAndPersistsRotatedToken(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	seen := map[string]string{}
	server := accountFakeTokenServer(t, http.StatusOK, map[string]any{
		"access_token": "new-access", "refresh_token": "rotated-refresh", "expires_in": 3600,
	}, &seen)
	// 假服务端在 127.0.0.1 上: 必须临时开启私网许可, 否则 SSRF 防护会把请求挡在门外。
	// 这也正是生产默认拒绝私网的证明 —— 同一条防护在这里如预期生效。
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("enable private upstream: %v", err)
	}
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, server.URL)
	t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, "") })

	result, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "codex-1",
		Raw: `{"access_token":"old-access","refresh_token":"old-refresh","email":"u@example.com"}`,
		Enabled: true,
	}, time.Now())
	if err != nil {
		if _, ok := err.(*AccountImportRefreshWarning); !ok {
			t.Fatalf("首刷应当成功, got %v", err)
		}
	}
	if !result.RefreshedNow {
		t.Fatal("应当标记已刷新")
	}
	// 请求形态: Codex 用 form-urlencoded 且带 client_id。
	if seen["grant_type"] != "refresh_token" || seen["refresh_token"] != "old-refresh" {
		t.Fatalf("刷新请求参数不对: %v", seen)
	}
	if seen["client_id"] == "" {
		t.Fatalf("Codex 刷新请求必须带 client_id: %v", seen)
	}

	// 落库检查: 新 access_token 与轮换后的 refresh_token 都必须在库里。
	var stored model.ChannelKey
	db.GetDB().First(&stored, result.ChannelKeyID)
	var payload map[string]any
	if err := json.Unmarshal([]byte(stored.Key), &payload); err != nil {
		t.Fatalf("stored key 不是 JSON: %v", err)
	}
	if payload["access_token"] != "new-access" {
		t.Fatalf("落库 access_token = %v", payload["access_token"])
	}
	if payload["refresh_token"] != "rotated-refresh" {
		t.Fatalf("落库 refresh_token = %v, want rotated-refresh", payload["refresh_token"])
	}
	if stored.TokenExpiresAt == nil {
		t.Fatal("落库应记下到期时间")
	}
	if stored.AccountKind != model.AccountKindOAuth || stored.AccountPlatform != model.AccountPlatformCodex {
		t.Fatalf("元数据错误: kind=%q platform=%q", stored.AccountKind, stored.AccountPlatform)
	}
	// 转发用的缓存也必须同步, 否则刷新白做(要等下次全量重载才生效)。
	if cached, ok := channelKeyCache.Get(stored.ID); !ok {
		t.Fatal("刷新后应写入缓存")
	} else if !strings.Contains(cached.Key, "new-access") {
		t.Fatalf("缓存里仍是旧令牌: %s", cached.Key)
	}
}

// 上游拒绝(invalid_grant): 导入仍然成功(行在库里), 但必须把失败原因带回,
// 让使用者知道这个账号此刻不可用, 而不是以为一切正常。
func TestAccountImportReportsUpstreamRejection(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	server := accountFakeTokenServer(t, http.StatusBadRequest,
		map[string]any{"error": "invalid_grant"}, nil)
	// 假服务端在 127.0.0.1 上: 必须临时开启私网许可, 否则 SSRF 防护会把请求挡在门外。
	// 这也正是生产默认拒绝私网的证明 —— 同一条防护在这里如预期生效。
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("enable private upstream: %v", err)
	}
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, server.URL)
	t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, "") })

	result, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "bad",
		Raw: `{"access_token":"a","refresh_token":"rt"}`, Enabled: true,
	}, time.Now())
	if err == nil {
		t.Fatal("上游拒绝时应当带回警告")
	}
	if _, ok := err.(*AccountImportRefreshWarning); !ok {
		t.Fatalf("应当是 AccountImportRefreshWarning, got %T", err)
	}
	// 关键: 行必须还在 —— 导入了坏账号不该被静默回滚, 也不该被当成"从未导入"。
	var count int64
	db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", result.ChannelKeyID).Count(&count)
	if count != 1 {
		t.Fatal("上游拒绝不应回滚已写入的凭据")
	}
}

func TestAccountImportDuplicateNameReportedClearly(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	server := accountFakeTokenServer(t, http.StatusOK,
		map[string]any{"access_token": "a", "refresh_token": "b", "expires_in": 60}, nil)
	// 假服务端在 127.0.0.1 上: 必须临时开启私网许可, 否则 SSRF 防护会把请求挡在门外。
	// 这也正是生产默认拒绝私网的证明 —— 同一条防护在这里如预期生效。
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("enable private upstream: %v", err)
	}
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, server.URL)
	t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, "") })

	raw := `{"access_token":"a","refresh_token":"b"}`
	if _, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "dup", Raw: raw, Enabled: true,
	}, time.Now()); err != nil {
		t.Fatalf("first import: %v", err)
	}
	_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "dup", Raw: raw, Enabled: true,
	}, time.Now())
	if err == nil {
		t.Fatal("重名应当报错")
	}
	// 报错要说清是撞名, 不是把 SQL 约束错误原样抛出去 —— 后者像系统故障, 会让人反复重试。
	if !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重名错误应说明已存在, got %v", err)
	}
}
