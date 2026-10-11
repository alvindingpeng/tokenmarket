package relay

import (
	"context"
	"errors"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// quote 拼 JSON 片段用的双引号, 避免源码里出现转义序列。
func quote() string {
	return string('"')
}

// TestClassifyUpstreamError 覆盖各家上游的典型错误样本: 同一状态码在不同厂商语义不同,
// 分类必须落在语义(code/type/文本)而不是只看状态码。
func TestClassifyUpstreamError(t *testing.T) {
	dq := quote()
	cases := []struct {
		name  string
		err   error
		class ErrorClass
	}{
		{"openai 参数错误", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Type: "invalid_request_error", Message: "Invalid value for 'temperature'"}}, ErrorClassClient},
		{"openai 上下文超长", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "context_length_exceeded", Type: "invalid_request_error", Message: "This model maximum context length is 128000 tokens"}}, ErrorClassClient},
		{"openai 凭据无效", &llm.ResponseError{StatusCode: 401, Detail: llm.ErrorDetail{Code: "invalid_api_key", Type: "authentication_error", Message: "Incorrect API key provided"}}, ErrorClassAuth},
		{"openai 无权限", &llm.ResponseError{StatusCode: 403, Detail: llm.ErrorDetail{Type: "permission_error", Message: "You do not have access to this model"}}, ErrorClassAuth},
		{"openai 额度耗尽", &llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Code: "insufficient_quota", Type: "rate_limit_error", Message: "You exceeded your current quota"}}, ErrorClassQuota},
		{"openai 限流", &llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Code: "tokens", Type: "rate_limit_error", Message: "Rate limit reached for tokens per min (TPM)"}}, ErrorClassRate},
		{"openai 模型缺失", &llm.ResponseError{StatusCode: 404, Detail: llm.ErrorDetail{Code: "model_not_found", Type: "invalid_request_error", Message: "The model does not exist"}}, ErrorClassModel},
		{"openai 内容拦截", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "content_policy_violation", Type: "invalid_request_error", Message: "Your request was rejected"}}, ErrorClassPolicy},
		{"openai 服务端错误", &llm.ResponseError{StatusCode: 500, Detail: llm.ErrorDetail{Type: "server_error", Message: "The server had an error"}}, ErrorClassTransient},
		{"anthropic 过载", &llm.ResponseError{StatusCode: 529, Detail: llm.ErrorDetail{Type: "overloaded_error", Message: "Overloaded"}}, ErrorClassOverload},
		{"anthropic 限流", &llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Type: "rate_limit_error", Message: "Number of request tokens has exceeded your per-minute rate limit"}}, ErrorClassRate},
		{"anthropic 提示词超长", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Type: "invalid_request_error", Message: "prompt is too long: 250000 tokens > 200000 maximum"}}, ErrorClassClient},
		{"anthropic 额度耗尽", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "billing_hard_limit_reached", Type: "invalid_request_error", Message: "Credit balance is too low"}}, ErrorClassQuota},
		{"gemini 配额限流", &llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Code: "RESOURCE_EXHAUSTED", Message: "Quota exceeded for quota metric Generate Content API requests per minute"}}, ErrorClassRate},
		{"gemini 过载", &llm.ResponseError{StatusCode: 503, Detail: llm.ErrorDetail{Code: "UNAVAILABLE", Message: "The model is overloaded. Please try again later."}}, ErrorClassOverload},
		{"gemini 安全拦截", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "SAFETY", Message: "Response was blocked due to SAFETY"}}, ErrorClassPolicy},
		{"deepseek 余额不足", &llm.ResponseError{StatusCode: 402, Detail: llm.ErrorDetail{Message: "Insufficient Balance"}}, ErrorClassQuota},
		{"deepseek 模型缺失", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "ModelNotExists", Message: "Model Not Exists"}}, ErrorClassModel},
		{"moonshot 限流", &llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Code: "rate_limit_reached", Message: "Your account reached max rpm"}}, ErrorClassRate},
		{"azure 内容过滤", &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Code: "content_filter", Message: "The response was filtered due to the prompt triggering Azure OpenAI content management policy"}}, ErrorClassPolicy},
		{"透传错误体额度不足", &httpclient.Error{StatusCode: 402, Body: []byte("{" + dq + "error" + dq + ":{" + dq + "message" + dq + ":" + dq + "Insufficient Balance" + dq + "}}")}, ErrorClassQuota},
		{"透传错误体限流", &httpclient.Error{StatusCode: 429, Body: []byte("{" + dq + "error" + dq + ":{" + dq + "type" + dq + ":" + dq + "rate_limit_error" + dq + "}}")}, ErrorClassRate},
		{"首帧超时", errors.New("upstream stream first event timeout"), ErrorClassTransient},
		{"完整响应超时", errors.New("upstream non-stream response timeout"), ErrorClassTransient},
		{"上下文超时", context.DeadlineExceeded, ErrorClassTransient},
		{"网络连接失败", errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), ErrorClassTransient},
		{"状态码 502", &llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{Message: "Bad Gateway"}}, ErrorClassTransient},
		{"状态码 404 无语义", &llm.ResponseError{StatusCode: 404, Detail: llm.ErrorDetail{Message: "Not Found"}}, ErrorClassModel},
		{"未识别错误", errors.New("something we have never seen"), ErrorClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, _ := classifyUpstreamError(tc.err)
			if class != tc.class {
				t.Fatalf("classify(%v) = %s, want %s", tc.err, class, tc.class)
			}
		})
	}
}

