package op

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 首刷失败是常态(上游不通 / 账号到期 / 地区限制), 而那条路径会提前返回 warning。
// 缓存写入若在刷新之后, 首刷一失败就永远走不到 —— 库里有行、详情看不见、转发选不中,
// 使用者看到的是"导入成功但渠道里就是没有这个账号"。
// 这条测试钉的就是这个顺序。
func TestAccountImportFirstRefreshFailureStillCaches(t *testing.T) {
	accountTestDB(t)
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("allow private upstream: %v", err)
	}
	channel := seedImportChannel(t)

	// 端点拒绝刷新(400 invalid_grant 之外的普通失败也走同一路径, 这里用 500 模拟上游故障)。
	server := accountFakeTokenServer(t, http.StatusInternalServerError,
		map[string]any{"error": "upstream_down"}, nil)
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformClaude, server.URL)
	t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformClaude, "") })

	raw := `{"claudeAiOauth":{"accessToken":"old-a","refreshToken":"old-r","expiresAt":1700000000000}}`
	result, err := AccountImport(context.Background(), channel.ID,
		AccountImportSpec{Platform: model.AccountPlatformClaude, Name: "claude-cache", Raw: raw, Enabled: true}, time.Now())

	// 首刷失败: 应当返回 warning(而不是硬失败), 且 RefreshedNow 为 false。
	var warning *AccountImportRefreshWarning
	if !errors.As(err, &warning) {
		t.Fatalf("首刷失败应返回 warning, got %v", err)
	}
	if result == nil {
		t.Fatal("首刷失败时仍应返回导入结果")
	}
	if result.RefreshedNow {
		t.Fatal("首刷失败不应标记 RefreshedNow")
	}

	// 关键断言: 即使首刷失败, 这条凭据也必须在缓存里可见。
	if _, ok := channelKeyCache.Get(result.ChannelKeyID); !ok {
		t.Fatal("首刷失败后凭据没有进缓存 —— 详情接口看不见, 转发也选不中")
	}

	// 详情接口(同一份缓存)必须能列出它, 否则使用者以为导入丢了。
	// 直接调 channelDetail —— 详情接口就是它的薄封装, 中间没有别的过滤。
	// 渠道本身也要在缓存里: 真实服务启动时 channelRefreshCache 会灌满, 测试里得手工补这一行。
	if _, ok := channelCache.Get(channel.ID); !ok {
		channelCache.Set(channel.ID, channel)
	}
	channel, ok := channelCache.Get(channel.ID)
	if !ok {
		t.Fatal("渠道不在缓存里")
	}
	detail := channelDetail(channel)
	found := false
	for _, key := range detail.Keys {
		if key.Name == "claude-cache" {
			found = true
			if key.AccountKind != model.AccountKindOAuth || key.AccountPlatform != model.AccountPlatformClaude {
				t.Fatalf("详情里的账号元数据不对: %+v", key)
			}
		}
	}
	if !found {
		t.Fatal("详情接口没有列出刚导入的账号")
	}
}
