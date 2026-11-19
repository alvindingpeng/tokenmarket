package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/gemini"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// Antigravity 的客户端身份常量。与 model/account_oauth.go 里那对保持一致 ——
// 凭据必须由同一个 client 授权, 否则数据面会 403(缺 cclog/experimentsandconfigs)。
const (
	// antigravityAPIClientHeader 是 Antigravity 客户端标识头。实测缺少它会被上游拒绝。
	antigravityAPIClientHeader = "google-cloud-sdk vscode_cloudshelleditor/0.1"
	// antigravityClientMetadata 是 Client-Metadata 头, 声明我们是 Antigravity IDE。
	// 这个值决定 loadCodeAssist/onboardUser 走哪条 tier 分支: 声明 ANTIGRAVITY 才能拿到
	// 自动分配的 project(aicode-consumers), 而 pluginType=GEMINI 的个人免费层已被 Google 停用
	// (onboardUser 返回 403 UNSUPPORTED_CLIENT, error_number 1013)。
	antigravityClientMetadata = `{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`
	// antigravityUserAgentVersion 是内置默认的 Antigravity 客户端版本号。
	//
	// 为什么从 1.1.0 提到 1.20x: 实测上游对流式请求(streamGenerateContent)做版本闸门,
	// 1.1.0 返回 200 但正文被替换成 "This version of Antigravity is no longer supported",
	// 于是客户端拿到一段提示文案而不是模型输出 —— 状态码成功, 根因只能靠读正文发现。
	//
	// 默认值只作兜底: 真正的版本号走设置项 SettingKeyAntigravityClientVersion,
	// 因为该闸门是分批灰度且会随上游发版变化(实测 1.100.0/1.200.0/2.0.0 通过,
	// 1.99.9/2.5.0 被拒), 写死必然在将来某天再次被拦。
	antigravityUserAgentVersion = "1.200.0"
	antigravityUserAgentOS      = "windows/amd64"
)

// antigravityUserAgent 取本轮使用的 Antigravity User-Agent。
//
// 版本号优先读设置项, 留空/读取失败时回落到内置默认值 —— 构造请求不能因为设置库异常而失败:
// 缺这个头本身就会让上游拒掉请求, 为了一个可选覆盖给自己制造失败没有道理。
func antigravityUserAgent() string {
	version := antigravityUserAgentVersion
	if v, err := op.SettingGetString(model.SettingKeyAntigravityClientVersion); err == nil && strings.TrimSpace(v) != "" {
		version = strings.TrimSpace(v)
	}
	return "antigravity/" + version + " " + antigravityUserAgentOS
}

// geminiCLIOutbound 把统一请求转成 Antigravity 的 v1internal:generateContent 信封。
//
// 为什么自己写而不直接用 axonhub 的 antigravity.Transformer:
// 库里的 Transformer 在构造时就会创建自己的 OAuth token provider, 用 refresh_token 换令牌,
// 并且请求由它自己的 httpClient 发出。而本项目的架构是"定时任务提前刷好令牌, 转发路径只用
// access_token"(见 token_provider.go 的注释), 同一个 refresh_token 若被两条路径同时使用,
// 会因轮换互相作废 —— 表现为账号隔一段时间就静默失效, 极难排查。
//
// 因此这里只取它的**转换语义**(信封形状、header、双层响应解包), 令牌与出口线路仍由本项目的
// 单一路径掌管: 请求由 sendConverted 的 executor 用 resolveUpstreamClient 取得的 client 发出,
// 渠道代理与出口池因此自然生效, 不需要在这里再处理代理。
type geminiCLIOutbound struct {
	// baseURL 是 Code Assist 的基址(渠道配置里填的值)。
	baseURL string
	// project 是 GCP project id; 留空时信封里不写, 由上游按当前 tier 解析。
	project string
	// tokenProvider 提供本轮使用的 access_token。
	tokenProvider auth.APIKeyProvider
	// inner 是库的 Gemini 转换器, 负责把统一请求转成标准 Gemini generateContent 请求体。
	inner transformer.Outbound
}

// newGeminiCLIOutbound 构造 Gemini CLI 订阅渠道的出站转换器。
// baseURL 只用于让内层 gemini 转换器确定版本前缀, 实际地址由调用方拼装。
func newGeminiCLIOutbound(baseURL string, tokenProvider auth.APIKeyProvider, project string) (transformer.Outbound, error) {
	inner, err := gemini.NewOutboundTransformer(baseURL, "placeholder")
	if err != nil {
		return nil, fmt.Errorf("构造内层 gemini 转换器: %w", err)
	}
	return &geminiCLIOutbound{
		baseURL:       baseURL,
		project:       project,
		tokenProvider: tokenProvider,
		inner:         inner,
	}, nil
}

// APIFormat 声明出站协议。Gemini Contents 是内层请求体的格式, 与统一响应无关。
func (t *geminiCLIOutbound) APIFormat() llm.APIFormat {
	return llm.APIFormatGeminiContents
}

