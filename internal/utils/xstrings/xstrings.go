package xstrings

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// SplitTrimCompact splits strings by sep, trims whitespace, and drops empty items.
// Commonly used for parsing comma-separated configs like "a, b, ,c,".
func SplitTrimCompact(sep string, parts ...string) []string {
	out := make([]string, 0)
	for _, p := range parts {
		if p == "" {
			continue
		}
		for _, item := range strings.Split(p, sep) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
	}
	return out
}

// SplitCompact 按分隔符拆分非空字符串，不修改或过滤拆分后的内容。
func SplitCompact(sep string, parts ...string) []string {
	out := make([]string, 0)
	for _, part := range parts {
		if part != "" {
			out = append(out, strings.Split(part, sep)...)
		}
	}
	return out
}

// TrimCompact trims whitespace and drops empty items in a string slice.
func TrimCompact(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// urlUserInfoPattern 匹配 URL 的凭据前缀段(scheme 可选)。
//
// 取的是**第一个 "/" 之前的最后一个 "@"**: 按 RFC, 最后一个 @ 才分隔 userinfo 与 host,
// 所以 http://u:p@a@1.2.3.4:8080 的用户名是 "u:p@a" —— 只吃到第一个 @ 会残留 "a@"。
// 用 [^/]* 而不是 .* 是为了不越过路径: http://host/path@x 里的 @ 不是凭据, 不能剥掉。
// 不带 @ 的普通地址(如 http://1.2.3.4:8080)不匹配, 原样返回。
var urlUserInfoPattern = regexp.MustCompile("^(?:([a-zA-Z][a-zA-Z0-9+.-]*://))?[^/]*@")

// RedactURLCredentials 剥掉 URL 里的 "user:pass@" 段, 没有凭据时原样返回。
//
// 存在的理由: url.Parse 失败时返回的 *url.Error 会把原始字符串整个带进 Error(), 于是
// 形如 parse "http://user:pw@[::1": missing ']' in host 的消息会连同密码一起
// 经 HTTP 响应回到浏览器, 也可能落进日志。代理地址常用 URL 内嵌凭据, 诊断信息里不该有它。
//
// 刻意不先调 url.Parse: 这个函数正是给"解析已经失败"的场景用的。
func RedactURLCredentials(raw string) string {
	return urlUserInfoPattern.ReplaceAllString(strings.TrimSpace(raw), "$1")
}

// URLParseReason 取出 url.Parse 失败的底层原因, 丢掉它携带的原始地址。
//
// url.Error.Error() 形如 parse "<URL>": <原因>, 其中 <URL> 可能内嵌 user:pass;
// 只取 <原因> 既能说清问题, 又不会把凭据带进诊断信息。
func URLParseReason(err error) error {
	if err == nil {
		return errors.New("invalid url")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	// 非 url.Error 的错误本来就不携带地址, 原样返回。
	return err
}
