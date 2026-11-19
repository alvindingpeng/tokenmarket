package op

import (
	"testing"
)

func TestTruncateReason(t *testing.T) {
	short := "channel healthy again"
	if got := truncateReason(short); got != short {
		t.Errorf("short reason should pass through, got %q", got)
	}
	long := make([]rune, 600)
	for i := range long {
		long[i] = 'x'
	}
	got := truncateReason(string(long))
	if len([]rune(got)) != 514 { // 500 + "...(truncated)" 14 位后缀
		t.Errorf("expected truncation to 500 runes + suffix, got %d runes", len([]rune(got)))
	}
}

func TestAlertWebhookConfigured(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"", false},
		{"https://hooks.example.com/alert", true},
		{"http://127.0.0.1:9/alert", true},
		{"ftp://example.com", false},
		{"https://", false},
		{"://bad", false},
		{"hooks.example.com/alert", false},
	}
	for _, tc := range cases {
		if got := alertWebhookConfigured(tc.url); got != tc.want {
			t.Errorf("alertWebhookConfigured(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestAlertFailStreakDefault(t *testing.T) {
	// 抖动抑制阈值: 未配置 alert_fail_streak 时回退到 5 次连续失败才判 down, 与文档保持一致。
	if alertFailStreakDefault != 5 {
		t.Errorf("alertFailStreakDefault should stay 5, got %d", alertFailStreakDefault)
	}
}

func TestAlertDedupDefault(t *testing.T) {
	// 去重窗口默认 10 分钟: 未配置 alert_dedup_minutes 时同故障 10 分钟内不重复告警。
	if alertDedupDefaultMinutes != 10 {
		t.Errorf("alertDedupDefaultMinutes should stay 10, got %d", alertDedupDefaultMinutes)
	}
}
