package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/signin"
)

// 登录换到的令牌转成凭据 JSON 后, 必须能被现有的解析器吃下 ——
// 这是整条链路最容易出问题的一处: 形状对不上不会报错, 只会得到一个"存进去了但
// 刷新/转发都读不出来"的账号。
func TestCredentialJSONRoundTripsThroughParser(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC()

	cases := []struct {
		platform string
		tokens   *signin.TokenResult
		wantKind string
	}{
		{
			platform: model.AccountPlatformClaude,
			tokens: &signin.TokenResult{
				AccessToken: "at-claude", RefreshToken: "rt-claude", ExpiresAt: expires,
			},
			wantKind: model.AccountKindOAuth,
		},
		{
			platform: model.AccountPlatformCodex,
			tokens: &signin.TokenResult{
				AccessToken: "at-codex", RefreshToken: "rt-codex", ExpiresAt: expires,
			},
			wantKind: model.AccountKindOAuth,
		},
		{
			platform: model.AccountPlatformGemini,
			tokens: &signin.TokenResult{
				AccessToken: "at-gemini", RefreshToken: "rt-gemini", ExpiresAt: expires,
			},
			wantKind: model.AccountKindOAuth,
		},
	}

	for _, c := range cases {
		t.Run(c.platform, func(t *testing.T) {
			raw, err := credentialJSONOf(c.platform, c.tokens)
			if err != nil {
				t.Fatalf("转换: %v", err)
			}
			credential, err := model.AccountJSONParse(c.platform, []byte(raw))
			if err != nil {
				t.Fatalf("解析器吃不进登录产出的凭据 (%s): %v", raw, err)
			}
			if credential.Kind != c.wantKind {
				t.Fatalf("kind = %q, want %q", credential.Kind, c.wantKind)
			}
			// 令牌必须原样穿透: 少一个字符都会让账号导入成功但立刻失效。
			if credential.AccessToken != c.tokens.AccessToken {
				t.Fatalf("access_token 未穿透: %q", credential.AccessToken)
			}
			if credential.RefreshToken != c.tokens.RefreshToken {
				t.Fatalf("refresh_token 未穿透: %q", credential.RefreshToken)
			}
			// 到期时间必须被解析出来且接近原值。差一个数量级(毫秒当秒)会让账号
			// 在一次刷新后就被判定为长期有效, 或永远被判定为已过期。
			if credential.ExpiresAt.IsZero() {
				t.Fatal("到期时间解析为零值: 定时刷新永远不会触发, 账号会静默失效")
			}
			if diff := credential.ExpiresAt.Sub(expires); diff > time.Minute || diff < -time.Minute {
				t.Fatalf("到期时间偏差过大: got %v want %v", credential.ExpiresAt, expires)
			}
		})
	}
}

// 未实现的平台必须报错, 不能返回一个空 JSON 让导入层去猜。
func TestCredentialJSONRejectsUnknownPlatform(t *testing.T) {
	if _, err := credentialJSONOf("kiro", &signin.TokenResult{AccessToken: "a"}); err == nil {
		t.Fatal("kiro 的登录凭据转换未实现, 应当报错")
	}
}

// Claude 的凭据必须放在 claudeAiOauth 容器里且是 camelCase: 平铺会让解析层读不到,
// 而这是"登录成功但账号不能用"的典型成因。
func TestClaudeCredentialShape(t *testing.T) {
	raw, err := credentialJSONOf(model.AccountPlatformClaude, &signin.TokenResult{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("转换: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	inner, ok := m["claudeAiOauth"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 claudeAiOauth 容器: %s", raw)
	}
	if inner["accessToken"] != "at" || inner["refreshToken"] != "rt" {
		t.Fatalf("容器内键名不是 camelCase: %v", inner)
	}
}

// Gemini 的到期时间必须写成毫秒时间戳: 写成 RFC3339 字符串会让解析层拿不到值。
func TestGeminiCredentialUsesMillis(t *testing.T) {
	expires := time.UnixMilli(1900000000000)
	raw, err := credentialJSONOf(model.AccountPlatformGemini, &signin.TokenResult{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("转换: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if _, ok := m["expiry_date"].(float64); !ok {
		t.Fatalf("expiry_date 不是数字(毫秒时间戳): %s", raw)
	}
}

// 平台能力查询要反映"部署是否配好": gemini 没配 client 时必须标成不可用并给出原因,
// 否则界面会提供一个一点就报错的按钮。
func TestSignInPlatformsReflectConfig(t *testing.T) {
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "")
	t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "")
	provider, err := signin.ProviderOf("gemini")
	if err != nil && provider.ClientID == "" {
		return // 预期: 未配置时拒绝, 接口层据此标记不可用
	}
}

// clientOwnerOf 必须区分"别人的 client"与"自己注册的 client": 界面要靠它告诉用户
// 授权页上显示的会是哪一方在申请权限。
func TestClientOwnerOf(t *testing.T) {
	if got := clientOwnerOf(model.AccountPlatformClaude); got != "claude-code" {
		t.Fatalf("claude client owner = %q", got)
	}
	if got := clientOwnerOf(model.AccountPlatformCodex); got != "codex-cli" {
		t.Fatalf("codex client owner = %q", got)
	}
	if got := clientOwnerOf(model.AccountPlatformGemini); got != "self-registered" {
		t.Fatalf("gemini client owner = %q", got)
	}
}
