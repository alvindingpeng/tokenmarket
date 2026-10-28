package xfyun

import (
	"net/http"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
)

// 与 httpsig 包测试同一基准向量(openssl 独立计算)。
const wantAuthorization = `api_key="probe-key", algorithm="hmac-sha256", headers="host date request-line", signature="IhjVn9KlhFee61WFnpi65MP/NxdzVQecLxwiFeNjpoc="`

var fixedSignTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestSignHTTPV2RequestClearsAuth(t *testing.T) {
	req := &httpclient.Request{
		Method:  "POST",
		URL:     "https://maas-api.cn-huabei-1.xf-yun.com/v2/chat/completions",
		Headers: make(http.Header),
		Auth:    &httpclient.AuthConfig{},
	}
	if err := SignHTTPV2Request(req, Credentials{APIKey: "probe-key", APISecret: "probe-secret"}, fixedSignTime); err != nil {
		t.Fatal(err)
	}
	if req.Auth != nil {
		t.Fatal("auth must be cleared so the transport cannot overwrite the signature with bearer")
	}
	if got := req.Headers.Get("Authorization"); got != wantAuthorization {
		t.Fatalf("authorization = %q, want %q", got, wantAuthorization)
	}
	if got := req.Headers.Get("Date"); got != "Thu, 01 Jan 2026 00:00:00 GMT" {
		t.Fatalf("date header = %q", got)
	}
	if err := SignHTTPV2Request(req, Credentials{APIKey: "probe-key"}, fixedSignTime); err == nil {
		t.Fatal("missing api secret must be rejected")
	}
}
