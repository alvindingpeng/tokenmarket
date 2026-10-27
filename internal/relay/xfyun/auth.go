// Package xfyun 实现科大讯飞星火原生 WebSocket 协议的最小可用适配:
// HMAC 鉴权 URL 签名、请求/响应帧、带取消语义的 WebSocket 执行, 以及响应帧流转统一响应块
// (OpenAI chat.completion.chunk 语义, 经 relay 现有入站转换器输出客户端协议 SSE)。
//
// 凭据安全: APIKey / APISecret / AppID 一律不写日志。签名 URL 的 authorization 查询参数
// base64 解码即 api_key 与签名原文, 同样视作凭据, 任何错误、日志、请求快照都不得携带它;
// 需要展示地址时用 RedactURL, 需要展示凭据时用 Credentials.String()(自动脱敏)。
package xfyun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Credentials 是讯飞开放平台的一组凭据: AppID 进请求帧 header, APIKey/APISecret 只用于 URL 签名。
type Credentials struct {
	AppID     string
	APIKey    string
	APISecret string
}

// String 以脱敏形态输出, fmt/log 在 %v / %s 下会隐式调用它, 避免无意中打印凭据明文。
func (c Credentials) String() string {
	return fmt.Sprintf("xfyun.Credentials{app_id:%s, api_key:%s, api_secret:%s}",
		maskSecret(c.AppID), maskSecret(c.APIKey), maskSecret(c.APISecret))
}

// GoString 让 %#v 同样走脱敏。
func (c Credentials) GoString() string { return c.String() }

func maskSecret(v string) string {
	if v == "" {
		return "<empty>"
	}
	return "<redacted>"
}

// signDate 生成鉴权头里的 date: RFC1123 带 GMT(讯飞 v1 鉴权要求的字面量格式)。
func signDate(t time.Time) string {
	return t.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
}

// SignURL 按讯飞 v1 鉴权(HMAC-SHA256)为星火 WebSocket 端点签出可拨号的 URL。
//
// 签名规则(与讯飞官方 demo 一致):
//
//	signature_origin    = "host: {host}\ndate: {date}\nGET {path} HTTP/1.1"
//	signature           = base64(hmac_sha256(api_secret, signature_origin))
//	authorization_origin = "api_key=\"{api_key}\", algorithm=\"hmac-sha256\", date=\"{date}\", headers=\"host date request-line\", signature=\"{signature}\""
//	authorization       = base64(authorization_origin)
//
// authorization / date / host 三个查询参数随 URL 下发。
//
// 返回的 URL 含派生凭据(authorization 解码即 api_key 与签名), 不得写日志、不得进错误文本;
// 需要展示时用 RedactURL。endpoint 须为 ws:// 或 wss:// 完整地址(含版本路径)。
func SignURL(endpoint string, cred Credentials, now time.Time) (string, error) {
	if cred.APIKey == "" {
		return "", fmt.Errorf("xunfei credentials incomplete: api_key is empty")
	}
	if cred.APISecret == "" {
		return "", fmt.Errorf("xunfei credentials incomplete: api_secret is empty")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid xunfei spark endpoint: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("invalid xunfei spark endpoint scheme %q: want ws or wss", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid xunfei spark endpoint: host is empty")
	}

	host := u.Host
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	date := signDate(now)

	signatureOrigin := "host: " + host + "\ndate: " + date + "\nGET " + path + " HTTP/1.1"
	mac := hmac.New(sha256.New, []byte(cred.APISecret))
	mac.Write([]byte(signatureOrigin))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	authorizationOrigin := fmt.Sprintf(
		`api_key="%s", algorithm="hmac-sha256", date="%s", headers="host date request-line", signature="%s"`,
		cred.APIKey, date, signature)
	authorization := base64.StdEncoding.EncodeToString([]byte(authorizationOrigin))

	query := u.Query()
	query.Set("host", host)
	query.Set("date", date)
	query.Set("authorization", authorization)
	u.RawQuery = query.Encode()

	return u.String(), nil
}

// RedactURL 去掉 URL 里的派生凭据(authorization 查询参数), 供日志与错误文本展示;
// 解析不出来的输入一律整体隐藏, 宁可少显示也不泄露。
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted-url>"
	}
	query := u.Query()
	query.Del("authorization")
	u.RawQuery = query.Encode()
	return u.String()
}

// EndpointFromBaseURL 把渠道基地址规整成星火 WebSocket 端点:
// http/https 映射为 ws/wss; 基地址没有路径时按讯飞服务 domain 拼出版本路径
// (general→/v1.1/chat, generalv2→/v2.1/chat, generalv3→/v3.1/chat,
//
//	generalv3.5→/v3.5/chat, 4.0Ultra→/v4.0/chat)。
//
// 基地址带路径时按原样使用, 不再拼接。
func EndpointFromBaseURL(baseURL, domain string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid xunfei spark base url: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("invalid xunfei spark base url scheme %q: want http(s) or ws(s)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid xunfei spark base url: host is empty")
	}
	if u.Path != "" && u.Path != "/" {
		return u.String(), nil
	}
	path, ok := sparkPathByDomain(domain)
	if !ok {
		return "", fmt.Errorf("xunfei spark domain %q has no known websocket path: configure the full wss endpoint in the channel base url", domain)
	}
	u.Path = path
	return u.String(), nil
}

// sparkPathByDomain 按讯飞官方文档的 domain→服务版本映射给出 WebSocket 路径。
func sparkPathByDomain(domain string) (string, bool) {
	switch domain {
	case "general":
		return "/v1.1/chat", true
	case "generalv2":
		return "/v2.1/chat", true
	case "generalv3":
		return "/v3.1/chat", true
	case "generalv3.5":
		return "/v3.5/chat", true
	case "4.0Ultra":
		return "/v4.0/chat", true
	default:
		return "", false
	}
}
