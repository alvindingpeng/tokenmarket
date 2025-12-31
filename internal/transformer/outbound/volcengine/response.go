package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound/openai"
)

var supportedReasoningEffortModel = map[string]bool{
	"doubao-seed-1-6-lite-251015":true,
	"doubao-seed-1-6-251015":true,
}

type ResponseOutbound struct {
	inner openai.ResponseOutbound
}

func (o *ResponseOutbound) TransformRequest(ctx context.Context, request *model.InternalLLMRequest, baseUrl, key string) (*http.Request, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}

	// Convert to Responses API request format
	openaiReq := openai.ConvertToResponsesRequest(request)
	if _, ok := supportedReasoningEffortModel[request.Model]; !ok {
		openaiReq.Reasoning = nil
	}
	responsesReq := ResponsesRequest{
		ResponsesRequest: openaiReq,
	}
	switch request.ReasoningEffort {
	case "minimal":
		responsesReq.Thinking.Type = ThinkingTypeDisabled
	case "low", "medium", "high":
		responsesReq.Thinking.Type = ThinkingTypeEnabled
	default:
	}

	body, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal responses api request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	// Parse and set URL
	parsedUrl, err := url.Parse(strings.TrimSuffix(baseUrl, "/"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse base url: %w", err)
	}
	parsedUrl.Path = parsedUrl.Path + "/responses"
	req.URL = parsedUrl
	req.Method = http.MethodPost

	return req, nil

}
func (o *ResponseOutbound) TransformResponse(ctx context.Context, response *http.Response) (*model.InternalLLMResponse, error) {
	return o.inner.TransformResponse(ctx, response)
}

func (o *ResponseOutbound) TransformStream(ctx context.Context, eventData []byte) (*model.InternalLLMResponse, error) {
	return o.inner.TransformStream(ctx, eventData)
}



type ResponsesRequest struct {
	*openai.ResponsesRequest
	Thinking Thinking `json:"thinking,omitzero"`
}

type ThinkingType string

const (
	ThinkingTypeAuto ThinkingType = "auto"
	ThinkingTypeDisabled ThinkingType = "disabled"
	ThinkingTypeEnabled ThinkingType = "enabled"
)

type Thinking struct {
	Type ThinkingType `json:"type"`
}
