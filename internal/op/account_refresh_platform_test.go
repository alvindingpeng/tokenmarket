package op

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 三平台的刷新端到端测试。每个平台都要验两件事:
//   1) 请求发出去时形状对(键名/Content-Type/必需头) —— 错了会换不到令牌
//   2) 刷新后回写的位置对 —— 错了会让账号静默失效(令牌换了但自己读不回来)
// 用假端点, 不碰真实上游。

type platformCase struct {
	platform     string
	importRaw    string
	payload      map[string]any
	wantAuthType string
	wantScope    string
	wantHeader   string
	// 回写后再解析, 期望读到的新令牌键名(由解析层决定, 与存储层级一一对应)。
	wantNewAccess  string
	wantNewRefresh string
}

func TestAccountRefreshPerPlatformRoundTrip(t *testing.T) {
	cases := []platformCase{
		{
			// Codex: 顶层 snake_case, 表单编码, 无额外头, 无 scope。
			platform:       model.AccountPlatformCodex,
			importRaw:      `{"access_token":"old-a","refresh_token":"old-r"}`,
			payload:        map[string]any{"access_token": "new-a", "refresh_token": "new-r", "expires_in": 3600},
			wantAuthType:   "application/x-www-form-urlencoded",
			wantNewAccess:  "new-a",
			wantNewRefresh: "new-r",
		},
		{
			// Kiro: 全 camelCase, JSON, 必须带 AWS SDK 头。
			platform:       model.AccountPlatformKiro,
			importRaw:      `{"accessToken":"old-a","refreshToken":"old-r","clientId":"cid","clientSecret":"sec"}`,
			payload:        map[string]any{"accessToken": "new-a", "refreshToken": "new-r", "expiresIn": 3600},
			wantAuthType:   "application/json",
			wantHeader:     "x-amz-user-agent",
			wantNewAccess:  "new-a",
			wantNewRefresh: "new-r",
		},
		{
			// Claude: 响应 snake_case, 但凭据文件里是 claudeAiOauth + camelCase。
			// 回写必须落回 claudeAiOauth 层并写 camelCase 键, 否则解析层读不回来。
			platform:       model.AccountPlatformClaude,
			importRaw:      `{"claudeAiOauth":{"accessToken":"old-a","refreshToken":"old-r","expiresAt":1700000000000}}`,
			payload:        map[string]any{"access_token": "new-a", "refresh_token": "new-r", "expires_in": 3600},
			wantAuthType:   "application/json",
			wantScope:      "user:inference user:profile",
			wantNewAccess:  "new-a",
			wantNewRefresh: "new-r",
		},
		{
			// Gemini: 顶层 snake_case(与 Codex 同键名)但走 form 编码, 且请求体必须带
			// client_id 与 client_secret —— 少了后者上游回 invalid_client。
			// 凭据文件是 Gemini CLI 的 oauth_creds.json 形状。
			platform:       model.AccountPlatformGemini,
			importRaw:      `{"access_token":"old-a","refresh_token":"old-r","expiry_date":1700000000000}`,
			payload:        map[string]any{"access_token": "new-a", "expires_in": 3599, "token_type": "Bearer"},
			wantAuthType:   "application/x-www-form-urlencoded",
			wantNewAccess:  "new-a",
			wantNewRefresh: "old-r",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.platform, func(t *testing.T) {
			accountTestDB(t)
			if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
				t.Fatalf("allow private upstream: %v", err)
			}

			channel := seedImportChannel(t)
			expiresAt := time.Now().Add(-time.Hour)
			config := model.ChannelKeyConfig{
				Name: "acct-" + testCase.platform, Key: testCase.importRaw, Enabled: true,
				AccountKind: model.AccountKindOAuth, AccountPlatform: testCase.platform,
				TokenExpiresAt: &expiresAt,
			}
			key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: config}
			if err := db.GetDB().Create(&key).Error; err != nil {
				t.Fatalf("create key: %v", err)
			}
			channelKeyCache.Set(key.ID, key)

			// gemini 的 OAuth 客户端身份来自部署配置(见 model/account_oauth.go 的说明),
			// 测试里配一份假值, 才能验到"请求体确实带上了这两个字段"。
			if testCase.platform == model.AccountPlatformGemini {
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "test-client-id")
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "test-client-secret")
			}

			seen := map[string]string{}
			server := accountFakeTokenServer(t, http.StatusOK, testCase.payload, &seen)
			model.AccountOAuthTokenURLOverrideSet(testCase.platform, server.URL)
			t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(testCase.platform, "") })

			// 导入时 clientId/clientSecret 可能不在 JSON 顶层, 由 ExtraFields 从凭据里取。
			if err := AccountRefresh(context.Background(), key.ID, time.Now()); err != nil {
				t.Fatalf("%s refresh: %v", testCase.platform, err)
			}

			// 1) 请求形状
			if got := seen["content_type"]; got != testCase.wantAuthType {
				t.Fatalf("%s content-type = %q, want %q", testCase.platform, got, testCase.wantAuthType)
			}
			if testCase.wantScope != "" {
				if got := seen["scope"]; got != testCase.wantScope {
					t.Fatalf("%s scope = %q, want %q", testCase.platform, got, testCase.wantScope)
				}
			}
			if testCase.wantHeader != "" && seen["header:"+testCase.wantHeader] == "" {
				t.Fatalf("%s 缺少必需请求头 %s", testCase.platform, testCase.wantHeader)
			}

			// 2) 回写往返: 重新解析必须读到新令牌。
			// 这一步是真正的护栏 —— 它同时覆盖"写到哪一层"和"用哪个键名"。
			var stored model.ChannelKey
			if err := db.GetDB().Where("id = ?", key.ID).First(&stored).Error; err != nil {
				t.Fatalf("read back: %v", err)
			}
			credential, err := model.AccountJSONParse(testCase.platform, []byte(stored.Key))
			if err != nil {
				t.Fatalf("%s 回写后无法解析: %v (json=%s)", testCase.platform, err, stored.Key)
			}
			if credential.AccessToken != testCase.wantNewAccess {
				t.Fatalf("%s 回写后 access = %q, want %q (json=%s)",
					testCase.platform, credential.AccessToken, testCase.wantNewAccess, stored.Key)
			}
			if credential.RefreshToken != testCase.wantNewRefresh {
				t.Fatalf("%s 回写后 refresh = %q, want %q —— 轮换令牌没写回正确位置, 账号将静默失效 (json=%s)",
					testCase.platform, credential.RefreshToken, testCase.wantNewRefresh, stored.Key)
			}

			// 3) 形状保真: 回写后的文件必须仍能被"该平台真实的客户端"读懂, 而不只是被我们自己读懂。
			// 我们的解析层对 accessToken / access_token 两种键名都容忍, 所以上面第 2 步抓不到
			// "把 camelCase 写成 snake_case"这类错误 —— 但真实 CLI 只认它自己那一套。
			if testCase.platform == model.AccountPlatformClaude {
				var shape map[string]any
				if err := json.Unmarshal([]byte(stored.Key), &shape); err != nil {
					t.Fatalf("claude 回写结果不是 JSON 对象: %v", err)
				}
				layer, ok := shape["claudeAiOauth"].(map[string]any)
				if !ok {
					t.Fatalf("claude 回写后丢了 claudeAiOauth 层: %s", stored.Key)
				}
				// 真实 Claude Code 凭据文件只认 camelCase 的 accessToken/refreshToken。
				if layer["accessToken"] == nil || layer["refreshToken"] == nil {
					t.Fatalf("claude 回写用了 CLI 不认的键名(应为 camelCase): %s", stored.Key)
				}
				// 反证: 不该在 claudeAiOauth 层里混进 snake_case。
				if layer["access_token"] != nil || layer["refresh_token"] != nil {
					t.Fatalf("claudeAiOauth 层混入了 snake_case 键: %s", stored.Key)
				}
			}
			if testCase.platform == model.AccountPlatformGemini {
				// 真实 Gemini CLI 的 oauth_creds.json 是**顶层** snake_case ——
				// 不能像 Claude 那样包一层, 包了 CLI 就读不到, 而我们的解析层两种都容忍,
				// 所以第 2 步的往返断言抓不到这个错。
				var shape map[string]any
				if err := json.Unmarshal([]byte(stored.Key), &shape); err != nil {
					t.Fatalf("gemini 回写结果不是 JSON 对象: %v", err)
				}
				if shape["access_token"] == nil || shape["refresh_token"] == nil {
					t.Fatalf("gemini 回写必须在顶层用 snake_case 键: %s", stored.Key)
				}
				// client_secret 是刷新请求的必需字段, 但它不该被回写进凭据文件:
				// 那是全局常量, 写进每个账号的文件里既冗余又会让人误以为它是账号自带的。
				if shape["client_secret"] != nil {
					t.Fatalf("gemini 回写不该把全局 client_secret 塞进凭据文件: %s", stored.Key)
				}
			}
			// 请求里必须真的带了 client_id 与 client_secret。
			// 缺了它们上游回 invalid_client, 而错误只说是"客户端无效", 不会指向我们的规格表。
			if testCase.platform == model.AccountPlatformGemini {
				// 值必须来自配置, 不是某个写死的常量 —— 后者等于把别人的 client 身份固化进代码。
				if got := seen["client_secret"]; got != "test-client-secret" {
					t.Fatalf("gemini 请求 client_secret = %q, want 配置值 —— 缺失上游会回 invalid_client", got)
				}
				if got := seen["client_id"]; got != "test-client-id" {
					t.Fatalf("gemini 请求 client_id = %q, want 配置值", got)
				}
			}
		})
	}
}
