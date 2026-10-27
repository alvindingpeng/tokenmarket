package xfyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// APIFormatXunfeiSpark 是星火 WebSocket 上游在统一 APIFormat 中的标识。
// 星火只有「文本对话」一种形态, 与 OpenAI Chat Completions 的语义最接近,
// 响应侧统一走 llm.Response(chat.completion 形状), 由客户端协议的入站转换器回转。
const APIFormatXunfeiSpark llm.APIFormat = "xunfei/spark_websocket"

// metadataModelKey 在出站请求的 Metadata 中记录本轮上游模型名,
// 供响应回转时写回 llm.Response.Model(星火响应帧本身不带模型名)。
const metadataModelKey = "xunfei_model"

// Config 是星火 WebSocket 出站转换器配置, 凭据取自渠道授权绑定的渠道凭据。
type Config struct {
	// BaseURL 是渠道基地址; 无路径时按 Domain 拼出版本路径, 见 EndpointFromBaseURL。
	BaseURL string
	// Credentials 是讯飞一组凭据: AppID 进请求帧, APIKey/APISecret 只用于 URL 签名。
	Credentials Credentials
	// Domain 是讯飞服务版本(general/generalv2/generalv3/generalv3.5/4.0Ultra);
	// 留空时按上游模型名解析。
	Domain string
}

// Outbound 把统一 llm.Request 转成星火请求帧与签名 WebSocket URL, 并把星火响应帧
// 转回统一 llm.Response。真正拨号收发由 Executor 完成(见 executor.go):
// transformer.Outbound 的请求/响应是 HTTP 形状, 而星火是 WebSocket 帧协议,
// 两者在 executor 的 *httpclient.Request/Response 边界上对接。
type Outbound struct {
	config Config
}

var (
	_ transformer.Outbound               = (*Outbound)(nil)
	_ pipeline.ChannelCustomizedExecutor = (*Outbound)(nil)
)

// NewOutbound 构造星火出站转换器; 凭据不完整或基地址为空直接报错,
// 错误文本只说缺哪个字段, 不回显任何凭据值。
func NewOutbound(config Config) (*Outbound, error) {
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, errors.New("xunfei spark channel base url is empty")
	}
	if config.Credentials.AppID == "" {
		return nil, errors.New("xunfei spark credentials incomplete: app_id is empty")
	}
	if config.Credentials.APIKey == "" {
		return nil, errors.New("xunfei spark credentials incomplete: api_key is empty")
	}
	if config.Credentials.APISecret == "" {
		return nil, errors.New("xunfei spark credentials incomplete: api_secret is empty")
	}
	return &Outbound{config: config}, nil
}

// APIFormat 返回上游协议标识。
func (o *Outbound) APIFormat() llm.APIFormat { return APIFormatXunfeiSpark }

// CustomizeExecutor 把 pipeline 的 HTTP 执行器换成星火 WebSocket 执行器
// (实现 pipeline.ChannelCustomizedExecutor, 由 pipeline 在每次执行前自动调用):
// transformer.Outbound 的请求/响应是 HTTP 形状, 星火是 WebSocket 帧协议,
// Do/DoStream 必须拨 wss、发请求帧、读响应帧流, 不能走 HTTP 传输。
//
// 传入的执行器是 relay 的上游 HTTP 客户端包装, 取出底层 *http.Client 复用同一份
// 代理与拨号配置, 保证 WS 与 HTTP 走同一条出口线路; 已是星火执行器时原样返回
// (relay 可能已显式装好执行器), 都取不到时按直连构造。
func (o *Outbound) CustomizeExecutor(executor pipeline.Executor) pipeline.Executor {
	if spark, ok := executor.(*Executor); ok {
		return spark
	}
	var client *http.Client
	if native, ok := executor.(interface{ GetNativeClient() *http.Client }); ok {
		client = native.GetNativeClient()
	}
	return NewExecutor(client)
}

