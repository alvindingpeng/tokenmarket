package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// telegramCapture 记录被投递到"Telegram"的报文, 用于断言降级行为。
type telegramCapture struct {
	mu       sync.Mutex
	payloads []map[string]any
}

func (c *telegramCapture) all() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.payloads...)
}

// newTelegramServer 起一个假 Telegram: 带 parse_mode 的报文一律按实体解析失败拒绝,
// 纯文本报文正常返回; 可选 networkReset 让第一次请求直接断开连接。
func newTelegramServer(t *testing.T, networkReset bool) (*httptest.Server, *telegramCapture) {
	t.Helper()
	capture := &telegramCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		capture.mu.Lock()
		capture.payloads = append(capture.payloads, payload)
		count := len(capture.payloads)
		capture.mu.Unlock()
		if networkReset && count == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		if _, ok := payload["parse_mode"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 275"}`)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

// TestSendTelegramFallsBackToPlainText 实体解析失败时必须降级为纯文本重发。
// 线上实测: 正文未转义的下划线让 Telegram 回 400 "Can't parse entities", 告警静默丢失。
func TestSendTelegramFallsBackToPlainText(t *testing.T) {
	srv, capture := newTelegramServer(t, false)
	alert := Alert{
		Kind:      "down",
		Scope:     "TntAPI-deepseek",
		Timestamp: time.Date(2026, 11, 2, 20, 6, 10, 0, time.UTC),
		Fields: []Field{
			{Label: "探测错误", Value: "authorization_failed (HTTP 403)"},
		},
	}
	url := srv.URL + "/botTOKEN/sendMessage"
	if err := postTelegramMessage(context.Background(), url, "123", telegramText(alert), "Markdown"); err == nil {
		t.Fatal("假 Telegram 应拒绝 Markdown 报文")
	}
	err := postTelegramMessage(context.Background(), url, "123", telegramPlainText(alert), "")
	if err != nil {
		t.Fatalf("纯文本重发应成功: %v", err)
	}
	payloads := capture.all()
	if len(payloads) != 2 {
		t.Fatalf("期望 2 次请求, got %d", len(payloads))
	}
	if _, ok := payloads[0]["parse_mode"]; !ok {
		t.Fatal("第一次应带 parse_mode")
	}
	if _, ok := payloads[1]["parse_mode"]; ok {
		t.Fatal("降级报文不应带 parse_mode")
	}
	text, _ := payloads[1]["text"].(string)
	if !strings.Contains(text, "authorization_failed (HTTP 403)") {
		t.Fatalf("纯文本正文应保留原始错误文本: %q", text)
	}
	if strings.Contains(text, `\_`) {
		t.Fatalf("纯文本正文不该残留转义反斜杠: %q", text)
	}
}

// TestIsTelegramEntityParseError 只对实体解析错误降级。
func TestIsTelegramEntityParseError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{telegramError(400, []byte(`{"ok":false,"description":"Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 275"}`)), true},
		{telegramError(400, []byte(`{"ok":false,"description":"Bad Request: chat not found"}`)), false},
		{telegramError(401, []byte(`{"ok":false,"description":"Unauthorized"}`)), false},
		{nil, false},
	}
	for i, tc := range cases {
		if got := isTelegramEntityParseError(tc.err); got != tc.want {
			t.Errorf("case %d: got %v want %v (err=%v)", i, got, tc.want, tc.err)
		}
	}
}

// TestTelegramNetworkErrorKeepsTokenRedacted 验证降级路径上的网络错误仍会被脱敏。
func TestTelegramNetworkErrorKeepsTokenRedacted(t *testing.T) {
	const token = "123456:secret-token"
	srv, _ := newTelegramServer(t, true)
	err := postTelegramMessage(context.Background(), srv.URL+"/bot"+token+"/sendMessage", "123", telegramText(Alert{Kind: "test"}), "Markdown")
	if err == nil {
		t.Fatal("断连应返回错误")
	}
	redacted := telegramRequestError(err, token)
	if strings.Contains(redacted.Error(), token) || strings.Contains(redacted.Error(), "secret-token") {
		t.Fatalf("降级路径泄露 bot token: %v", redacted)
	}
}

// TestSendTelegramEndToEndFallsBackToPlainText 走真实 sendTelegram 路径, 覆盖降级链路整体。
func TestSendTelegramEndToEndFallsBackToPlainText(t *testing.T) {
	srv, capture := newTelegramServer(t, false)
	origBase := telegramAPIBase
	origProvider := SettingsProvider
	defer func() {
		telegramAPIBase = origBase
		SettingsProvider = origProvider
	}()
	telegramAPIBase = srv.URL
	SettingsProvider = func(key model.SettingKey) (string, error) {
		switch key {
		case model.SettingKeyAlertTelegramBotToken:
			return "TOKEN", nil
		case model.SettingKeyAlertTelegramChatID:
			return "123", nil
		}
		return "", fmt.Errorf("setting not found")
	}

	alert := Alert{
		Kind:      "down",
		Scope:     "TntAPI-deepseek",
		Timestamp: time.Date(2026, 11, 2, 20, 6, 10, 0, time.UTC),
		Fields:    []Field{{Label: "探测错误", Value: "authorization_failed (HTTP 403)"}},
	}
	if err := sendTelegram(context.Background(), alert); err != nil {
		t.Fatalf("降级重发后应成功: %v", err)
	}
	payloads := capture.all()
	if len(payloads) != 2 {
		t.Fatalf("期望 2 次请求(Markdown 失败 + 纯文本成功), got %d", len(payloads))
	}
	if _, ok := payloads[1]["parse_mode"]; ok {
		t.Fatal("降级报文不应带 parse_mode")
	}
}
