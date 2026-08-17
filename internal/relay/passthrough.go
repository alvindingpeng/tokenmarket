package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

// passthroughStreamResponse 保存已验证首事件但尚未提交的原始上游流。
type passthroughStreamResponse struct {
	status   int                      // 上游成功响应状态码。
	headers  http.Header              // 上游响应头。
	decoder  httpclient.StreamDecoder // decoder 持有并读取上游响应体。
	captured *bytes.Buffer            // captured 保存提交前和事件间已经读取的原始字节。
	first    *httpclient.StreamEvent  // 已验证的第一个非空事件。
	terminal bool                     // 首事件已经正常结束响应流。
	protocol *relayProtocol           // protocol 负责判断后续流事件状态。
	outbound transformer.Outbound     // outbound 负责旁路聚合原始上游事件。
	request  *httpclient.Request      // 聚合流事件所需的上游请求。
	cancel   context.CancelFunc       // cancel 关闭上游流上下文。
}

// executePassthrough 构造同协议请求并执行上游调用，在客户端提交前验证响应。
func (f *forwarder) executePassthrough(ctx context.Context, modelName string, channel *model.Channel) upstreamResult {
	request, err := buildPassthrough(f.protocol, f.request.raw, modelName, channel)
	if err != nil {
		return upstreamResult{err: err}
	}
	outbound, err := newOutbound(channel.Type, channel.BaseURL, channel.Key)
	if err != nil {
		return upstreamResult{err: fmt.Errorf("%w: %v", errUnsupportedTarget, err)}
	}
	if f.request.stream {
		if f.protocol.format == llm.APIFormatOpenAIChatCompletion {
			request.Body, err = sjson.SetBytes(request.Body, "stream_options.include_usage", true)
			if err != nil {
				return upstreamResult{err: err}
			}
		}
		return f.executePassthroughStream(ctx, request, outbound)
	}

	response, err := httpclient.NewHttpClientWithClient(f.client).Do(ctx, request)
	if err != nil {
		result := upstreamResult{err: err}
		var upstream *httpclient.Error
		if errors.As(err, &upstream) {
			result.responseBody = upstream.Body
		}
		return result
	}
	parsed, err := outbound.TransformResponse(ctx, response)
	if err != nil {
		return upstreamResult{responseBody: slices.Clone(response.Body), err: err}
	}
	if err := validateUnifiedResponse(f.protocol.format, parsed); err != nil {
		return upstreamResult{responseBody: slices.Clone(response.Body), usage: parsed.Usage, err: err}
	}
	return upstreamResult{response: &bufferedResponse{status: response.StatusCode, headers: response.Headers.Clone(), body: slices.Clone(response.Body)}, usage: parsed.Usage}
}

// executePassthroughStream 预读并验证原始 SSE 的第一个非空事件。
func (f *forwarder) executePassthroughStream(ctx context.Context, request *httpclient.Request, outbound transformer.Outbound) upstreamResult {
	streamCtx, cancel := context.WithCancel(ctx)

	rawRequest, err := httpclient.BuildHttpRequest(streamCtx, request)
	if err != nil {
		cancel()
		return upstreamResult{err: err}
	}
	if accept := rawRequest.Header.Get("Accept"); accept == "" || strings.EqualFold(accept, "application/json") {
		rawRequest.Header.Set("Accept", "text/event-stream")
	}
	rawRequest.Header.Set("Cache-Control", "no-cache")
	rawResponse, err := f.client.Do(rawRequest)
	if err != nil {
		cancel()
		return upstreamResult{err: err}
	}
	if rawResponse.StatusCode >= http.StatusBadRequest {
		body, readErr := io.ReadAll(rawResponse.Body)
		rawResponse.Body.Close()
		cancel()
		if readErr != nil {
			return upstreamResult{err: readErr}
		}
		return upstreamResult{responseBody: body, err: &httpclient.Error{Method: rawRequest.Method, URL: rawRequest.URL.String(), StatusCode: rawResponse.StatusCode, Status: rawResponse.Status, Body: body, Headers: rawResponse.Header}}
	}

	captured := &bytes.Buffer{}
	reader := struct {
		io.Reader
		io.Closer
	}{Reader: io.TeeReader(rawResponse.Body, captured), Closer: rawResponse.Body}
	decoder := httpclient.NewDefaultSSEDecoder(streamCtx, reader)
	type firstEventResult struct {
		event *httpclient.StreamEvent // 读取到的第一个非空事件。
		err   error                   // 首事件前发生的读取错误。
	}
	first := make(chan firstEventResult, 1)
	go func() {
		for decoder.Next() {
			if event := decoder.Current(); event != nil && len(event.Data) > 0 {
				first <- firstEventResult{event: event}
				return
			}
		}
		err := decoder.Err()
		if err == nil {
			err = errors.New("stream ended before first event")
		}
		first <- firstEventResult{err: err}
	}()

	var firstResult firstEventResult
	select {
	case <-ctx.Done():
		cancel()
		decoder.Close()
		return upstreamResult{err: ctx.Err()}
	case firstResult = <-first:
	}
	if firstResult.err != nil {
		cancel()
		decoder.Close()
		return upstreamResult{responseBody: slices.Clone(captured.Bytes()), err: firstResult.err}
	}
	terminal, terminalErr := f.protocol.streamTerminal(firstResult.event)
	if terminalErr != nil {
		cancel()
		decoder.Close()
		return upstreamResult{responseBody: slices.Clone(captured.Bytes()), err: terminalErr}
	}
	return upstreamResult{response: &passthroughStreamResponse{status: rawResponse.StatusCode, headers: rawResponse.Header.Clone(), decoder: decoder, captured: captured, first: firstResult.event, terminal: terminal, protocol: f.protocol, outbound: outbound, request: request, cancel: cancel}}
}

