package xfyun

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignURL(t *testing.T) {
	got, err := SignURL("wss://example.com/v1.1/chat", Credentials{APIKey: "key", APISecret: "secret"}, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("host") != "example.com" {
		t.Fatalf("host = %q", u.Query().Get("host"))
	}
	if !strings.Contains(RedactURL(got), "wss://example.com/v1.1/chat") || strings.Contains(RedactURL(got), "authorization=") {
		t.Fatalf("redaction failed: %s", RedactURL(got))
	}
	raw, err := base64.StdEncoding.DecodeString(u.Query().Get("authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "api_key=\"key\"") {
		t.Fatalf("authorization missing key")
	}
}

func TestEndpointFromBaseURL(t *testing.T) {
	cases := []struct{ base, domain, want string }{
		// MaaS 平台(maas-api.*): WebSocket 路径固定 /v1.1/chat, 服务版本由帧内 domain 决定。
		{"https://maas-api.cn-huabei-1.xf-yun.com", "generalv3.5", "wss://maas-api.cn-huabei-1.xf-yun.com/v1.1/chat"},
		{"https://maas-api.cn-huabei-1.xf-yun.com", "4.0Ultra", "wss://maas-api.cn-huabei-1.xf-yun.com/v1.1/chat"},
		// 经典开放平台(spark-api.*): 按 domain 生成版本路径。
		{"https://spark-api.xf-yun.com", "generalv3.5", "wss://spark-api.xf-yun.com/v3.5/chat"},
		{"https://spark-api.xf-yun.com", "lite", "wss://spark-api.xf-yun.com/v1.1/chat"},
		{"https://spark-api.xf-yun.com", "max-32k", "wss://spark-api.xf-yun.com/chat/max-32k"},
		{"https://spark-api.xf-yun.com", "pro-128k", "wss://spark-api.xf-yun.com/chat/pro-128k"},
		// 基地址带路径时按原样使用(控制台给出的完整端点优先于任何映射)。
		{"wss://maas-api.cn-huabei-1.xf-yun.com/v1.1/chat", "generalv3", "wss://maas-api.cn-huabei-1.xf-yun.com/v1.1/chat"},
		{"https://spark-api.xf-yun.com/v4.0/chat", "4.0Ultra", "wss://spark-api.xf-yun.com/v4.0/chat"},
	}
	for _, c := range cases {
		got, err := EndpointFromBaseURL(c.base, c.domain)
		if err != nil {
			t.Fatalf("EndpointFromBaseURL(%q, %q): %v", c.base, c.domain, err)
		}
		if got != c.want {
			t.Fatalf("EndpointFromBaseURL(%q, %q) = %q, want %q", c.base, c.domain, got, c.want)
		}
	}
}
