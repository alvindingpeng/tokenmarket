package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// convertedStreamResponse 保存已验证首事件的客户端协议转换流。
type convertedStreamResponse struct {
	protocol *relayProtocol                         // protocol 负责校验和聚合客户端协议事件。
	stream   streams.Stream[*httpclient.StreamEvent] // AxonHub 转换后的客户端事件流。
	first    *httpclient.StreamEvent                // 已验证的第一个非空事件。
	terminal bool                                   // 首事件已经正常结束响应流。
}

// forwardMiddleware 应用渠道覆盖并收集 AxonHub pipeline 状态。
type forwardMiddleware struct {
	pipeline.DummyMiddleware                // DummyMiddleware 提供未使用钩子的默认实现。
	channel                  *model.Channel // 当前上游渠道配置。
	responseBody             []byte         // 上游错误响应内容。
	response                 *llm.Response  // 出站协议解析后的统一响应。
}

// executeConverted 通过 AxonHub pipeline 请求上游并在提交前验证客户端协议响应。
func (f *forwarder) executeConverted(ctx context.Context, modelName string, channel *model.Channel) upstreamResult {
	raw := cloneRequest(f.request.raw, ctx)
	request, err := f.protocol.inbound.TransformRequest(ctx, raw)
	if err != nil {
		return upstreamResult{err: err}
	}
	request.Model = modelName
	outbound, err := newOutbound(channel.Type, channel.BaseURL, channel.Key)
	if err != nil {
		return upstreamResult{err: fmt.Errorf("%w: %v", errUnsupportedTarget, err)}
	}
	middleware := &forwardMiddleware{channel: channel}
	result, err := pipeline.NewFactory(httpclient.NewHttpClientWithClient(f.client)).
		Pipeline(
			&parsedInbound{Inbound: f.protocol.inbound, request: request},
			outbound,
			pipeline.WithMiddlewares(stream.EnsureUsage(), middleware),
			pipeline.WithEmptyResponseDetection(),
		).
		Process(ctx, raw)
	if err != nil {
		var usage *llm.Usage
		if middleware.response != nil {
			usage = middleware.response.Usage
		}
		return upstreamResult{responseBody: middleware.responseBody, usage: usage, err: err}
	}
	if result.Stream {
		for result.EventStream.Next() {
			event := result.EventStream.Current()
			if event == nil || len(event.Data) == 0 {
				continue
			}
			terminal, terminalErr := f.protocol.streamTerminal(event)
			if terminalErr != nil {
				result.EventStream.Close()
				return upstreamResult{responseBody: slices.Clone(event.Data), err: terminalErr}
			}
			return upstreamResult{response: &convertedStreamResponse{protocol: f.protocol, stream: result.EventStream, first: event, terminal: terminal}}
		}
		err := result.EventStream.Err()
		result.EventStream.Close()
		if err == nil {
			err = errors.New("stream ended before first event")
		}
		return upstreamResult{err: err}
	}
	if result.Response == nil {
		return upstreamResult{err: errors.New("upstream response is empty")}
	}
	if err := validateUnifiedResponse(f.protocol.format, middleware.response); err != nil {
		var usage *llm.Usage
		if middleware.response != nil {
			usage = middleware.response.Usage
		}
		return upstreamResult{responseBody: slices.Clone(result.Response.Body), usage: usage, err: err}
	}
	usage := middleware.response.Usage
	return upstreamResult{response: &bufferedResponse{status: result.Response.StatusCode, headers: result.Response.Headers.Clone(), body: slices.Clone(result.Response.Body)}, usage: usage}
}

// Commit 写出已验证首事件，并继续发送 AxonHub 转换后的客户端协议事件。
func (r *convertedStreamResponse) Commit(ctx context.Context, c *gin.Context) commitResult {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	result := commitResult{}
	events := make([]*httpclient.StreamEvent, 0, 16)
	event := r.first
	terminal := r.terminal

	for {
		events = append(events, event)
		var encoded bytes.Buffer
		if err := sse.Encode(&encoded, sse.Event{Event: event.Type, Id: event.LastEventID, Data: event.Data}); err != nil {
			result.err = err
			break
		}
		if _, err := c.Writer.Write(encoded.Bytes()); err != nil {
			result.err = fmt.Errorf("%w: %v", errClientWrite, err)
			break
		}
		if result.firstWriteAt.IsZero() {
			result.firstWriteAt = time.Now()
		}
		c.Writer.Flush()
		if terminal {
			break
		}

		found := false
		for r.stream.Next() {
			event = r.stream.Current()
			if event != nil && len(event.Data) > 0 {
				found = true
				break
			}
		}
		if !found {
			if ctx.Err() != nil {
				result.err = ctx.Err()
			} else if result.err = r.stream.Err(); result.err == nil {
				result.err = responses.ErrStreamIncomplete
			}
			break
		}
		terminal, result.err = r.protocol.streamTerminal(event)
		if result.err != nil {
			terminal = true
		}
	}

	if body, meta, err := r.protocol.inbound.AggregateStreamChunks(context.WithoutCancel(ctx), events); err == nil {
		result.responseBody = body
		result.usage = meta.Usage
	}
	return result
}

// Close 关闭 AxonHub 转换后的客户端事件流。
func (r *convertedStreamResponse) Close() error {
	return r.stream.Close()
}

// Name 返回稳定的 middleware 名称。
func (m *forwardMiddleware) Name() string {
	return "switchyard_relay"
}

// OnOutboundRawRequest 应用渠道参数覆盖和自定义 Header。
func (m *forwardMiddleware) OnOutboundRawRequest(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	if err := applyChannelOptions(m.channel, request); err != nil {
		return nil, err
	}
	return request, nil
}

// OnOutboundRawResponse 保存转换前的上游成功响应内容。
func (m *forwardMiddleware) OnOutboundRawResponse(_ context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.responseBody = slices.Clone(response.Body)
	return response, nil
}

// OnOutboundRawError 保存上游错误响应内容。
func (m *forwardMiddleware) OnOutboundRawError(_ context.Context, err error) {
	var upstream *httpclient.Error
	if errors.As(err, &upstream) {
		m.responseBody = slices.Clone(upstream.Body)
	}
}

// OnOutboundLlmResponse 保存出站协议解析后的统一响应。
func (m *forwardMiddleware) OnOutboundLlmResponse(_ context.Context, response *llm.Response) (*llm.Response, error) {
	m.response = response
	return response, nil
}
