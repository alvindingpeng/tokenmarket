package relay

import (
	"net/http"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
)

// subscriptionRateLimitError 造一个 Gemini/Code Assist 的**速率限制** 429。
//
// 真实形态(实测 2026-11): HTTP 429 + message "Resource has been exhausted (e.g. check quota)."
// + type RESOURCE_EXHAUSTED。注意它虽然提到 quota, 实际是每分钟限流:
// 同一账号同一时刻 retrieveUserQuota 显示 17 个模型 remainingFraction 全为 1。
// 这类 429 会自行恢复, 不该暂停渠道。
func subscriptionRateLimitError() error {
	return &llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail: llm.ErrorDetail{
			Message: "Resource has been exhausted (e.g. check quota).",
			Type:    "RESOURCE_EXHAUSTED",
		},
	}
}

// subscriptionQuotaExhaustedError 造一个明确表示额度耗尽的 429。
func subscriptionQuotaExhaustedError() string {
	return "You exceeded your current quota, please check your plan and billing details."
}

// TestSubscriptionRateLimitDoesNotPauseChannel 钉住"瞬时限流不得暂停渠道"。
//
// 这是对上一版行为的更正。上一版把订阅渠道的 429 一律判成 quota, 理由是订阅额度是
// 账户级配额, 用完只能等下个周期。实测推翻了这个前提: 同一个演示里的 429 只是每分钟限流,
// 额度实际是满的。一律判 quota 的后果是一秒级的限流被放大成整渠道暂停(默认 30 分钟),
// 而订阅渠道通常只有一个账号 —— 渠道一停, 分组就没有成员,
// 对外报 "group has no upstream members configured"。
func TestSubscriptionRateLimitDoesNotPauseChannel(t *testing.T) {
	cases := []struct {
		name    string
		dialect model.Dialect
		want    ErrorClass
	}{
		// resource_exhausted 对限流与配额是同一句措辞, 不构成额度耗尽证据。
		{"订阅渠道的 resource_exhausted 维持限流", model.DialectSubscription, ErrorClassRate},
		{"普通渠道的 429 维持限流", model.DialectGeneric, ErrorClassRate},
		{"gemini_cli 渠道维持限流", model.DialectGeminiCLI, ErrorClassRate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, rule := classifyUpstreamErrorForChannel(subscriptionRateLimitError(), tc.dialect)
			if class != tc.want {
				t.Fatalf("方言 %q: class=%s want %s (rule=%s)", tc.dialect, class, tc.want, rule)
			}
		})
	}
}

// TestSubscriptionQuotaExhaustedStillPausesChannel 保证真正额度耗尽仍能暂停渠道。
//
// 放宽不能把该保护的场景一起放掉: 账户级额度真用完时, 冷却重试永远好不了,
// 只有暂停渠道换号才能解决。
func TestSubscriptionQuotaExhaustedStillPausesChannel(t *testing.T) {
	err := &llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail: llm.ErrorDetail{
			Message: subscriptionQuotaExhaustedError(),
			Type:    "insufficient_quota",
		},
	}
	class, rule := classifyUpstreamErrorForChannel(err, model.DialectSubscription)
	if class != ErrorClassQuota {
		t.Fatalf("明确的额度耗尽应判 quota: got %s (rule=%s)", class, rule)
	}
	// 明确的额度耗尽文本对任何方言都是 quota(通用分类器本来就认它),
	// 订阅分支不改变这一点, 只是让"模糊的 429"不再被当成额度耗尽。
	class, _ = classifyUpstreamErrorForChannel(err, model.DialectGeneric)
	if class != ErrorClassQuota {
		t.Fatalf("明确额度耗尽对普通渠道也应判 quota: got %s", class)
	}
}
