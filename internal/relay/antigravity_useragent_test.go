package relay

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// TestAntigravityUserAgentNotLegacy 钉住默认版本号不再是会被闸门拦下的旧值。
//
// 上游对流式请求做客户端版本闸门: 旧版本返回 200 但正文被替换成
// "This version of Antigravity is no longer supported"。状态码是成功的,
// 客户端只会看到一段提示文案, 因此这条只能靠版本号本身来守。
func TestAntigravityUserAgentNotLegacy(t *testing.T) {
	initRelayAgentTestDB(t)
	ua := antigravityUserAgent()
	if ua == "antigravity/1.1.0 windows/amd64" {
		t.Fatalf("User-Agent 仍是实测被闸门拦下的旧版本: %q", ua)
	}
	if !strings.HasPrefix(ua, "antigravity/") {
		t.Fatalf("User-Agent 形状不对: %q", ua)
	}
}

// TestAntigravityUserAgentFollowsSetting 设置项必须能覆盖版本号。
//
// 上游的闸门是分批灰度且随发版变化(实测 1.100.0/1.200.0/2.0.0 通过,
// 1.99.9/2.5.0 被拒), 写死的版本号迟早会被拦; 能在设置页改版本是不被卡死的唯一办法。
func TestAntigravityUserAgentFollowsSetting(t *testing.T) {
	initRelayAgentTestDB(t)
	if err := op.SettingUpsert(model.SettingKeyAntigravityClientVersion, "9.9.9"); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}
	ua := antigravityUserAgent()
	want := "antigravity/9.9.9 windows/amd64"
	if ua != want {
		t.Fatalf("User-Agent = %q, want %q(设置项未生效)", ua, want)
	}
}

// TestAntigravityUserAgentFallsBackOnBlankSetting 设置为空时回落内置默认值。
//
// 空值不该产出 "antigravity/ windows/amd64" 这种残缺头: 上游只会拒掉它,
// 而失败原因指向"请求非法"而不是"设置没填"。
func TestAntigravityUserAgentFallsBackOnBlankSetting(t *testing.T) {
	initRelayAgentTestDB(t)
	if err := op.SettingUpsert(model.SettingKeyAntigravityClientVersion, "   "); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}
	ua := antigravityUserAgent()
	if ua != "antigravity/"+antigravityUserAgentVersion+" windows/amd64" {
		t.Fatalf("空设置应回落内置默认版本: got %q", ua)
	}
}

func initRelayAgentTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "agent_ua.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
}
