package openai

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/bytedance/sonic"
)

type ChatInbound struct{}

func (i *ChatInbound) TransformRequest(ctx context.Context, body []byte) (*model.InternalLLMRequest, error) {
	var request model.InternalLLMRequest
	if err := sonic.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	return &request, nil
}

func (i *ChatInbound) TransformResponse(ctx context.Context, response *model.InternalLLMResponse) ([]byte, error) {
	body, err := sonic.Marshal(response)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (i *ChatInbound) TransformStream(ctx context.Context, stream *model.InternalLLMResponse) ([]byte, error) {
	if stream.Object == "[DONE]" {
		return []byte("data: [DONE]\n\n"), nil
	}
	body, err := sonic.Marshal(stream)
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(body) + "\n\n"), nil
}
