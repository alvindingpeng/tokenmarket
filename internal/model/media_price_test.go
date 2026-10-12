package model

import "testing"

// TestMediaPriceCostVideos 固定生视频的计费口径: 每条固定起步价 + 时长 × 每秒单价,
// 分辨率分档只覆盖每秒单价, 不动与时长无关的起步价。
func TestMediaPriceCostVideos(t *testing.T) {
	price := MediaPrice{PerVideo: 0.5, PerSecond: 0.1, Resolution: map[string]float64{"1080p": 0.3}}
	cases := []struct {
		name       string
		videos     int64
		seconds    float64
		resolution string
		want       float64
	}{
		{"起步价加时长价", 1, 4, "", 0.9},
		{"分辨率分档覆盖每秒价", 1, 4, "1080p", 1.7},
		{"未命中的分档回落到每秒价", 1, 4, "480p", 0.9},
		{"无产出不计费", 0, 4, "", 0},
		{"零时长只收起步价", 1, 0, "", 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := price.CostVideos(tc.videos, tc.seconds, tc.resolution); got != tc.want {
				t.Fatalf("CostVideos = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMediaPriceCostVideosWithoutPerSecond 只有每条价时按条计价, 时长不影响费用。
func TestMediaPriceCostVideosWithoutPerSecond(t *testing.T) {
	price := MediaPrice{PerVideo: 2}
	for _, seconds := range []float64{0, 4, 60} {
		if got := price.CostVideos(1, seconds, ""); got != 2 {
			t.Fatalf("seconds=%v CostVideos = %v, want 2", seconds, got)
		}
	}
}

// TestMediaPriceMaxVideosPerRequest 预扣上限与结算同口径: 起步价 + 最大每秒单价 × 时长。
func TestMediaPriceMaxVideosPerRequest(t *testing.T) {
	price := MediaPrice{PerVideo: 0.5, PerSecond: 0.1, Resolution: map[string]float64{"1080p": 0.3}}
	if got := price.MaxVideosPerRequest(4); got != 0.5+0.3*4 {
		t.Fatalf("MaxVideosPerRequest = %v, want %v", got, 0.5+0.3*4)
	}
}

// TestMediaPriceScaleCoversPerVideo 上浮必须覆盖每条价, 否则视频模型的用户价会漏掉起步价。
func TestMediaPriceScaleCoversPerVideo(t *testing.T) {
	scaled := MediaPrice{PerImage: 1, PerVideo: 2, PerSecond: 3, Resolution: map[string]float64{"720p": 4}}.Scale(0.5)
	if scaled.PerVideo != 3 {
		t.Fatalf("PerVideo = %v, want 3", scaled.PerVideo)
	}
	if scaled.PerImage != 1.5 || scaled.PerSecond != 4.5 || scaled.Resolution["720p"] != 6 {
		t.Fatalf("unexpected scaled price: %+v", scaled)
	}
}