// Commit 写出已验证的首事件，随后原样转发同一上游流。
func (r *passthroughStreamResponse) Commit(ctx context.Context, c *gin.Context) commitResult {
	copyHeaders(c.Writer.Header(), r.headers)
	c.Header("X-Accel-Buffering", "no")
	c.Status(r.status)
	result := commitResult{}
	events := []*httpclient.StreamEvent{r.first}
	terminal := r.terminal

	for {
		if r.captured.Len() > 0 {
			if _, err := c.Writer.Write(r.captured.Bytes()); err != nil {
				result.err = fmt.Errorf("%w: %v", errClientWrite, err)
				break
			}
			r.captured.Reset()
			if result.firstWriteAt.IsZero() {
				result.firstWriteAt = time.Now()
			}
			c.Writer.Flush()
		}
		if terminal {
			break
		}
		if !r.decoder.Next() {
			if ctx.Err() != nil {
				result.err = ctx.Err()
			} else if result.err = r.decoder.Err(); result.err == nil {
				result.err = responses.ErrStreamIncomplete
			}
			break
		}
		event := r.decoder.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		events = append(events, event)
		terminal, result.err = r.protocol.streamTerminal(event)
		if result.err != nil {
			terminal = true
		}
	}

	// 流已提交后，即使尾部不足以组成完整 SSE 事件，也必须原样转发已读取的字节。
	if r.captured.Len() > 0 && ctx.Err() == nil && !errors.Is(result.err, errClientWrite) {
		if _, err := c.Writer.Write(r.captured.Bytes()); err != nil {
			if result.err == nil {
				result.err = fmt.Errorf("%w: %v", errClientWrite, err)
			}
		} else {
			if result.firstWriteAt.IsZero() {
				result.firstWriteAt = time.Now()
			}
			c.Writer.Flush()
		}
		r.captured.Reset()
	}

	if body, meta, err := r.outbound.AggregateStreamChunks(context.WithoutCancel(ctx), r.request, events); err == nil {
		result.responseBody = body
		result.usage = meta.Usage
	}
	return result
}

// Close 关闭原始上游流及其上下文。
func (r *passthroughStreamResponse) Close() error {
	r.cancel()
	return r.decoder.Close()
}

// buildPassthrough 构造同协议上游请求。
func buildPassthrough(protocol *relayProtocol, inbound *httpclient.Request, modelName string, channel *model.Channel) (*httpclient.Request, error) {
	body, err := sjson.SetBytes(inbound.Body, "model", modelName)
	if err != nil {
		return nil, err
	}
	baseURL := channel.BaseURL
	rawURL := strings.HasSuffix(baseURL, "##")
	baseURL = transformer.BuildRequestURL(strings.TrimSuffix(baseURL, "##"), "v1", protocol.route, "", rawURL)
	contentType := inbound.Headers.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	auth := &httpclient.AuthConfig{Type: protocol.authType, APIKey: channel.Key}
	if protocol.authType == httpclient.AuthTypeAPIKey {
		auth.HeaderKey = "X-API-Key"
	}
	request := httpclient.MergeInboundRequest(&httpclient.Request{Method: inbound.Method, URL: baseURL, Headers: http.Header{"Accept": []string{"application/json"}, "Content-Type": []string{contentType}}, Body: body, Auth: auth, APIFormat: protocol.format.String()}, inbound)
	request, err = httpclient.FinalizeAuthHeaders(request)
	if err != nil {
		return nil, err
	}
	if err := applyChannelOptions(channel, request); err != nil {
		return nil, err
	}
	return request, nil
}
