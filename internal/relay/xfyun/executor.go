package xfyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/rhttp"
	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

// Executor 以 WebSocket 执行星火上游请求: 拨签名 URL、发一帧请求、读响应帧流。
// 实现 pipeline.Executor, 供 relay 的跨协议 pipeline 复用同一套中间件与终态校验:
// Do 把整段帧流聚合成一个响应帧(非流式), DoStream 逐帧产出流事件(流式)。
//
// 凭据安全: 所有错误文本与 httpclient.Error 都不得携带签名 URL 或 authorization 查询参数,
// 一律经 scrubError / RedactURL 处理; 拨号与读写阶段都不打印任何日志。
type Executor struct {
	dialer *websocket.Dialer
}

var _ pipeline.Executor = (*Executor)(nil)

// NewExecutor 构造星火 WebSocket 执行器。client 取自 relay 的上游客户端,
// 复用它的代理与拨号配置(含 SOCKS 代理与私网防护), 保证 WS 与 HTTP 走同一条出口线路;
// client 为空时直连。
func NewExecutor(client *http.Client) *Executor {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}
	if transport := rhttp.BaseTransport(client); transport != nil {
		dialer.Proxy = transport.Proxy
		dialer.NetDialContext = transport.DialContext
		if transport.TLSClientConfig != nil {
			dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
	}
	return &Executor{dialer: dialer}
}

// Do 拨通星火 WebSocket, 发出请求帧并读完整段响应帧流, 聚合成一个响应帧返回。
// 协议帧错误(header.code != 0)以 *httpclient.Error 返回, 由出站转换器映射成统一错误。
func (e *Executor) Do(ctx context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	conn, err := e.open(ctx, request)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	release := watchContext(ctx, conn)
	defer release()

	agg := &frameAggregate{}
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil, readFailure(ctx, request, err)
		}
		if len(data) == 0 {
			continue
		}
		frame, err := DecodeFrame(data)
		if err != nil {
			return nil, scrubError(err, request)
		}
		if failure := sparkHTTPError(request, frame); failure != nil {
			return nil, failure
		}
		agg.add(frame)
		if frame.Done() {
			break
		}
	}

	body, err := agg.encode()
	if err != nil {
		return nil, scrubError(err, request)
	}
	return &httpclient.Response{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
		Request:    request,
	}, nil
}

// DoStream 拨通星火 WebSocket 并把响应帧逐帧产出为流事件, 事件正文即原始响应帧 JSON。
// 帧的解析与统一响应转换由出站转换器的 TransformStream 完成。
func (e *Executor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	conn, err := e.open(ctx, request)
	if err != nil {
		return nil, err
	}
	return &frameStream{
		conn:    conn,
		ctx:     ctx,
		request: request,
		release: watchContext(ctx, conn),
	}, nil
}

// open 建立 WebSocket 连接并发出请求帧。握手与写入失败都不携带签名 URL。
func (e *Executor) open(ctx context.Context, request *httpclient.Request) (*websocket.Conn, error) {
	if request == nil || request.URL == "" {
		return nil, errors.New("xunfei spark request url is empty")
	}
	header := http.Header{}
	for key, values := range request.Headers {
		// WebSocket 握手只透传渠道自定义 Header; 凭据不在 Header 里(鉴权只在 URL 签名),
		// 协议自有头交给 gorilla 处理, 避免它拒绝重复/保留头。
		if httpclient.IsSensitiveHeader(key) || isWebSocketReservedHeader(key) {
			continue
		}
		for _, value := range values {
			header.Add(key, value)
		}
	}

	conn, response, err := e.dialer.DialContext(ctx, request.URL, header)
	if err != nil {
		return nil, dialFailure(request, response, err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, request.Body); err != nil {
		conn.Close()
		return nil, scrubError(fmt.Errorf("xunfei spark websocket write: %w", err), request)
	}
	return conn, nil
}

// isWebSocketReservedHeader 判定 gorilla 会拒绝或自管的握手头。
func isWebSocketReservedHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Host", "Upgrade", "Connection", "Sec-Websocket-Key", "Sec-Websocket-Version",
		"Sec-Websocket-Extensions", "Sec-Websocket-Protocol", "Sec-Websocket-Accept",
		"Content-Length", "Accept-Encoding":
		return true
	default:
		return false
	}
}

