package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestParseVideoRequestUnits 固定从请求体读取计费用量的规则: seconds 是字符串或数字都认,
// 分辨率档位优先取显式 resolution, 否则由 size 的短边推出 720p 这样的键。
func TestParseVideoRequestUnits(t *testing.T) {
	cases := []struct {
		name string
		body string
		want model.MediaUnits
	}{
		{"字符串时长与 size 推导", `{"model":"sora-2","seconds":"4","size":"1280x720"}`, model.MediaUnits{Seconds: 4, Resolution: "720p"}},
		{"数字时长", `{"model":"sora-2","seconds":8,"size":"720x1280"}`, model.MediaUnits{Seconds: 8, Resolution: "720p"}},
		{"显式 resolution 优先于 size", `{"seconds":"6","size":"1280x720","resolution":"1080p"}`, model.MediaUnits{Seconds: 6, Resolution: "1080p"}},
		{"缺时长", `{"model":"sora-2","size":"1280x720"}`, model.MediaUnits{Resolution: "720p"}},
		{"非法时长按零", `{"seconds":"abc","size":"1280x720"}`, model.MediaUnits{Resolution: "720p"}},
		{"无分辨率线索", `{"seconds":"4"}`, model.MediaUnits{Seconds: 4}},
		{"非法正文", `not json`, model.MediaUnits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseVideoRequestUnits([]byte(tc.body)); got != tc.want {
				t.Fatalf("parseVideoRequestUnits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestMediaVideoUnits 固定结算用量的来源: 上游回显的时长优先, 缺 id 视为没拿到任务对象而不计费。
func TestMediaVideoUnits(t *testing.T) {
	requested := model.MediaUnits{Seconds: 4, Resolution: "720p"}
	cases := []struct {
		name string
		body string
		want model.MediaUnits
	}{
		{"上游回显时长优先", `{"id":"video_1","status":"queued","seconds":"8","size":"1280x720"}`, model.MediaUnits{Videos: 1, Seconds: 8, Resolution: "720p"}},
		{"上游未回显时长时回落到请求声明值", `{"id":"video_1","status":"queued"}`, model.MediaUnits{Videos: 1, Seconds: 4, Resolution: "720p"}},
		{"缺 id 视为未创建成功", `{"error":{"message":"boom"}}`, model.MediaUnits{}},
		{"空正文", ``, model.MediaUnits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaVideoUnits(tc.body, requested); got != tc.want {
				t.Fatalf("mediaVideoUnits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestVideoResolutionKey 固定分辨率归一: 定价表按 480p / 720p / 1080p 这类键填写, 竖屏与横屏必须归到同一个键。
func TestVideoResolutionKey(t *testing.T) {
	cases := []struct{ resolution, size, want string }{
		{"", "1280x720", "720p"},
		{"", "720x1280", "720p"},
		{"", "1920x1080", "1080p"},
		{"1080p", "1280x720", "1080p"},
		{"", "", ""},
		{"", "720", ""},
		{"", "axb", ""},
	}
	for _, tc := range cases {
		if got := videoResolutionKey(tc.resolution, tc.size); got != tc.want {
			t.Fatalf("videoResolutionKey(%q, %q) = %q, want %q", tc.resolution, tc.size, got, tc.want)
		}
	}
}

// TestIsMediaProtocol 媒体协议按张或按秒计费, 限流估算必须把它们与 token 请求区分开。
func TestIsMediaProtocol(t *testing.T) {
	if !isMediaProtocol(model.ProtocolOpenAIImage) || !isMediaProtocol(model.ProtocolOpenAIVideo) {
		t.Fatal("image and video protocols must be treated as media")
	}
	if isMediaProtocol(model.ProtocolOpenAIChatCompletion) {
		t.Fatal("chat completion must not be treated as media")
	}
}

// TestVideoReserveSeconds 预扣时长优先用客户端声明值, 未声明才回落到保守默认值。
func TestVideoReserveSeconds(t *testing.T) {
	if got := videoReserveSeconds(4); got != 4 {
		t.Fatalf("declared seconds must be used, got %v", got)
	}
	if got := videoReserveSeconds(0); got != defaultVideoReserveSeconds {
		t.Fatalf("missing seconds must fall back to %v, got %v", defaultVideoReserveSeconds, got)
	}
}
