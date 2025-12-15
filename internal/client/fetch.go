package client

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound"
	"github.com/bytedance/sonic"
)

func FetchLLMName(ctx context.Context, request model.Channel) ([]string, error) {
	client, err := NewHTTPClient(request.Proxy)
	if err != nil {
		return nil, err
	}
	base_url := fmt.Sprintf("%s/models", request.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base_url, nil)
	if err != nil {
		return nil, err
	}
	switch request.Type {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOneAPI:
		req.Header.Set("Authorization", "Bearer "+request.Key)
	case outbound.OutboundTypeAnthropic:
		req.Header.Set("Authorization", "Bearer "+request.Key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := sonic.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range result.Data {
		models = append(models, model.ID)
	}
	return models, nil
}
