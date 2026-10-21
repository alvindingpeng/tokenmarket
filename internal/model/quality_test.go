package model

import "testing"

// TestQualityGradeOf 覆盖评级的三个维度与边界:
// 样本不足不评级、延迟分档、探测缺失时权重归一、A 级还额外要求成功率 99.5%。
func TestQualityGradeOf(t *testing.T) {
	cases := []struct {
		name         string
		successRate  float64
		avgWaitMs    int64
		uptimePct    float64
		requestCount int64
		healthChecks int64
		wantGrade    string
	}{
		{"样本不足不评级", 1.0, 100, 100, 29, 10, ""},
		{"完美渠道为 A", 1.0, 500, 100, 100, 10, QualityGradeA},
		// 成功率 99.6% 但延迟 3.5s: 成功率 99.6 + 延迟 (5000-3500)/3000*100=50 → 0.4*99.6+0.35*50+0.25*100 = 39.84+17.5+25=82.34 → B
		{"高成功率但延迟中等为 B", 0.996, 3500, 100, 100, 10, QualityGradeB},
		{"低成功率为 D", 0.5, 4000, 50, 100, 10, QualityGradeD},
		// 探测未开启(healthChecks=0): 权重按 0.75 归一, 满分仍可到 100, 不该因没开探测就得 D。
		{"无探测时权重归一", 1.0, 500, 0, 100, 0, QualityGradeA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, grade := QualityGradeOf(tc.successRate, tc.avgWaitMs, tc.uptimePct, tc.requestCount, tc.healthChecks)
			if grade != tc.wantGrade {
				t.Fatalf("grade = %q, want %q", grade, tc.wantGrade)
			}
		})
	}
}

// TestQualityGradeOfNoHealthNormalization 单独验证「没开探测的渠道也能拿 A」:
// 若不归一权重, 三维只算两维最高 75 分, 会被误判成 B。
func TestQualityGradeOfNoHealthNormalization(t *testing.T) {
	score, grade := QualityGradeOf(1.0, 100, 0, 100, 0)
	if grade != QualityGradeA || score < 99 {
		t.Fatalf("score=%.2f grade=%q, want near 100 and A", score, grade)
	}
}

// TestQualityGradeARequiresHighSuccess 验证 A 级的额外门槛: 综合分够但成功率不到 99.5% 只能是 B。
func TestQualityGradeARequiresHighSuccess(t *testing.T) {
	// 成功率 99.0% + 延迟满分 + 可用率满分 → 0.4*99 + 0.35*100 + 0.25*100 = 39.6+35+25 = 99.6 分, 但成功率不足 99.5%
	score, grade := QualityGradeOf(0.99, 100, 100, 100, 10)
	if score < 90 {
		t.Fatalf("score=%.2f, expected >= 90 for this case", score)
	}
	if grade != QualityGradeB {
		t.Fatalf("grade = %q, want %q (success rate below 99.5%%)", grade, QualityGradeB)
	}
}

// TestLatencyDimension 覆盖延迟维度的三段分界: 2 秒内满分, 5 秒归零, 中间线性。
func TestLatencyDimension(t *testing.T) {
	cases := []struct {
		ms   int64
		want float64
	}{
		{0, 100},
		{2000, 100},
		{3500, 50},
		{5000, 0},
		{9000, 0},
	}
	for _, tc := range cases {
		if got := latencyDimension(tc.ms); got != tc.want {
			t.Fatalf("latencyDimension(%d) = %.2f, want %.2f", tc.ms, got, tc.want)
		}
	}
}

// TestQualityBands 验证对客脱敏区间: 分享页只出区间, 不出精确值。
func TestQualityBands(t *testing.T) {
	if got := QualityBandUptime(99.95); got != "99.9%+" {
		t.Fatalf("uptime band = %q", got)
	}
	if got := QualityBandUptime(96); got != "95%+" {
		t.Fatalf("uptime band = %q", got)
	}
	if got := QualityBandLatency(1500); got != "<2s" {
		t.Fatalf("latency band = %q", got)
	}
	if got := QualityBandLatency(6000); got != ">5s" {
		t.Fatalf("latency band = %q", got)
	}
	if got := QualityBandSuccess(0.996); got != "99.5%+" {
		t.Fatalf("success band = %q", got)
	}
	if got := QualityBandSuccess(0.9); got != "<95%" {
		t.Fatalf("success band = %q", got)
	}
}
