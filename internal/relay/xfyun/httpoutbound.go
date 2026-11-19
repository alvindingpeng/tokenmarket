package xfyun

import (
	"context"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// HTTPSigningOutbound 包装标准 HTTP 出站转换器: 请求成形后按讯飞 V2 签名改写 Authorization
// (MaaS 平台只认 APIKey/APISecret 签名, 不认 Bearer)。签名之外的行为全部委托内层转换器。
type HTTPSigningOutbound struct {
	inner transformer.Outbound
	creds Credentials
	now   func() time.Time // 测试可注入; 为 nil 时用 time.Now。
}

// NewHTTPSigningOutbound 构造带讯飞 V2 签名的出站包装器。
func NewHTTPSigningOutbound(inner transformer.Outbound, creds Credentials) *HTTPSigningOutbound {
	return &HTTPSigningOutbound{inner: inner, creds: creds}
}

var _ transformer.Outbound = (*HTTPSigningOutbound)(nil)

func (o *HTTPSigningOutbound) APIFormat() llm.APIFormat {
	return o.inner.APIFormat()
}

func (o *HTTPSigningOutbound) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	req, err := o.inner.TransformRequest(ctx, request)
	if err != nil {
		return req, err
	}
	signTime := time.Now()
	if o.now != nil {
		signTime = o.now()
	}
	if err := SignHTTPV2Request(req, o.creds, signTime); err != nil {
		return nil, err
	}
	return req, nil
}

func (o *HTTPSigningOutbound) TransformResponse(ctx context.Context, response *httpclient.Response) (*llm.Response, error) {
	return o.inner.TransformResponse(ctx, response)
}

func (o *HTTPSigningOutbound) TransformStream(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return o.inner.TransformStream(ctx, req, stream)
}

func (o *HTTPSigningOutbound) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return o.inner.TransformError(ctx, err)
}

func (o *HTTPSigningOutbound) AggregateStreamChunks(ctx context.Context, req *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return o.inner.AggregateStreamChunks(ctx, req, chunks)
}