// TestAttemptStatusOfClass 尝试终态与历史取值兼容: 限流/超时/取消沿用旧值, 其余为分类名。
func TestAttemptStatusOfClass(t *testing.T) {
	cases := []struct {
		err    error
		status string
	}{
		{nil, "success"},
		{context.Canceled, "canceled"},
		{errors.New("upstream stream first event timeout"), "timeout"},
		{&llm.ResponseError{StatusCode: 429, Detail: llm.ErrorDetail{Type: "rate_limit_error"}}, "limited"},
		{&llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Type: "invalid_request_error"}}, "client_error"},
		{&llm.ResponseError{StatusCode: 401, Detail: llm.ErrorDetail{Type: "authentication_error"}}, "auth_error"},
		{&llm.ResponseError{StatusCode: 402, Detail: llm.ErrorDetail{Message: "Insufficient Balance"}}, "quota"},
		{&llm.ResponseError{StatusCode: 529, Detail: llm.ErrorDetail{Type: "overloaded_error"}}, "overloaded"},
		{&llm.ResponseError{StatusCode: 404, Detail: llm.ErrorDetail{Code: "model_not_found"}}, "model_missing"},
		{errors.New("something we have never seen"), "failed"},
	}
	for _, tc := range cases {
		got := attemptStatusOf(tc.err)
		if got != tc.status {
			t.Fatalf("attemptStatusOf(%v) = %s, want %s", tc.err, got, tc.status)
		}
	}
}

// TestRelayResponseErrorPassthrough 上游错误原样透传, 网关自身错误才包装 relay_unavailable。
func TestRelayResponseErrorPassthrough(t *testing.T) {
	dq := quote()
	upstream := &llm.ResponseError{StatusCode: 400, Detail: llm.ErrorDetail{Type: "invalid_request_error", Message: "bad param"}}
	if got := relayResponseError(upstream, 502); got != upstream {
		t.Fatalf("上游错误应原样透传, got %#v", got)
	}
	wrapped := relayResponseError(errors.New("no available upstream"), 503)
	if wrapped.Detail.Type != "relay_unavailable" || wrapped.StatusCode != 503 {
		t.Fatalf("网关错误应包装 relay_unavailable, got %#v", wrapped)
	}
	body := []byte("{" + dq + "error" + dq + ":{" + dq + "type" + dq + ":" + dq + "rate_limit_error" + dq + "," + dq + "message" + dq + ":" + dq + "slow down" + dq + "}}")
	parsed := relayResponseError(&httpclient.Error{StatusCode: 429, Body: body}, 502)
	if parsed.StatusCode != 429 || parsed.Detail.Type != "rate_limit_error" {
		t.Fatalf("透传错误体应解析保留原始结构, got %#v", parsed)
	}
}
