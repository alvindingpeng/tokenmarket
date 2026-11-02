package op

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestClassifyHealthStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "authentication_failed",
		http.StatusForbidden:           "authorization_failed",
		http.StatusNotFound:            "models_endpoint_not_found",
		http.StatusMethodNotAllowed:    "models_endpoint_method_not_allowed",
		http.StatusInternalServerError: "upstream_http_error",
	}
	for status, want := range cases {
		if got := classifyHealthStatus(status); !strings.HasPrefix(got, want) {
			t.Errorf("status %d: got %q, want prefix %q", status, got, want)
		}
	}
}

func TestHealthProbeRequestUsesCachedKeyAuthWithoutLeakingKey(t *testing.T) {
	const secret = "sk-health-probe-secret"
	target := model.ChannelConfig{BaseURL: "https://api.example.test", OpenAIResponsePath: "/v1/responses"}
	req, err := newOpenAIModelsRequest(context.Background(), target, secret, "", "https://api.example.test/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+secret {
		t.Fatalf("authorization header = %q", got)
	}
	if strings.Contains(req.URL.String(), secret) || strings.Contains(classifyHealthStatus(http.StatusUnauthorized), secret) {
		t.Fatal("probe URL or status error leaked channel key")
	}
}

// TestProbeUnknown 探测「未知」口径: 4xx 与缺凭据不算故障, 5xx/传输层/健康必须参与判定。
func TestProbeUnknown(t *testing.T) {
	cases := []struct {
		name   string
		record model.ChannelHealthRecord
		want   bool
	}{
		{"健康", model.ChannelHealthRecord{StatusCode: 200, Healthy: true}, false},
		{"401 凭据被拒但非故障证据", model.ChannelHealthRecord{StatusCode: 401, Error: "authentication_failed (HTTP 401)"}, true},
		{"403", model.ChannelHealthRecord{StatusCode: 403, Error: "authorization_failed (HTTP 403)"}, true},
		{"404 未实现 models", model.ChannelHealthRecord{StatusCode: 404, Error: "models_endpoint_not_found (HTTP 404)"}, true},
		{"405", model.ChannelHealthRecord{StatusCode: 405, Error: "models_endpoint_method_not_allowed (HTTP 405)"}, true},
		{"无可用凭据", model.ChannelHealthRecord{StatusCode: 0, Error: "no enabled channel key available"}, true},
		{"5xx 真故障", model.ChannelHealthRecord{StatusCode: 502, Error: "upstream_http_error (HTTP 502)"}, false},
		{"传输层失败", model.ChannelHealthRecord{StatusCode: 0, Error: "dial tcp: connection refused"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeUnknown(tc.record); got != tc.want {
				t.Fatalf("probeUnknown = %v, want %v (record=%+v)", got, tc.want, tc.record)
			}
		})
	}
}