// TransformRequest 把统一请求转成 Antigravity 信封请求。
func (t *geminiCLIOutbound) TransformRequest(ctx context.Context, llmReq *llm.Request) (*httpclient.Request, error) {
	if llmReq == nil {
		return nil, fmt.Errorf("请求为空")
	}
	// 内层转换: 得到标准 Gemini generateContent 请求体。
	innerReq, err := t.inner.TransformRequest(ctx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("内层 gemini 转换失败: %w", err)
	}

	var geminiBody any
	if err := json.Unmarshal(innerReq.Body, &geminiBody); err != nil {
		return nil, fmt.Errorf("解析内层 gemini 请求体: %w", err)
	}

	// 信封: {"project","model","request",...}。request 是内层请求体原样。
	envelope := map[string]any{
		"project":     t.project,
		"model":       llmReq.Model,
		"request":     geminiBody,
		"requestType": "agent",
		"userAgent":   "antigravity",
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("序列化信封: %w", err)
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", antigravityUserAgent())
	headers.Set("X-Goog-Api-Client", antigravityAPIClientHeader)
	headers.Set("Client-Metadata", antigravityClientMetadata)
	if llmReq.Stream != nil && *llmReq.Stream {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}

	token := ""
	if t.tokenProvider != nil {
		token = t.tokenProvider.Get(ctx)
	}
	if token == "" {
		return nil, fmt.Errorf("gemini cli: 凭据里没有 access_token")
	}

	streaming := llmReq.Stream != nil && *llmReq.Stream
	action := "generateContent"
	if streaming {
		action = "streamGenerateContent"
	}
	// 方法地址是冒号形式(v1internal:generateContent), 不是斜杠 —— 这是 Code Assist 的 RPC 约定。
	//
	// 版本段**固定为 v1internal**, 不能沿用内层 gemini 转换器给出的版本: 后者面向的是
	// generativelanguage 的 REST 接口(v1beta/v1), 而 Code Assist 是另一套 RPC。
	// 实测把 v1beta 拼进来会得到 404, 且响应正文不说明版本错了。
	const codeAssistPath = "v1internal"
	url := fmt.Sprintf("%s/%s:%s", strings.TrimSuffix(t.baseURL, "/"), codeAssistPath, action)
	if streaming {
		url += "?alt=sse"
	}

	return &httpclient.Request{
		Method:  http.MethodPost,
		URL:     url,
		Headers: headers,
		Body:    body,
		Auth:    &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: token},
	}, nil
}

// TransformResponse 解包 Antigravity 的双层响应再交给内层 gemini 转换器。
//
// 关键: 真实响应形如 {"traceId":...,"response":{"candidates":[...],"usageMetadata":...}}。
// 直接读顶层 candidates 会永远是空且不报错 —— 这是本协议最容易踩的坑。
func (t *geminiCLIOutbound) TransformResponse(ctx context.Context, resp *httpclient.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("响应为空")
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("上游 HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	unwrapped, err := unwrapAntigravityBody(resp.Body)
	if err != nil {
		return nil, err
	}
	clone := *resp
	clone.Body = unwrapped
	return t.inner.TransformResponse(ctx, &clone)
}

// TransformStream 把 SSE 事件流解包后交给内层 gemini 转换器。
func (t *geminiCLIOutbound) TransformStream(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	unwrapped, err := unwrapAntigravityStream(stream)
	if err != nil {
		return nil, err
	}
	return t.inner.TransformStream(ctx, req, unwrapped)
}

// TransformError 把上游错误转成统一错误响应。
func (t *geminiCLIOutbound) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return t.inner.TransformError(ctx, err)
}

// AggregateStreamChunks 把流式分片聚合成完整响应。
func (t *geminiCLIOutbound) AggregateStreamChunks(ctx context.Context, req *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return t.inner.AggregateStreamChunks(ctx, req, chunks)
}

// unwrapAntigravityBody 从双层响应里取出内层 response。
func unwrapAntigravityBody(body []byte) ([]byte, error) {
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析 antigravity 响应信封: %w", err)
	}
	if len(envelope.Response) == 0 {
		return nil, fmt.Errorf("antigravity 响应的 response 字段为空")
	}
	return envelope.Response, nil
}

// unwrapAntigravityStream 逐事件解包 SSE 流里的 response 字段。
func unwrapAntigravityStream(stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	if stream == nil {
		return nil, fmt.Errorf("流为空")
	}
	return streams.Map(stream, func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
		if event == nil || len(event.Data) == 0 {
			return event
		}
		unwrapped, err := unwrapAntigravityBody(event.Data)
		if err != nil {
			// 单个事件解包失败不中断整条流: 上游可能插入非标准事件(如心跳),
			// 原样透传, 让内层转换器按自己的规则判断。
			return event
		}
		return &httpclient.StreamEvent{Data: unwrapped, Type: event.Type, LastEventID: event.LastEventID}
	}), nil
}