// watchContext 在 ctx 结束时关闭连接, 让阻塞中的读写立刻返回。
// 返回的函数用于正常收尾时停止监听, 避免 goroutine 挂到请求结束之后。
func watchContext(ctx context.Context, conn *websocket.Conn) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

// frameStream 逐帧读取星火响应流。
type frameStream struct {
	conn     *websocket.Conn
	ctx      context.Context
	request  *httpclient.Request
	release  func()
	current  *httpclient.StreamEvent
	err      error
	finished bool
	closed   bool
}

func (s *frameStream) Next() bool {
	if s.err != nil || s.finished {
		return false
	}
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			s.err = readFailure(s.ctx, s.request, err)
			return false
		}
		if len(data) == 0 {
			continue
		}
		s.current = &httpclient.StreamEvent{Data: append([]byte(nil), data...)}
		// 终结帧之后不再读: 服务端可能保持连接, 一直读会挂住请求收尾。
		if frame, decodeErr := DecodeFrame(data); decodeErr == nil && frame.Done() {
			s.finished = true
		}
		return true
	}
}

func (s *frameStream) Current() *httpclient.StreamEvent { return s.current }

func (s *frameStream) Err() error { return s.err }

func (s *frameStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.release()
	return s.conn.Close()
}

// frameAggregate 累计整段响应帧流: 文本拼接, 用量以最后一帧为准(星火逐帧累计下发)。
type frameAggregate struct {
	strings.Builder
	usage *ChatUsage
	sid   string
}

func (a *frameAggregate) add(frame *Frame) {
	a.WriteString(frame.Content())
	if usage := frame.Usage(); usage != nil {
		a.usage = usage
	}
	if sid := frame.SID(); sid != "" {
		a.sid = sid
	}
}

// frame 输出聚合后的响应帧, 形状与单帧响应一致, 可被 DecodeFrame 原样解析。
func (a *frameAggregate) frame() *Frame {
	return &Frame{
		Header: FrameHeader{SID: a.sid, Status: StatusComplete},
		Payload: &FramePayload{
			Choices: &ChatChoices{
				Status: StatusComplete,
				Text:   []ChatChoice{{Role: RoleAssistant, Content: a.String()}},
			},
			Usage: a.usage,
		},
	}
}

func (a *frameAggregate) encode() ([]byte, error) { return a.frame().Encode() }

// dialFailure 把握手失败转成执行器错误: HTTP 层失败以 *httpclient.Error 承载状态码与错误体
// (供出站转换器还原成统一错误), 其余网络失败按临时故障文本返回。
func dialFailure(request *httpclient.Request, response *http.Response, err error) error {
	if response != nil && response.StatusCode >= http.StatusBadRequest {
		body := readErrorBody(response)
		return &httpclient.Error{
			Method:     request.Method,
			URL:        RedactURL(request.URL),
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Body:       body,
			Headers:    response.Header.Clone(),
		}
	}
	return scrubError(fmt.Errorf("xunfei spark websocket dial %s: %w", RedactURL(request.URL), err), request)
}

// readErrorBody 读握手失败响应体, 上限 64KiB, 防异常响应打爆内存。
func readErrorBody(response *http.Response) []byte {
	defer response.Body.Close()
	limited := make([]byte, 64<<10)
	n, _ := response.Body.Read(limited)
	return limited[:n]
}

// readFailure 把读取失败归类: ctx 结束按取消/超时返回(保留 errors.Is 语义),
// 其余按网络故障文本返回, 供上游错误分类识别为临时故障。
func readFailure(ctx context.Context, request *httpclient.Request, err error) error {
	if ctx.Err() != nil {
		return scrubError(fmt.Errorf("xunfei spark websocket: %w", ctx.Err()), request)
	}
	return scrubError(fmt.Errorf("xunfei spark websocket read: %w", err), request)
}

// sparkHTTPError 把响应帧的协议错误转成 *httpclient.Error; 正常帧返回 nil。
// 状态码与错误类型按讯飞公开错误码口径映射(mapSparkError), 错误体只含码/信息/会话 ID。
func sparkHTTPError(request *httpclient.Request, frame *Frame) *httpclient.Error {
	if frame == nil || frame.Err() == nil {
		return nil
	}
	detail := responseErrorFromFrame(frame)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": detail.Detail.Message,
		"type":    detail.Detail.Type,
		"code":    detail.Detail.Code,
	}})
	return &httpclient.Error{
		Method:     http.MethodGet,
		URL:        RedactURL(request.URL),
		StatusCode: detail.StatusCode,
		Status:     http.StatusText(detail.StatusCode),
		Body:       body,
	}
}

