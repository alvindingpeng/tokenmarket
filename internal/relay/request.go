package relay

import (
	"context"
	"slices"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// relayRequest 保存可重复使用的客户端原始请求及路由元数据。
type relayRequest struct {
	raw    *httpclient.Request // 可重复构造尝试的原始请求。
	model  string              // 客户端请求的模型组名称。
	stream bool                // 客户端是否请求流式响应。
}

// cloneRequest 复制请求结构，隔离每次上游尝试的可变元数据并共享只读请求体。
func cloneRequest(request *httpclient.Request, ctx context.Context) *httpclient.Request {
	copyRequest := *request
	copyRequest.Headers = request.Headers.Clone()
	copyRequest.Query = make(map[string][]string, len(request.Query))
	for key, values := range request.Query {
		copyRequest.Query[key] = slices.Clone(values)
	}
	if request.Auth != nil {
		auth := *request.Auth
		copyRequest.Auth = &auth
	}
	if request.RawRequest != nil {
		copyRequest.RawRequest = request.RawRequest.Clone(ctx)
		copyRequest.RawRequest.Header = copyRequest.Headers
	}
	return &copyRequest
}

// parsedInbound 将已解析的统一请求交给 pipeline，避免重复解析原始请求。
type parsedInbound struct {
	transformer.Inbound              // Inbound 负责响应和错误的客户端协议转换。
	request             *llm.Request // 当前尝试独占的统一请求。
}

// TransformRequest 返回本次尝试独占的统一请求。
func (in *parsedInbound) TransformRequest(_ context.Context, _ *httpclient.Request) (*llm.Request, error) {
	return in.request, nil
}
