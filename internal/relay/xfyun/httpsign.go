package xfyun

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"

	"github.com/alvindingpeng/switchyard/internal/relay/xfyun/httpsig"
)

// HTTPSigningRequired 判断该渠道的 HTTP 请求是否要走讯飞 V2 签名鉴权,
// 规则与平台差异见 httpsig 包注释。
func HTTPSigningRequired(baseURL, apiSecret string) bool {
	return httpsig.Required(baseURL, apiSecret)
}

// SignHTTPV2Request 为出站 httpclient.Request 添加 V2 签名并摘除 Auth,
// 否则传输层 applyAuth 会把签名头覆写成 Bearer。
func SignHTTPV2Request(req *httpclient.Request, creds Credentials, now time.Time) error {
	if req == nil {
		return fmt.Errorf("xunfei http signing: request is nil")
	}
	if creds.APIKey == "" || creds.APISecret == "" {
		return fmt.Errorf("xunfei http signing requires api key and api secret")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("xunfei http signing: request url is not absolute")
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	authorization, date := httpsig.V2Signature(creds.APIKey, creds.APISecret, method, u.Host, u.RequestURI(), now)
	if req.Headers == nil {
		req.Headers = make(http.Header)
	}
	req.Headers.Set("Authorization", authorization)
	req.Headers.Set("Date", date)
	req.Auth = nil
	return nil
}