// TransformRequest 把统一请求转成星火请求帧, 并签出可拨号的 WebSocket URL。
//
// 返回的 httpclient.Request.URL 含派生凭据(authorization 查询参数), 不得进日志或错误文本;
// 请求经 executor 以 WebSocket 握手(GET)发出, 正文是帧的 JSON。
// SkipInboundQueryMerge 置真: 签名 URL 的查询参数由签名决定, 客户端查询参数不得混入。
func (o *Outbound) TransformRequest(_ context.Context, llmReq *llm.Request) (*httpclient.Request, error) {
	if llmReq == nil {
		return nil, fmt.Errorf("%w: xunfei spark request is nil", transformer.ErrInvalidRequest)
	}
	if len(llmReq.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", transformer.ErrInvalidRequest)
	}

	domain := resolveDomain(o.config.Domain, llmReq.Model)
	if domain == "" {
		return nil, fmt.Errorf("%w: xunfei spark domain is not configured (set the channel key domain or use a spark domain as the model name)", transformer.ErrInvalidRequest)
	}
	text, err := sparkMessages(llmReq.Messages)
	if err != nil {
		return nil, err
	}

	chat := ChatConfig{Domain: domain}
	if llmReq.Temperature != nil {
		chat.Temperature = *llmReq.Temperature
	}
	switch {
	case llmReq.MaxTokens != nil:
		chat.MaxTokens = *llmReq.MaxTokens
	case llmReq.MaxCompletionTokens != nil:
		chat.MaxTokens = *llmReq.MaxCompletionTokens
	}

	endpoint, err := EndpointFromBaseURL(o.config.BaseURL, domain)
	if err != nil {
		return nil, err
	}
	signedURL, err := SignURL(endpoint, o.config.Credentials, time.Now())
	if err != nil {
		return nil, err
	}

	frame := &Frame{
		Header:    FrameHeader{AppID: o.config.Credentials.AppID, UID: "switchyard"},
		Parameter: &ChatParameter{Chat: chat},
		Payload:   &FramePayload{Message: &ChatMessages{Text: text}},
	}
	body, err := frame.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode xunfei spark frame: %w", err)
	}

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	return &httpclient.Request{
		Method:                http.MethodGet,
		URL:                   signedURL,
		Headers:               headers,
		Body:                  body,
		RequestType:           string(llm.RequestTypeChat),
		APIFormat:             string(APIFormatXunfeiSpark),
		SkipInboundQueryMerge: true,
		Metadata:              map[string]string{metadataModelKey: llmReq.Model},
	}, nil
}

// TransformResponse 把 executor 聚合出的星火响应帧转成统一响应。
func (o *Outbound) TransformResponse(_ context.Context, httpResp *httpclient.Response) (*llm.Response, error) {
	if httpResp == nil {
		return nil, errors.New("xunfei spark response is nil")
	}
	if httpResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("xunfei spark upstream responded %d", httpResp.StatusCode)
	}
	frame, err := DecodeFrame(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if failure := frame.Err(); failure != nil {
		return nil, ResponseErrorFromFrame(frame)
	}
	return frameCompletion(frame, requestModelName(httpResp.Request)), nil
}

// TransformStream 把逐帧流转成统一响应增量: 每个响应帧一个增量块,
// 终结帧带 finish_reason 与用量, 随后补 [DONE] 哨兵, 由客户端协议转换器收尾成 SSE。
func (o *Outbound) TransformStream(_ context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	if stream == nil {
		return nil, errors.New("xunfei spark stream is nil")
	}
	return &responseStream{src: stream, model: requestModelName(req)}, nil
}

// TransformError 把执行器报出的上游错误(HTTP 握手失败或协议帧错误)转成统一错误。
// 星火协议错误的语义映射在 executor 侧完成并写进错误体, 这里只做形状还原。
func (o *Outbound) TransformError(_ context.Context, rawErr *httpclient.Error) *llm.ResponseError {
	if rawErr == nil {
		return &llm.ResponseError{
			StatusCode: http.StatusBadGateway,
			Detail:     llm.ErrorDetail{Message: "xunfei spark upstream error", Type: "api_error"},
		}
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	detail := llm.ErrorDetail{Type: "api_error"}
	if err := json.Unmarshal(rawErr.Body, &parsed); err == nil && parsed.Error.Message != "" {
		detail.Message = parsed.Error.Message
		if parsed.Error.Type != "" {
			detail.Type = parsed.Error.Type
		}
		detail.Code = codeString(parsed.Error.Code)
	} else if msg := strings.TrimSpace(string(rawErr.Body)); msg != "" {
		detail.Message = msg
	} else if text := http.StatusText(rawErr.StatusCode); text != "" {
		detail.Message = text
	} else {
		detail.Message = "xunfei spark upstream error"
	}

	status := rawErr.StatusCode
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusBadGateway
	}
	return &llm.ResponseError{StatusCode: status, Detail: detail}
}

// AggregateStreamChunks 把已转发的星火响应帧聚合成完整响应正文与用量,
// 供计费与日志在流结束后取得最终正文; 聚合形状与 executor 非流式响应一致。
func (o *Outbound) AggregateStreamChunks(_ context.Context, req *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	agg := &frameAggregate{}
	for _, chunk := range chunks {
		if chunk == nil || len(chunk.Data) == 0 {
			continue
		}
		frame, err := DecodeFrame(chunk.Data)
		if err != nil {
			return nil, llm.ResponseMeta{}, err
		}
		agg.add(frame)
	}
	body, err := agg.encode()
	if err != nil {
		return nil, llm.ResponseMeta{}, err
	}
	meta := llm.ResponseMeta{ID: agg.frame().SID(), Usage: usageFromChat(agg.usage)}
	return body, meta, nil
}

// resolveDomain 决定本轮请求的星火服务版本: 模型名本身就是合法 Domain 时优先用它,
// 否则回落到渠道凭据配置的 Domain。
func resolveDomain(configured, model string) string {
	if _, ok := sparkPathByDomain(model); ok {
		return model
	}
	return strings.TrimSpace(configured)
}

