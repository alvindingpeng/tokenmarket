package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// 上游错误分类: 同一个 HTTP 状态码在不同厂商语义可能完全不同(429 既可能是限流也可能是额度耗尽),
// 故按"语义(code/type/文本)优先、状态码兜底"分出九类, 路由按类别决定重试、换渠道、冷却与熔断:
//
//   - client/policy: 请求本身有问题或内容被拦, 换渠道必然同样失败 -> 立即透传, 不重试不换渠道(policy 不换渠道是为了不跨供应商绕过内容审查)。
//   - auth/quota:    凭据失效/额度耗尽, 该 Key 已不可用 -> 立即熔断 Key 并换渠道, 不在原地重试。
//   - overload:      上游暂时过载 -> 立即换渠道, 当前成员短冷却。
//   - model:         该渠道不提供目标模型 -> 立即换渠道, 不计渠道失败率。
//   - rate:          限流 -> 标记窗口触顶供选路跳过(维持既有行为)。
//   - transient/unknown: 临时故障 -> 先按间隔重试当前成员, 达上限或有其他成员再换(维持既有行为)。
type ErrorClass string

const (
	ErrorClassClient    ErrorClass = "client"    // 请求错误: 参数不合法/上下文超长等, 任何渠道都会失败。
	ErrorClassPolicy    ErrorClass = "policy"    // 内容被安全策略拦截: 不换渠道绕过审查, 直接透传。
	ErrorClassAuth      ErrorClass = "auth"      // 凭据失效: 401/403 与各家 invalid_api_key 语义。
	ErrorClassQuota     ErrorClass = "quota"     // 额度/余额耗尽: 402 与 insufficient_quota 语义。
	ErrorClassRate      ErrorClass = "rate"      // 限流: 429 与各家 rate limit 语义。
	ErrorClassOverload  ErrorClass = "overload"  // 上游过载: 529(Anthropic)与 overloaded 语义。
	ErrorClassModel     ErrorClass = "model"     // 目标模型缺失: 404 与 model_not_found 语义。
	ErrorClassTransient ErrorClass = "transient" // 临时故障: 5xx/超时/网络错误。
	ErrorClassUnknown   ErrorClass = "unknown"   // 未能识别: 按 transient 处理, 便于事后按日志补规则。
)

// errorRule 一条分类规则: 命中任一状态码或任一文本特征即归入 class。
// tokens 对错误全文做小写包含匹配, statuses 只看 HTTP 状态码。
type errorRule struct {
	name     string
	class    ErrorClass
	statuses []int
	tokens   []string
}

// errorRules 按序匹配, 顺序即优先级: 语义特征先于状态码; 同为语义时额度先于限流、
// 模型与安全先于通用请求错误(400 + model_not_found 归模型缺失而不是请求错误)。
// 匹配表是内置常量, 后续如需按部署环境调整再提为可配置。
var errorRules = []errorRule{
	{name: "policy_blocked", class: ErrorClassPolicy, tokens: []string{
		"content_policy_violation", "content_filter", "responsibleaipolicyviolation",
		"content management policy", "prohibited_content", "blocked due to safety", "recitation", "blocklist",
	}},
	{name: "model_missing", class: ErrorClassModel, tokens: []string{
		"model_not_found", "model_not_exists", "modelnotexists", "invalid_model",
		"model does not exist", "not found: model",
	}},
	{name: "quota_exhausted", class: ErrorClassQuota, tokens: []string{
		"insufficient_quota", "insufficient_balance", "insufficient balance",
		"billing_hard_limit_reached", "billing hard limit", "credit balance too low", "insufficient credits",
	}},
	{name: "auth_invalid", class: ErrorClassAuth, tokens: []string{
		"invalid_api_key", "invalid_x_api_key", "invalid_authentication_error",
		"authentication_error", "permission_error", "permission_denied_error",
		"account_deactivated", "api key not valid", "incorrect api key", "invalid api key",
		"unauthenticated", "permission_denied",
	}},
	{name: "rate_limited", class: ErrorClassRate, tokens: []string{
		"rate_limit", "rate limit", "too many requests", "resource_exhausted", "resource exhausted",
		"tokens per min", "requests per min", "requests per second", "concurrent connections", "quota exceeded",
	}},
	{name: "overloaded", class: ErrorClassOverload, tokens: []string{
		"overloaded", "at capacity", "server is busy",
	}},
	{name: "client_invalid", class: ErrorClassClient, tokens: []string{
		"invalid_request_error", "invalid_prompt", "context_length_exceeded",
		"prompt is too long", "request_too_large", "invalid_argument", "reduce the length",
	}},
	// 语义没给出答案时按状态码归类: 主流厂商的状态码语义基本一致。
	{name: "status_rate", class: ErrorClassRate, statuses: []int{http.StatusTooManyRequests}},
	{name: "status_auth", class: ErrorClassAuth, statuses: []int{http.StatusUnauthorized, http.StatusForbidden}},
	{name: "status_quota", class: ErrorClassQuota, statuses: []int{http.StatusPaymentRequired}},
	{name: "status_model", class: ErrorClassModel, statuses: []int{http.StatusNotFound}},
	{name: "status_client", class: ErrorClassClient, statuses: []int{
		http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity,
	}},
	{name: "status_overload", class: ErrorClassOverload, statuses: []int{529}},
	{name: "status_transient", class: ErrorClassTransient, statuses: []int{
		http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	}},
	{name: "network_transient", class: ErrorClassTransient, tokens: []string{
		"timeout", "deadline exceeded", "connection reset", "connection refused",
		"broken pipe", "empty response", "stream disconnected", "eof", "no such host", "tls handshake",
	}},
}

