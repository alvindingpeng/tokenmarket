package netguard

import (
	"context"
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range cases {
		if got := IsPrivateIP(net.ParseIP(tc.ip)); got != tc.want {
			t.Fatalf("IsPrivateIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestCheckURLRejectsPrivateAndNonHTTP(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		"http://127.0.0.1:8080/v1",
		"http://[::1]:9000",
		"http://169.254.169.254/latest/meta-data",
		"http://192.168.1.10:11434/v1",
		"http://10.0.0.1",
		"file:///etc/passwd",
		"gopher://127.0.0.1:70",
	} {
		if err := CheckURL(ctx, raw, false); err == nil {
			t.Fatalf("CheckURL(%q) should be rejected", raw)
		}
	}
	// 公网地址放行(不发起网络请求: 直接给 IP 字面量)。
	if err := CheckURL(ctx, "https://api.openai.com:443", false); err != nil {
		t.Fatalf("public literal IP-free host should pass save-time check: %v", err)
	}
	if err := CheckURL(ctx, "http://8.8.8.8/v1", false); err != nil {
		t.Fatalf("public IP should pass: %v", err)
	}
	// 允许私网时全部放行(管理员例外开关)。
	if err := CheckURL(ctx, "http://192.168.1.10:11434/v1", true); err != nil {
		t.Fatalf("allow-private should pass: %v", err)
	}
}