// sparkMessages 把统一多轮消息压成星火上下文: 星火只收 user/assistant/system 三种角色
// 与纯文本内容, 无法表达的形态(工具调用/图片等)直接报请求错误而不是静默丢弃。
func sparkMessages(messages []llm.Message) ([]ChatMessage, error) {
	text := make([]ChatMessage, 0, len(messages))
	for i, message := range messages {
		content, err := messageText(message)
		if err != nil {
			return nil, fmt.Errorf("%w: message %d: %w", transformer.ErrInvalidRequest, i, err)
		}
		text = append(text, ChatMessage{Role: sparkRole(message.Role), Content: content})
	}
	return text, nil
}

// sparkRole 把统一角色映射成星火角色; 星火没有 tool/developer, 一律归到 user 侧上下文。
func sparkRole(role string) string {
	switch role {
	case RoleSystem:
		return RoleSystem
	case RoleAssistant:
		return RoleAssistant
	default:
		return RoleUser
	}
}

// messageText 取出一条消息的纯文本内容; 带工具调用或非文本分片时返回错误,
// 避免把无法表达的形态悄悄丢掉后打向星火。
func messageText(message llm.Message) (string, error) {
	if len(message.ToolCalls) > 0 {
		return "", errors.New("tool calls are not supported by the xunfei spark websocket protocol")
	}
	if message.Content.Content != nil {
		return *message.Content.Content, nil
	}
	var b strings.Builder
	for _, part := range message.Content.MultipleContent {
		if part.Type != "text" || part.Text == nil {
			return "", fmt.Errorf("content part %q is not supported by the xunfei spark websocket protocol", part.Type)
		}
		b.WriteString(*part.Text)
	}
	return b.String(), nil
}

// frameCompletion 把聚合帧转成完整统一响应。
func frameCompletion(frame *Frame, model string) *llm.Response {
	content := frame.Content()
	finish := "stop"
	return &llm.Response{
		ID:      completionID(frame),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []llm.Choice{{
			Index:        0,
			Message:      &llm.Message{Role: RoleAssistant, Content: llm.MessageContent{Content: &content}},
			FinishReason: &finish,
		}},
		Usage: usageFromChat(frame.Usage()),
	}
}

// frameChunk 把单个响应帧转成统一增量块; 无内容且未终结的帧返回 nil(不产生客户端事件)。
func frameChunk(frame *Frame, model string) *llm.Response {
	content := frame.Content()
	if content == "" && !frame.Done() {
		return nil
	}
	chunk := &llm.Response{
		ID:      completionID(frame),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []llm.Choice{{
			Index: 0,
			Delta: &llm.Message{Role: RoleAssistant, Content: llm.MessageContent{Content: &content}},
		}},
	}
	if frame.Done() {
		finish := "stop"
		chunk.Choices[0].FinishReason = &finish
		chunk.Usage = usageFromChat(frame.Usage())
	}
	return chunk
}

// completionID 用星火会话 ID 派生响应 ID, 没有会话 ID 时用固定前缀。
func completionID(frame *Frame) string {
	if sid := frame.SID(); sid != "" {
		return "chatcmpl-" + sid
	}
	return "chatcmpl"
}

// usageFromChat 转换星火用量; 星火逐帧累计下发, 聚合时以最后一帧为准。
func usageFromChat(usage *ChatUsage) *llm.Usage {
	if usage == nil {
		return nil
	}
	return &llm.Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

// requestModelName 取出请求元数据里记录的本轮上游模型名。
func requestModelName(request *httpclient.Request) string {
	if request == nil || request.Metadata == nil {
		return ""
	}
	return request.Metadata[metadataModelKey]
}

// codeString 把 JSON 里的 code(字符串或数字)统一转成字符串。
func codeString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// responseStream 把星火响应帧流转成统一响应增量流: 终结帧之后补 [DONE] 哨兵,
// 协议帧错误在流内转成统一错误, 由上层按错误终态收尾。
type responseStream struct {
	src     streams.Stream[*httpclient.StreamEvent]
	model   string
	pending []*llm.Response
	current *llm.Response
	err     error
	done    bool
}

func (s *responseStream) Next() bool {
	for {
		if len(s.pending) > 0 {
			s.current = s.pending[0]
			s.pending = s.pending[1:]
			return true
		}
		if s.done || s.err != nil {
			return false
		}
		if !s.src.Next() {
			s.err = s.src.Err()
			s.done = true
			return false
		}
		event := s.src.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		frame, err := DecodeFrame(event.Data)
		if err != nil {
			s.err = err
			s.done = true
			return false
		}
		if failure := frame.Err(); failure != nil {
			s.err = ResponseErrorFromFrame(frame)
			s.done = true
			return false
		}
		if chunk := frameChunk(frame, s.model); chunk != nil {
			s.pending = append(s.pending, chunk)
		}
		if frame.Done() {
			s.pending = append(s.pending, llm.DoneResponse)
			s.done = true
		}
	}
}

func (s *responseStream) Current() *llm.Response { return s.current }

func (s *responseStream) Err() error { return s.err }

func (s *responseStream) Close() error { return s.src.Close() }
