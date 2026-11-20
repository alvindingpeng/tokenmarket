package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// 该测试锁住一条真实故障: 渠道配了地区出口代理, 转发走代理正常, 但"拉取模型"
// 绕过出口池直连本机 IP, 被上游以 UNSUPPORTED_LOCATION 拒绝。
//
// 断言"请求确实经过了代理", 而非"返回了非 nil 的 client" —— 后者在回退成
// probeHTTPClient(channel.ChannelConfig) 时依然成立, 抓不住问题。
// setupEgressTestEnv 建测试库并放开私网许可: 出口代理在本机 httptest 起,
// 默认私网防护会把它拦掉 —— 那是生产环境的正确行为, 测试里要显式放开。
func setupEgressTestEnv(t *testing.T) {
	t.Helper()
	handlerTestResetCaches(t)
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "egress.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("allow private: %v", err)
	}
}

func TestProbeHTTPClientForChannelUsesEgress(t *testing.T) {
	setupEgressTestEnv(t)

	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer proxy.Close()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		Name:    "订阅渠道",
		Dialect: model.DialectSubscription,
	}}
	channel.ID = 40
	// 关键: 渠道自身没有代理(Proxy=false, ChannelProxy 为空), 唯一可用线路就是出口池。
	channel.Egresses = []model.ChannelEgress{{
		ID: 11, ChannelID: 40, Name: "egress-1",
		Kind:    model.EgressKindHTTP,
		URL:     proxy.URL,
		Enabled: true,
	}}

	client, err := probeHTTPClientForChannel(channel)
	if err != nil {
		t.Fatalf("构建客户端失败: %v", err)
	}
	resp, err := client.Get("http://example.invalid/probe")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()
	if got := proxied.Load(); got == 0 {
		t.Fatalf("请求没有经过出口代理(代理收到 0 次): 探测绕过了 egress 直连本机, 这正是生产上被上游按地区拒绝的原因")
	}
}

// 出口池为空时必须回退到老行为(按 Proxy/ChannelProxy 构建), 不能因新增分支出错。
func TestProbeHTTPClientForChannelFallsBackWithoutEgress(t *testing.T) {
	setupEgressTestEnv(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "普通渠道"}}
	client, err := probeHTTPClientForChannel(channel)
	if err != nil {
		t.Fatalf("无出口时应回退而不是报错: %v", err)
	}
	if client == nil {
		t.Fatal("必须返回可用客户端")
	}

	// 未开代理(Proxy=false)的老路径必须直连, 不能被新分支改坏。
	old, err := probeHTTPClient(channel.ChannelConfig)
	if err != nil {
		t.Fatalf("老路径构建失败: %v", err)
	}
	if old == nil {
		t.Fatal("老路径必须返回客户端")
	}
}
