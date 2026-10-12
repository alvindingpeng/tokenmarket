package op

import (
	"context"
	"testing"
)

// TestSendMultiChannelAlertUsesInjectedDispatcher 校验多渠道外发真的走到了注入的分发器。
// 这里曾是静默失效点: sendMultiChannelAlert 是空占位函数, 配好的 Telegram/Email 一条都发不出去,
// 而设置页的连通性测试却是通的, 故障只会在"该收到告警时收不到"时才暴露。
func TestSendMultiChannelAlertUsesInjectedDispatcher(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()

	var gotEvent, gotTitle, gotMessage string
	calls := 0
	AlertDispatcher = func(_ context.Context, event, title, message string) bool {
		calls++
		gotEvent, gotTitle, gotMessage = event, title, message
		return true
	}

	if ok := sendMultiChannelAlert(context.Background(), "balance_low", "余额不足告警", "用户 x 可用余额不足"); !ok {
		t.Fatalf("分发成功但返回值未透传")
	}
	if calls != 1 {
		t.Fatalf("分发器调用次数 = %d, want 1", calls)
	}
	if gotEvent != "balance_low" || gotTitle != "余额不足告警" || gotMessage != "用户 x 可用余额不足" {
		t.Fatalf("分发内容被改写: event=%q title=%q message=%q", gotEvent, gotTitle, gotMessage)
	}
}

// TestSendMultiChannelAlertReportsFailure 校验分发失败如实返回 false。
// 该返回值决定 notified 标记是否回填, 谎报成功会让界面显示"已通知"而实际没发出去。
func TestSendMultiChannelAlertReportsFailure(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()
	AlertDispatcher = func(context.Context, string, string, string) bool { return false }

	if ok := sendMultiChannelAlert(context.Background(), "channel_failed", "t", "m"); ok {
		t.Fatalf("分发失败却返回 true")
	}
}

// TestSendMultiChannelAlertWithoutDispatcherIsSafe 校验未接线时静默跳过而不是 panic。
// 单测与未经 cmd 启动的进程都不该因为告警外发未接线而崩。
func TestSendMultiChannelAlertWithoutDispatcherIsSafe(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()
	AlertDispatcher = nil
	if ok := sendMultiChannelAlert(context.Background(), "channel_failed", "t", "m"); ok {
		t.Fatalf("未接线却报告发送成功")
	}
}

// TestDetermineAlertEvent 校验事件名映射: 未知 kind 不能掉成空串, 否则外发内容缺字段。
func TestDetermineAlertEvent(t *testing.T) {
	cases := map[string]string{
		"down":        "channel_failed",
		"recovered":   "channel_recovered",
		"low_balance": "channel_degraded",
	}
	for kind, want := range cases {
		if got := determineAlertEvent(kind); got != want {
			t.Fatalf("determineAlertEvent(%q) = %q, want %q", kind, got, want)
		}
	}
}
