package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestSendDispatchesToConfiguredChannels 校验按 alert_channels 选中的渠道真的收到了告警,
// 并如实回报发送成功。用本地 httptest 冒充 Webhook 出口, 断言恰好发出一条约定的请求。
func TestSendDispatchesToConfiguredChannels(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	original := SettingsProvider
	defer func() { SettingsProvider = original }()
	SettingsProvider = func(key model.SettingKey) (string, error) {
		switch key {
		case model.SettingKeyAlertChannels:
			return "webhook", nil
		case model.SettingKeyAlertWebhookURL:
			return server.URL, nil
		}
		return "", fmt.Errorf("setting not found")
	}

	if ok := Send(context.Background(), Alert{Event: EventBalanceLow, Title: "t", Message: "m"}); !ok {
		t.Fatalf("Webhook 发送成功却回报失败")
	}
	if hits != 1 {
		t.Fatalf("Webhook 收到 %d 次, want 1", hits)
	}
}

// TestSendSkipsChannelsNotEnabled 校验未启用的渠道不会外发, 且失败会如实回报。
func TestSendSkipsChannelsNotEnabled(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	original := SettingsProvider
	defer func() { SettingsProvider = original }()
	SettingsProvider = func(key model.SettingKey) (string, error) {
		switch key {
		case model.SettingKeyAlertChannels:
			return "telegram", nil // 只启用 telegram, Webhook 地址配了也不该发。
		case model.SettingKeyAlertWebhookURL:
			return server.URL, nil
		}
		return "", fmt.Errorf("setting not found")
	}

	// telegram 未配置 token, 发送必然失败, 故整体应回报失败。
	if ok := Send(context.Background(), Alert{Event: EventBalanceLow, Title: "t", Message: "m"}); ok {
		t.Fatalf("唯一渠道发送失败却回报成功")
	}
	if hits != 0 {
		t.Fatalf("未启用的 Webhook 仍被调用 %d 次", hits)
	}
}

// TestSendWithoutSettingsProviderIsSafe 校验未注入设置读取器时不 panic 且回报失败。
func TestSendWithoutSettingsProviderIsSafe(t *testing.T) {
	original := SettingsProvider
	defer func() { SettingsProvider = original }()
	SettingsProvider = nil
	if ok := Send(context.Background(), Alert{Event: EventBalanceLow, Title: "t", Message: "m"}); ok {
		t.Fatalf("无设置读取器却回报发送成功")
	}
}
