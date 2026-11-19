package xstrings

import (
	"errors"
	"net/url"
	"testing"
)

// TestRedactURLCredentials 凭据必须从 URL 里剥掉, 且不能误伤正常地址。
//
// 存在的理由: url.Parse 失败时返回的 *url.Error 会把原始字符串整个带进 Error(),
// 于是 "parse "http://user:pw@[::1": missing ']' in host" 会连同密码一起经 HTTP 响应
// 回到浏览器 —— 代理地址常用 URL 内嵌凭据, 诊断信息里不该有它。
func TestRedactURLCredentials(t *testing.T) {
	cases := map[string]string{
		"http://user:pw@1.2.3.4:8080":     "http://1.2.3.4:8080",
		"http://user:pw@[::1":             "http://[::1",
		"socks5://u:p@1.2.3.4:1080":       "socks5://1.2.3.4:1080",
		"http://user:p%40ss@1.2.3.4:8080": "http://1.2.3.4:8080",
		"user:pw@1.2.3.4:1080":            "1.2.3.4:1080",
		"http://u:p@a@1.2.3.4:8080":       "http://1.2.3.4:8080", // 多个 @ 只保留最后一个之后
		// 以下都不得被改动。
		"http://1.2.3.4:8080":     "http://1.2.3.4:8080",
		"https://api.example.com": "https://api.example.com",
		"http://host/path@x":      "http://host/path@x", // 路径里的 @ 不是凭据
		"":                        "",
	}
	for in, want := range cases {
		if got := RedactURLCredentials(in); got != want {
			t.Errorf("RedactURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestURLParseReason 解析失败的原因里不能带原始地址。
func TestURLParseReason(t *testing.T) {
	_, err := url.Parse("http://user:secret@[::1")
	if err == nil {
		t.Fatal("该地址应解析失败")
	}
	// 原始错误确实带地址与凭据 —— 这正是必须换掉它的原因。
	if got := err.Error(); !contains(got, "secret") {
		t.Fatalf("前提不成立: url.Error 未携带原始地址: %q", got)
	}
	reason := URLParseReason(err)
	if contains(reason.Error(), "secret") || contains(reason.Error(), "user") {
		t.Errorf("底层原因仍带凭据: %q", reason.Error())
	}
	if reason.Error() == "" {
		t.Error("底层原因不应为空")
	}

	// nil 与普通错误都要有确定行为。
	if URLParseReason(nil) == nil {
		t.Error("nil 应返回一个可展示的错误")
	}
	plain := errors.New("boom")
	if URLParseReason(plain) != plain {
		t.Error("非 url.Error 应原样返回")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
