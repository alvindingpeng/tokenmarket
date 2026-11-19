package relay

import (
	"context"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// channelKeyTokenOf 是把"账号凭据"变成"本轮请求用的 Bearer"的唯一入口。
// 它错了不会报错, 只会让上游回 401 —— 表现是"渠道莫名其妙全是鉴权失败", 极难定位到这一行。

func TestChannelKeyTokenOfStaticKey(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-plain-key"}}
	// 静态凭据原样返回: 这是既有的全部渠道的行为, 不能被账号逻辑影响。
	if got := channelKeyTokenOf(key); got != "sk-plain-key" {
		t.Fatalf("static key = %q, want sk-plain-key", got)
	}
}

func TestChannelKeyTokenOfOAuthAccount(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             `{"access_token":"a.b.c","refresh_token":"rt"}`,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformCodex,
	}}
	// 账号凭据要取 access_token, 而不是把整包 JSON 当 Bearer 发出去。
	// 见过的最坏情况是把整包 JSON 塞进 Authorization: 上游回 401, 而日志里只看到一串 JSON。
	if got := channelKeyTokenOf(key); got != "a.b.c" {
		t.Fatalf("oauth token = %q, want a.b.c", got)
	}
}

// CLI 导出形状(prettier 的 tokens 层)同样要取出令牌。
func TestChannelKeyTokenOfWrappedAccount(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             `{"OPENAI_API_KEY":null,"tokens":{"access_token":"wrapped.tok.en","refresh_token":"rt"}}`,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformCodex,
	}}
	if got := channelKeyTokenOf(key); got != "wrapped.tok.en" {
		t.Fatalf("wrapped token = %q", got)
	}
}

// 坏的账号 JSON 返回空串而不是 panic/错误:
// 空 Bearer 会让上游回 401, 走既有的凭据熔断与换成员逻辑 —— 那条路已经能处理"这把凭据此刻不能用"。
// 而这个接口(APIKeyProvider.Get)本身不允许返回 error, 也没有地方上报。
func TestChannelKeyTokenOfBrokenAccountReturnsEmpty(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             "not-json",
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformCodex,
	}}
	if got := channelKeyTokenOf(key); got != "" {
		t.Fatalf("broken json = %q, want empty", got)
	}
}

// provider 只是把解析好的令牌原样给出, 不在 Get 里做任何 IO/刷新 ——
// 刷新是网络调用, 而 Get 在每轮请求的 TransformRequest 里同步执行。
func TestChannelKeyTokenProviderGetIsPure(t *testing.T) {
	provider := channelKeyTokenProvider{token: "tok"}
	for i := 0; i < 3; i++ {
		if got := provider.Get(context.Background()); got != "tok" {
			t.Fatalf("Get #%d = %q", i, got)
		}
	}
}

// Claude 的令牌在 claudeAiOauth 嵌套层里。转发取令牌必须能穿透这一层,
// 否则导入"成功"、转发时带空 Bearer 全部 401 —— 与 Codex 早期那个缺陷同一形状。
func TestChannelKeyTokenOfClaudeNested(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real","refreshToken":"rt","expiresAt":1900000000000}}`,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformClaude,
	}}
	if got := channelKeyTokenOf(key); got != "sk-ant-oat01-real" {
		t.Fatalf("claude token = %q, want sk-ant-oat01-real", got)
	}
}

// Kiro 的令牌是顶层 camelCase。
func TestChannelKeyTokenOfKiro(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             `{"accessToken":"kiro-at","refreshToken":"rt","clientId":"c","clientSecret":"s"}`,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformKiro,
	}}
	if got := channelKeyTokenOf(key); got != "kiro-at" {
		t.Fatalf("kiro token = %q", got)
	}
}

// cookie 类账号不是 OAuth: 它们的会话串要原样发出去(作为 Bearer 或按渠道自定义头),
// 不能去解析 accessToken —— 那会得到空串, 表现为"cookie 账号导入后完全不工作"。
func TestChannelKeyTokenOfCookieAccountPassesThrough(t *testing.T) {
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             "cookie-session-value",
		AccountKind:     model.AccountKindCookie,
		AccountPlatform: model.AccountPlatformGemini,
	}}
	if got := channelKeyTokenOf(key); got != "cookie-session-value" {
		t.Fatalf("cookie token = %q, want 原样透传", got)
	}
}
