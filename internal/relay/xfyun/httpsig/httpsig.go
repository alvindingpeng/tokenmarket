// Package httpsig 实现讯飞 V2 通用 HTTP 签名鉴权。
//
// 讯飞 MaaS 平台(maas-api.*)以 HTTP 密码鉴权为主(Bearer ak- 开头的 API 密码, 2026-10 线上实测
// 三协议接口均可用); 网关对非 ak- 前缀的 Authorization 会按 V2 签名解析, 解析失败时回
// "HMAC signature cannot be verified" —— 故仍保留 APIKey/APISecret 签名路径供持有签名密钥对的账号使用,
// 渠道填了 API Secret 且目标是 maas-api.* 主机时启用, 否则保持 Bearer。
//
// 独立成小包供模型探测(internal/op)与出站(internal/relay/xfyun)共用, 避免 op→xfyun→rhttp→op 的包环。
package httpsig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Required 判断目标地址是否要走 V2 签名鉴权。
func Required(baseURL, apiSecret string) bool {
	if strings.TrimSpace(apiSecret) == "" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	return strings.HasPrefix(u.Hostname(), "maas-api.")
}

// V2Signature 计算讯飞 V2 通用签名的 Authorization 与 Date 头值。
// 签名基串 = "host: {host}\ndate: {date}\n{method} {request-target} HTTP/1.1",
// signature = base64(HMAC-SHA256(APISecret, 基串)); Authorization 取明文 authorization_origin
// (实测网关不接受 base64 包装形式); Date 为 GMT 的 RFC1123 字面格式(带时区偏移会被拒)。
func V2Signature(apiKey, apiSecret, method, host, requestURI string, now time.Time) (authorization, date string) {
	date = now.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	signingBase := "host: " + host + "\n" + "date: " + date + "\n" + method + " " + requestURI + " HTTP/1.1"
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingBase))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	authorization = `api_key="` + apiKey + `", algorithm="hmac-sha256", headers="host date request-line", signature="` + signature + `"`
	return authorization, date
}

// SignHTTPV2 为 HTTP 请求(net/http 形状, 模型探测用)添加 V2 签名鉴权头。
// 错误只说明缺哪个字段, 不回显任何凭据值。
func SignHTTPV2(req *http.Request, apiKey, apiSecret string, now time.Time) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("xunfei http signing: request is nil")
	}
	if apiKey == "" || apiSecret == "" {
		return fmt.Errorf("xunfei http signing requires api key and api secret")
	}
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	authorization, date := V2Signature(apiKey, apiSecret, req.Method, host, req.URL.RequestURI(), now)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Date", date)
	return nil
}
