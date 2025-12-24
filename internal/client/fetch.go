package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound"
)

// Gemini Models List Refer：https://ai.google.dev/api/models?hl=zh_cn

func FetchLLMName(ctx context.Context, request model.Channel) ([]string, error) {
	client, err := NewHTTPClient(request.Proxy)
	if err != nil {
		return nil, err
	}

	var allModels []string
	pageToken := ""

	// 循环获取所有分页数据
	for {
		base_url := fmt.Sprintf("%s/models", request.BaseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base_url, nil)
		if err != nil {
			return nil, err
		}

		// 如果有 pageToken，添加到查询参数
		if pageToken != "" {
			q := req.URL.Query()
			q.Add("pageToken", pageToken)
			req.URL.RawQuery = q.Encode()
		}

		switch request.Type {
		case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse:
			req.Header.Set("Authorization", "Bearer "+request.Key)
		case outbound.OutboundTypeAnthropic:
			req.Header.Set("Authorization", "Bearer "+request.Key)
		case outbound.OutboundTypeGemini:
			//req.Header.Set("Authorization", "Bearer "+request.Key)
			req.Header.Set("X-Goog-Api-Key", request.Key)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			// 兼容Gemini格式
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			// Gemini格式下的分页字段
			NextPageToken string `json:"nextPageToken"`
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(body, &result); err != nil {
			return nil, err
		}

		// 收集当前页的模型
		if len(result.Data) > 0 {
			// OpenAI 格式
			for _, model := range result.Data {
				allModels = append(allModels, model.ID)
			}
		} else if len(result.Models) > 0 {
			// Gemini 格式
			for _, model := range result.Models {
				modelName := model.Name // 因为发送请求的时候自动添加了models/前缀，这里去掉
				if after, ok := strings.CutPrefix(modelName, "models/"); ok {
					modelName = after
				}
				allModels = append(allModels, modelName)
			}
		}

		// 如果没有下一页，退出循环
		if result.NextPageToken == "" {
			break
		}

		pageToken = result.NextPageToken
	}

	return allModels, nil
}
