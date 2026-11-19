package xfyun

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// testCredentials 是测试用假凭据, 用于验证错误文本不回显明文凭据。
var testCredentials = Credentials{AppID: "test-app-id", APIKey: "test-api-key", APISecret: "test-api-secret"}

// startSparkServer 起一个最小星火 WebSocket 服务端: 读一帧请求后由 respond 决定回帧。
// 返回可直接作为渠道基地址使用的 ws:// 地址。
func startSparkServer(t *testing.T, respond func(conn *websocket.Conn, request []byte)) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		respond(conn, request)
	}))
	t.Cleanup(server.Close)
	return "ws://" + strings.TrimPrefix(server.URL, "http://")
}

// writeFrame 写出一个响应帧。
func writeFrame(t *testing.T, conn *websocket.Conn, frame *Frame) {
	t.Helper()
	data, err := frame.Encode()
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// contentFrame 构造一个增量响应帧。
func contentFrame(sid, content string, status int) *Frame {
	return &Frame{
		Header:  FrameHeader{SID: sid, Status: status},
		Payload: &FramePayload{Choices: &ChatChoices{Status: status, Text: []ChatChoice{{Role: RoleAssistant, Content: content}}}},
	}
}

// chatRequest 构造一个最小统一聊天请求。
func chatRequest() *llm.Request {
	text := "hello"
	return &llm.Request{
		Model:    "generalv3.5",
		Messages: []llm.Message{{Role: RoleUser, Content: llm.MessageContent{Content: &text}}},
	}
}

// newTestOutbound 构造指向本地服务端的出站转换器。
func newTestOutbound(t *testing.T, baseURL string) *Outbound {
	t.Helper()
	outbound, err := NewOutbound(Config{BaseURL: baseURL, Credentials: testCredentials, Domain: "generalv3.5"})
	if err != nil {
		t.Fatalf("new outbound: %v", err)
	}
	return outbound
}

// TestExecutorDoAggregatesFrames 非流式: 多帧响应聚合成一个完整响应, 用量取最后一帧。
func TestExecutorDoAggregatesFrames(t *testing.T) {
	baseURL := startSparkServer(t, func(conn *websocket.Conn, request []byte) {
		writeFrame(t, conn, contentFrame("sid-1", "Hello", StatusStreaming))
		writeFrame(t, conn, contentFrame("sid-1", " world", StatusStreaming))
		final := contentFrame("sid-1", "", StatusComplete)
		final.Payload.Usage = &ChatUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
		writeFrame(t, conn, final)
	})

	outbound := newTestOutbound(t, baseURL)
	request, err := outbound.TransformRequest(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	executor := NewExecutor(nil)
	response, err := executor.Do(context.Background(), request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if response.Request != request {
		t.Fatalf("response must carry the originating request for model metadata")
	}

	result, err := outbound.TransformResponse(context.Background(), response)
	if err != nil {
		t.Fatalf("transform response: %v", err)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content.Content == nil {
		t.Fatalf("missing choices: %+v", result)
	}
	if got := *result.Choices[0].Message.Content.Content; got != "Hello world" {
		t.Fatalf("content = %q", got)
	}
	if result.Model != "generalv3.5" {
		t.Fatalf("model = %q", result.Model)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.Choices[0].FinishReason == nil || *result.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish reason = %v", result.Choices[0].FinishReason)
	}
}

// TestExecutorDoStreamFeedsTransformStream 流式: 逐帧转成统一增量, 终结帧带用量, 末尾补 [DONE]。
func TestExecutorDoStreamFeedsTransformStream(t *testing.T) {
	baseURL := startSparkServer(t, func(conn *websocket.Conn, request []byte) {
		writeFrame(t, conn, contentFrame("sid-2", "Hi", StatusStreaming))
		final := contentFrame("sid-2", "!", StatusComplete)
		final.Payload.Usage = &ChatUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
		writeFrame(t, conn, final)
	})

	outbound := newTestOutbound(t, baseURL)
	request, err := outbound.TransformRequest(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	events, err := NewExecutor(nil).DoStream(context.Background(), request)
	if err != nil {
		t.Fatalf("do stream: %v", err)
	}
	defer events.Close()

	chunks, err := outbound.TransformStream(context.Background(), request, events)
	if err != nil {
		t.Fatalf("transform stream: %v", err)
	}

	var content string
	var objects []string
	var usage *llm.Usage
	for chunks.Next() {
		chunk := chunks.Current()
		objects = append(objects, chunk.Object)
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && chunk.Choices[0].Delta.Content.Content != nil {
			content += *chunk.Choices[0].Delta.Content.Content
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if err := chunks.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if content != "Hi!" {
		t.Fatalf("content = %q", content)
	}
	if usage == nil || usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	if len(objects) < 2 || objects[len(objects)-1] != "[DONE]" {
		t.Fatalf("objects = %v", objects)
	}
}

// TestExecutorMapsProtocolError 协议帧错误映射: 11200 鉴权失败归 auth 语义, 错误体不含凭据。
func TestExecutorMapsProtocolError(t *testing.T) {
	baseURL := startSparkServer(t, func(conn *websocket.Conn, request []byte) {
		writeFrame(t, conn, &Frame{Header: FrameHeader{Code: 11200, Message: "auth failed", SID: "sid-3"}})
	})

	outbound := newTestOutbound(t, baseURL)
	request, err := outbound.TransformRequest(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	_, err = NewExecutor(nil).Do(context.Background(), request)
	if err == nil {
		t.Fatal("protocol error must fail the attempt")
	}
	var httpErr *httpclient.Error
	if !errors.As(err, &httpErr) {
		t.Fatalf("want *httpclient.Error, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", httpErr.StatusCode)
	}

	failure := outbound.TransformError(context.Background(), httpErr)
	if failure.StatusCode != http.StatusUnauthorized || failure.Detail.Type != "authentication_error" {
		t.Fatalf("mapped error = %+v", failure)
	}
	assertNoCredentialLeak(t, err.Error())
	assertNoCredentialLeak(t, string(httpErr.Body))
	assertNoCredentialLeak(t, httpErr.URL)
}

// TestExecutorContextCancel 取消语义: ctx 取消后读循环立刻返回, 错误保留 context.Canceled。
func TestExecutorContextCancel(t *testing.T) {
	baseURL := startSparkServer(t, func(conn *websocket.Conn, request []byte) {
		writeFrame(t, conn, contentFrame("sid-4", "partial", StatusStreaming))
		// 不发终结帧, 一直挂着, 直到客户端取消导致连接关闭。
		time.Sleep(2 * time.Second)
	})

	outbound := newTestOutbound(t, baseURL)
	request, err := outbound.TransformRequest(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = NewExecutor(nil).Do(ctx, request)
	if err == nil {
		t.Fatal("canceled read must fail")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	assertNoCredentialLeak(t, err.Error())
}

// TestOutboundTransformRequestSignsAndRedacts 请求构造: 签名 URL 含 authorization,
// 展示与错误路径经 RedactURL/scrubError 后不携带派生凭据。
func TestOutboundTransformRequestSignsAndRedacts(t *testing.T) {
	outbound := newTestOutbound(t, "wss://spark-api-open.xf-yun.com")
	request, err := outbound.TransformRequest(context.Background(), chatRequest())
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}
	if !strings.Contains(request.URL, "authorization=") {
		t.Fatalf("signed url missing authorization: %s", RedactURL(request.URL))
	}
	if strings.Contains(RedactURL(request.URL), "authorization=") {
		t.Fatalf("redaction failed: %s", RedactURL(request.URL))
	}

	requestErr := errors.New("dial failed for " + request.URL)
	scrubbed := scrubError(requestErr, request)
	assertNoCredentialLeak(t, scrubbed.Error())
	if !errors.Is(scrubbed, requestErr) {
		t.Fatalf("scrub must keep the wrapped cause")
	}
}

// TestOutboundCustomizeExecutor pipeline.ChannelCustomizedExecutor 挂钩:
// 星火执行器原样返回(relay 已显式装好时), HTTP 执行器换装成 WebSocket 执行器
// 并复用底层客户端的代理与拨号配置。
func TestOutboundCustomizeExecutor(t *testing.T) {
	outbound := newTestOutbound(t, "wss://spark-api-open.xf-yun.com")

	spark := NewExecutor(nil)
	if got := outbound.CustomizeExecutor(spark); got != spark {
		t.Fatalf("customize executor = %T, want the same *Executor", got)
	}

	proxyURL, err := url.Parse("http://proxy.internal:8080")
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:       http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
	}}
	got := outbound.CustomizeExecutor(httpclient.NewHttpClientWithClient(client))
	installed, ok := got.(*Executor)
	if !ok {
		t.Fatalf("customize executor = %T, want *Executor", got)
	}
	if installed.dialer.NetDialContext == nil {
		t.Fatal("customized executor must reuse the native client dialer")
	}
	chosen, err := installed.dialer.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "spark-api-open.xf-yun.com"}})
	if err != nil || chosen == nil || chosen.String() != proxyURL.String() {
		t.Fatalf("customized executor proxy = %v (%v), want %s", chosen, err, proxyURL)
	}
}

// assertNoCredentialLeak 断言文本不含任何明文凭据或派生凭据。
func assertNoCredentialLeak(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{testCredentials.AppID, testCredentials.APIKey, testCredentials.APISecret, "authorization="} {
		if strings.Contains(text, secret) {
			t.Fatalf("credential leaked in %q", text)
		}
	}
}
