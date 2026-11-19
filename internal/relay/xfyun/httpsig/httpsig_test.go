package httpsig

import (
	"net/http"
	"testing"
	"time"
)

// 基准向量由 openssl 独立计算: 签名基串
// "host: maas-api.cn-huabei-1.xf-yun.com\ndate: Thu, 01 Jan 2026 00:00:00 GMT\nPOST /v2/chat/completions HTTP/1.1"
// 以 probe-secret 做 HMAC-SHA256 后 base64。
const wantAuthorization = `api_key="probe-key", algorithm="hmac-sha256", headers="host date request-line", signature="IhjVn9KlhFee61WFnpi65MP/NxdzVQecLxwiFeNjpoc="`

var fixedSignTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestV2Signature(t *testing.T) {
	authorization, date := V2Signature("probe-key", "probe-secret",
		"POST", "maas-api.cn-huabei-1.xf-yun.com", "/v2/chat/completions", fixedSignTime)
	if date != "Thu, 01 Jan 2026 00:00:00 GMT" {
		t.Fatalf("date = %q", date)
	}
	if authorization != wantAuthorization {
		t.Fatalf("authorization = %q, want %q", authorization, wantAuthorization)
	}
}

func TestSignHTTPV2(t *testing.T) {
	req, err := http.NewRequest("POST", "https://maas-api.cn-huabei-1.xf-yun.com/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignHTTPV2(req, "probe-key", "probe-secret", fixedSignTime); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != wantAuthorization {
		t.Fatalf("authorization = %q, want %q", got, wantAuthorization)
	}
	if got := req.Header.Get("Date"); got != "Thu, 01 Jan 2026 00:00:00 GMT" {
		t.Fatalf("date header = %q", got)
	}
	if err := SignHTTPV2(req, "probe-key", "", fixedSignTime); err == nil {
		t.Fatal("missing api secret must be rejected")
	}
}

func TestRequired(t *testing.T) {
	cases := []struct {
		base, secret string
		want         bool
	}{
		{"https://maas-api.cn-huabei-1.xf-yun.com", "secret", true},
		{"https://maas-api.cn-huabei-2.xf-yun.com", "secret", true},
		{"https://maas-api.cn-huabei-1.xf-yun.com", " ", false},
		{"https://spark-api-open.xf-yun.com", "secret", false},
		{"", "secret", false},
		{"://bad url", "secret", false},
	}
	for _, c := range cases {
		if got := Required(c.base, c.secret); got != c.want {
			t.Fatalf("Required(%q, %q) = %v, want %v", c.base, c.secret, got, c.want)
		}
	}
}
