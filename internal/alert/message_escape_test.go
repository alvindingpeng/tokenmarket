package alert

import (
	"strings"
	"testing"
	"time"
)

// TestTelegramTextEscapesBody 正文里的下划线也必须转义。
// 线上实测: 渠道探测错误文本 "authorization_failed (HTTP 403)" 里的裸下划线被 Telegram
// legacy Markdown 当作斜体起点, 整条告警回 400 "Can't parse entities: Can't find end of
// the entity starting at byte offset 275", 告警发不出去(2026-11-02 当天 15 条未送达)。
func TestTelegramTextEscapesBody(t *testing.T) {
	a := Alert{
		Kind:      "down",
		Scope:     "TntAPI-deepseek",
		Timestamp: time.Date(2026, 11, 2, 20, 6, 10, 0, time.UTC),
		Fields: []Field{
			{Label: "渠道", Value: "TntAPI-deepseek"},
			{Label: "状态", Value: "HTTP 403"},
			{Label: "探测错误", Value: "authorization_failed (HTTP 403)"},
		},
	}
	got := telegramText(a)
	if strings.Contains(got, "authorization_failed") {
		t.Fatalf("正文下划线未转义, Telegram 会拒绝整条消息:\n%s", got)
	}
	if !strings.Contains(got, "authorization\\_failed") {
		t.Fatalf("期望转义后的 authorization\\_failed:\n%s", got)
	}
}
