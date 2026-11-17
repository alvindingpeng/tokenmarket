package relay

import (
	"net/http"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
)

// geminiQuotaError 造一个 Gemini/Code Assist 额度耗尽的上游错误。
//
// 真实形态: HTTP 429 + message 含 Resource exhausted + type RESOURCE_EXHAUSTED。
func geminiQuotaError() error {
	return &llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail: llm.ErrorDetail{
			Message: "Resource has been exhausted (e.g. check quota).",
			Type:    "RESOURCE_EXHAUSTED",
		},
	}
}

// TestSubscriptionQuotaExhaustedPausesChannel 是 S2 的验收测试。
//
// 缺口: 通用分类器把 429 的 resource_exhausted 判成 rate(限流), 于是订阅账号额度耗尽后
// 系统只会"冷却几秒再试", 反复去打一个已经没额度的账号, 而不会暂停渠道等待换号。
//
// 订阅渠道的额度是账户级月度配额: 用完就是彻底用完, "过一会儿再试"不成立。
func TestSubscriptionQuotaExhaustedPausesChannel(t *testing.T) {
	cases := []struct {
		name    string
		dialect model.Dialect
		want    ErrorClass
	}{
		{"订阅渠道的 429 视为额度耗尽", model.DialectSubscription, ErrorClassQuota},
		// 边界: 普通渠道的 429 必须维持 rate —— 它们的限流窗口过后确实能恢复,
		// 错判成 quota 会让普通渠道因为一次限流就被暂停几分钟, 比限流本身更糟。
		{"普通渠道的 429 维持限流", model.DialectGeneric, ErrorClassRate},
		{"gemini_cli 渠道维持限流", model.DialectGeminiCLI, ErrorClassRate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, rule := classifyUpstreamErrorForChannel(geminiQuotaError(), tc.dialect)
			if class != tc.want {
				t.Fatalf("方言 %q: class=%s want %s (rule=%s)", tc.dialect, class, tc.want, rule)
			}
		})
	}
}