func (r errorRule) match(status int, lowerText string) bool {
	for _, s := range r.statuses {
		if status == s {
			return true
		}
	}
	for _, token := range r.tokens {
		if strings.Contains(lowerText, token) {
			return true
		}
	}
	return false
}

// classifyUpstreamError 把上游错误归入九类, 并返回命中的规则名(进路由事件便于事后补规则)。
func classifyUpstreamError(err error) (ErrorClass, string) {
	if err == nil {
		return ErrorClassUnknown, "no_error"
	}
	status, text := upstreamErrorInfo(err)
	lower := strings.ToLower(text)
	for _, rule := range errorRules {
		if rule.match(status, lower) {
			return rule.class, rule.name
		}
	}
	return ErrorClassUnknown, "unmatched"
}

// upstreamErrorInfo 取出上游错误的 HTTP 状态码与用于分类的全文(message/code/type/错误体)。
func upstreamErrorInfo(err error) (int, string) {
	var responseErr *llm.ResponseError
	if errors.As(err, &responseErr) {
		text := responseErr.Detail.Message + " " + responseErr.Detail.Code + " " + responseErr.Detail.Type
		return responseErr.StatusCode, text
	}
	var httpErr *httpclient.Error
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode, string(httpErr.Body) + " " + httpErr.Status
	}
	return 0, err.Error()
}

// upstreamStatus 给客户端透传上游错误时使用的状态码; 拿不到时按网关错误 502。
func upstreamStatus(err error) int {
	if status, _ := upstreamErrorInfo(err); status > 0 {
		return status
	}
	return http.StatusBadGateway
}

// relayResponseError 把最终失败原因转换成给客户端的错误响应:
// 上游错误保留原始状态码与错误结构原样透传, 其余包装成网关自己的 relay_unavailable。
func relayResponseError(cause error, status int) *llm.ResponseError {
	var responseErr *llm.ResponseError
	if errors.As(cause, &responseErr) {
		return responseErr
	}
	var httpErr *httpclient.Error
	if errors.As(cause, &httpErr) {
		// 同协议透传拿到的上游错误体多为 {"error":{...}} 形状, 解析得出就保留原始结构。
		var parsed llm.ResponseError
		if json.Unmarshal(httpErr.Body, &parsed) == nil && parsed.Detail.Message != "" {
			parsed.StatusCode = httpErr.StatusCode
			return &parsed
		}
		return &llm.ResponseError{
			StatusCode: upstreamStatus(cause),
			Detail:     llm.ErrorDetail{Message: string(httpErr.Body), Type: "upstream_error"},
		}
	}
	return &llm.ResponseError{StatusCode: status, Detail: llm.ErrorDetail{Message: cause.Error(), Type: "relay_unavailable"}}
}

// modelUnavailableError 全渠道都没有目标模型时的友好提示: 各家上游的原始报文既不一致也难读,
// 统一告诉调用方问题出在模型名或上架配置。
func modelUnavailableError(modelName string) error {
	return &llm.ResponseError{
		StatusCode: http.StatusNotFound,
		Detail: llm.ErrorDetail{
			Message: "model " + strconv.Quote(modelName) + " is not available on any upstream channel; check the model name, or ask the administrator to publish it on a channel",
			Type:    "model_unavailable",
		},
	}
}

// classCooldownSeconds 换路类错误的成员冷却时长(秒): 只为防止本轮立刻又选中同一成员, 不做长惩罚——
// 凭据/额度问题的长惩罚由 Key 熔断承担, 不拖累该渠道下其他 Key 与其他模型。
// 两档时长都是运维参数(熔断与冷却设置), 缺键或非法值回退内置默认。
func classCooldownSeconds(class ErrorClass) int {
	switch class {
	case ErrorClassAuth, ErrorClassQuota:
		if v := op.BreakerCoolAuthSeconds(); v > 0 {
			return v
		}
	case ErrorClassOverload, ErrorClassModel:
		if v := op.BreakerCoolSoftSeconds(); v > 0 {
			return v
		}
	}
	return 1
}

// attemptStatusOfClass 尝试终态按错误分类映射: 与历史取值兼容的保留原值, 其余新增为分类名。
func attemptStatusOfClass(class ErrorClass) string {
	switch class {
	case ErrorClassRate:
		return "limited"
	case ErrorClassClient:
		return "client_error"
	case ErrorClassPolicy:
		return "policy_blocked"
	case ErrorClassAuth:
		return "auth_error"
	case ErrorClassQuota:
		return "quota"
	case ErrorClassOverload:
		return "overloaded"
	case ErrorClassModel:
		return "model_missing"
	}
	return "failed"
}

// isTimeoutError 人工定时之外的超时判定: 等待首帧/完整响应的包装错误带 timeout 字样,
// 也可能是 context.DeadlineExceeded 本身。
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline")
}
