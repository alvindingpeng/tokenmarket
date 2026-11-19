package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// fakeGroupServer 起一个假的群机器人服务: 校验请求体形状并回一个指定响应。
//
// 三个渠道的真机行为有一个共同的坑: 参数错误、签名失败、关键词不匹配时它们返回
// HTTP 200 + 业务错误码。只看状态码会把"根本没发出去"记成送达, 补发机制永不触发。
// 因此这里用真实 HTTP 交互验证"200 但 code != 0 必须判失败"这条路径。
func fakeGroupServer(t *testing.T, wantField string, response string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if _, ok := payload[wantField]; !ok {
			t.Errorf("payload missing field %q: %v", wantField, payload)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server
}

// withSettings 注入一组设置值, 测试结束自动还原。
func withSettings(t *testing.T, values map[model.SettingKey]string) {
	t.Helper()
	previous := SettingsProvider
	SettingsProvider = func(key model.SettingKey) (string, error) {
		return values[key], nil
	}
	t.Cleanup(func() { SettingsProvider = previous })
}

func groupTestAlert() Alert {
	return Alert{
		Event:     EventTest,
		Kind:      "test",
		Scope:     "渠道A",
		Timestamp: time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC),
	}
}

func TestSendFeishuSucceeds(t *testing.T) {
	server := fakeGroupServer(t, "msg_type", `{"code":0,"msg":"success"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertFeishuWebhook: server.URL})
	if err := sendFeishu(context.Background(), groupTestAlert()); err != nil {
		t.Fatalf("feishu send: %v", err)
	}
}

func TestSendFeishuRejectsBusinessError(t *testing.T) {
	// 飞书在关键词不匹配时回 HTTP 200 + code=19024, 必须判失败。
	server := fakeGroupServer(t, "msg_type", `{"code":19024,"msg":"Key Words Not Found"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertFeishuWebhook: server.URL})
	err := sendFeishu(context.Background(), groupTestAlert())
	if err == nil {
		t.Fatal("200 但 code=19024 必须判失败, 否则告警被静默丢弃")
	}
	if !strings.Contains(err.Error(), "19024") {
		t.Fatalf("error should carry feishu code, got %v", err)
	}
}

func TestSendFeishuAcceptsLegacyStatusCode(t *testing.T) {
	// 旧版飞书机器人只回 StatusCode, 不能因为 code 缺省为 0 就误判成功。
	server := fakeGroupServer(t, "msg_type", `{"StatusCode":9499,"StatusMessage":"Bad Request"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertFeishuWebhook: server.URL})
	if err := sendFeishu(context.Background(), groupTestAlert()); err == nil {
		t.Fatal("StatusCode 非 0 必须判失败")
	}
}

func TestSendDingtalkSucceeds(t *testing.T) {
	server := fakeGroupServer(t, "msgtype", `{"errcode":0,"errmsg":"ok"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertDingtalkWebhook: server.URL})
	if err := sendDingtalk(context.Background(), groupTestAlert()); err != nil {
		t.Fatalf("dingtalk send: %v", err)
	}
}

func TestSendDingtalkRejectsBusinessError(t *testing.T) {
	// 钉钉签名失败/关键词不匹配: HTTP 200 + errcode=310000。
	server := fakeGroupServer(t, "msgtype", `{"errcode":310000,"errmsg":"sign not match"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertDingtalkWebhook: server.URL})
	err := sendDingtalk(context.Background(), groupTestAlert())
	if err == nil {
		t.Fatal("200 但 errcode=310000 必须判失败")
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Fatalf("error should carry dingtalk errcode, got %v", err)
	}
}

func TestSendWecomSucceeds(t *testing.T) {
	server := fakeGroupServer(t, "msgtype", `{"errcode":0,"errmsg":"ok"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertWecomWebhook: server.URL})
	if err := sendWecom(context.Background(), groupTestAlert()); err != nil {
		t.Fatalf("wecom send: %v", err)
	}
}

func TestSendWecomRejectsBusinessError(t *testing.T) {
	// 企业微信地址失效回 HTTP 200 + errcode=93000。
	server := fakeGroupServer(t, "msgtype", `{"errcode":93000,"errmsg":"invalid webhook url"}`)
	withSettings(t, map[model.SettingKey]string{model.SettingKeyAlertWecomWebhook: server.URL})
	if err := sendWecom(context.Background(), groupTestAlert()); err == nil {
		t.Fatal("200 但 errcode=93000 必须判失败")
	}
}

func TestGroupChannelsReportUnconfigured(t *testing.T) {
	// 未配置地址时必须明确报错而不是静默成功 —— 否则告警"发成功了"却没人收到。
	withSettings(t, map[model.SettingKey]string{})
	for name, fn := range map[string]func(context.Context, Alert) error{
		"feishu": sendFeishu, "dingtalk": sendDingtalk, "wecom": sendWecom,
	} {
		if err := fn(context.Background(), groupTestAlert()); err == nil {
			t.Fatalf("%s 未配置时必须报错", name)
		}
	}
}

func TestGroupTextIsPlain(t *testing.T) {
	// 群机器人按纯文本呈现, 不能像 Telegram 那样转义反斜杠:
	// 转义序列会被原样显示成 "channel\_failed", 反而更难读。
	alert := Alert{Kind: "down", Scope: "渠道A", Fields: []Field{{Label: "原因", Value: "upstream_failed (HTTP 502)"}}, Timestamp: time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC)}
	text := groupText(alert)
	if strings.Contains(text, "\\_") || strings.Contains(text, "\\*") {
		t.Fatalf("群机器人正文不应做 Markdown 转义: %q", text)
	}
	if !strings.Contains(text, "upstream_failed (HTTP 502)") {
		t.Fatalf("正文丢失了原始错误: %q", text)
	}
}
