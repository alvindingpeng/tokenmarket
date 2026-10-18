package op

import (
	"testing"
	"time"
)

// TestEscalatedBreakerDuration 固定递增规则: 第 n 次熔断时长 = 基准 × 2^(n-1), 封顶 maxMinutes。
func TestEscalatedBreakerDuration(t *testing.T) {
	base := 5 * time.Minute
	cases := []struct {
		trips int
		want  time.Duration
	}{
		{0, 5 * time.Minute},  // 0 视为首次, 不能算出 0 时长
		{1, 5 * time.Minute},  // 首次熔断就是基准时长
		{2, 10 * time.Minute}, // 第二次翻倍
		{3, 20 * time.Minute},
		{4, 40 * time.Minute},
		{5, 60 * time.Minute},  // 80 分钟超过上限, 取 60
		{50, 60 * time.Minute}, // 大次数不溢出, 直接落在上限
	}
	for _, c := range cases {
		if got := escalatedBreakerDuration(base, c.trips, 60); got != c.want {
			t.Errorf("trips=%d: got %v, want %v", c.trips, got, c.want)
		}
	}
}

// TestEscalatedBreakerDurationCapsAtMax 上限低于基准时长时以上限为准, 不反过来放大。
func TestEscalatedBreakerDurationCapsAtMax(t *testing.T) {
	if got := escalatedBreakerDuration(30*time.Minute, 1, 10); got != 10*time.Minute {
		t.Fatalf("got %v, want 10m", got)
	}
	if got := escalatedBreakerDuration(30*time.Minute, 3, 10); got != 10*time.Minute {
		t.Fatalf("got %v, want 10m", got)
	}
}

// TestEscalatedBreakerDurationNeverZero 基准时长非法为 0 时也不能算出 0 —— 0 时长等于不熔断。
func TestEscalatedBreakerDurationNeverZero(t *testing.T) {
	if got := escalatedBreakerDuration(0, 1, 60); got != 0 {
		t.Fatalf("基准为 0 时结果为 0(调用方保证基准 >= 1 分钟), 实得 %v", got)
	}
}