// ResponseErrorFromFrame 把响应帧的协议错误映射成统一错误(流内错误与帧错误共用)。
func ResponseErrorFromFrame(frame *Frame) *llm.ResponseError {
	return responseErrorFromFrame(frame)
}

// responseErrorFromFrame 是 ResponseErrorFromFrame 的内部实现。
func responseErrorFromFrame(frame *Frame) *llm.ResponseError {
	failure, ok := frame.Err().(*Error)
	if !ok || failure == nil {
		return &llm.ResponseError{
			StatusCode: http.StatusBadGateway,
			Detail:     llm.ErrorDetail{Message: "xunfei spark upstream error", Type: "api_error"},
		}
	}
	detail := llm.ErrorDetail{
		Message: failure.Error(),
		Code:    fmt.Sprintf("%d", failure.Code),
	}
	status, errorType := mapSparkError(failure.Code)
	detail.Type = errorType
	// 分类文本带上讯飞原始错误信息; 帧错误只含码/信息/sid, 不含凭据。
	if failure.Message != "" {
		detail.Message = failure.Message
	}
	return &llm.ResponseError{StatusCode: status, Detail: detail}
}

// mapSparkError 把星火协议错误码映射为 (HTTP 状态码, 统一错误类型)。
// 取值按讯飞开放平台公开错误码口径归类: 10xxx 为通用码, 112xx 为星火鉴权码;
// 统一错误类型直接喂给 relay 的上游错误分类(auth/quota/rate/client 语义),
// 未收录的码一律按上游故障 502 处理, 不猜语义。
func mapSparkError(code int) (int, string) {
	switch code {
	// 参数/请求形态错误: 换渠道必然同样失败。
	case 10001, 10002, 10003, 10004, 10005, 10013:
		return http.StatusBadRequest, "invalid_request_error"
	// 频控与并发: 按限流处理, 由选路侧跳过受限成员。
	case 10006, 10110, 10111, 10112, 10113, 10114, 10907:
		return http.StatusTooManyRequests, "rate_limit_exceeded"
	// 凭据与授权: 立即熔断 Key 并换渠道。
	case 10007, 10008, 10106, 10108, 10109, 11200:
		return http.StatusUnauthorized, "authentication_error"
	case 10105, 11201:
		return http.StatusForbidden, "permission_error"
	// 额度/余额耗尽。
	case 10009:
		return http.StatusPaymentRequired, "insufficient_quota"
	// 服务忙/过载。
	case 10010:
		return http.StatusServiceUnavailable, "server is busy"
	// 请求超时。
	case 10011:
		return http.StatusGatewayTimeout, "timeout"
	// 其余(含 10000 未知错误与 10012 请求失败)按上游故障处理。
	default:
		return http.StatusBadGateway, "api_error"
	}
}

// scrubError 清除错误文本里的派生凭据: 签名 URL 的 authorization 查询参数解码即 api_key
// 与签名原文, 与明文凭据同级对待。文本无需清理时原样返回, 否则包一层保留 errors.Is 语义。
func scrubError(err error, request *httpclient.Request) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if request != nil && request.URL != "" {
		redacted := RedactURL(request.URL)
		text = strings.ReplaceAll(text, request.URL, redacted)
		text = strings.ReplaceAll(text, url.QueryEscape(request.URL), redacted)
		if auth := signedAuthorization(request.URL); auth != "" {
			text = strings.ReplaceAll(text, auth, "<redacted>")
			text = strings.ReplaceAll(text, url.QueryEscape(auth), "<redacted>")
		}
	}
	if text == err.Error() {
		return err
	}
	return &scrubbedError{text: text, cause: err}
}

// signedAuthorization 取出签名 URL 里的 authorization 查询参数值。
func signedAuthorization(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("authorization")
}

// scrubbedError 是清理过凭据文本的错误包装, 保留底层错误以兼容 errors.Is/As。
type scrubbedError struct {
	text  string
	cause error
}

func (e *scrubbedError) Error() string { return e.text }

func (e *scrubbedError) Unwrap() error { return e.cause }
