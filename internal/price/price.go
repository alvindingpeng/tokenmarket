package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
	"github.com/charmbracelet/log"
)

const llmPriceUrl = "https://models.dev/api.json"
const maxModelsDevBodyBytes = 32 << 20

// developerFamilies 定义研发商及其自研模型系列前缀。
var developerFamilies = map[string][]string{
	"openai":     {"gpt", "o"},
	"anthropic":  {"claude"},
	"google":     {"gemini", "gemma", "lyria", "veo"},
	"deepseek":   {"deepseek"},
	"xai":        {"grok"},
	"alibaba":    {"qwen", "qvq"},
	"zhipuai":    {"glm"},
	"minimax":    {"minimax"},
	"moonshotai": {"kimi"},
	"v0":         {"v0"},
	"xiaomi":     {"mimo"},
}

var lastUpdateTime time.Time // 最近一次成功更新时间。

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID         string `json:"id"`
	Canonical  string `json:"canonical_model_id"`
	Family     string `json:"family"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost json.RawMessage `json:"cost"`
}

var catalogSync = make(chan struct{}, 1)

// SyncModelCatalog shares the fetch and persistence path between manual and scheduled refreshes.
func SyncModelCatalog(ctx context.Context) (op.ModelCatalogSyncResult, error) {
	select {
	case catalogSync <- struct{}{}:
		defer func() { <-catalogSync }()
	case <-ctx.Done():
		return op.ModelCatalogSyncResult{}, ctx.Err()
	}
	capabilities, references, calibration, err := fetchModelsDev(ctx)
	if err != nil {
		return op.ModelCatalogSyncResult{}, err
	}
	result, err := op.ReplaceModelCatalog(ctx, capabilities, references)
	if err != nil {
		return op.ModelCatalogSyncResult{}, err
	}
	publishCalibration(calibration)
	return result, nil
}

// fetchModelsDev parses a single feed for reference metadata and the existing price calibration.
func fetchModelsDev(ctx context.Context) ([]model.ModelCapability, []model.ModelReference, map[string]model.LLMPrice, error) {
	var body []byte
	httpClient, err := rhttp.Direct()
	if err == nil {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if requestErr != nil {
			return nil, nil, nil, requestErr
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, requestErr := httpClient.Do(req)
		if requestErr != nil {
			err = requestErr
		} else {
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
			} else {
				body, err = io.ReadAll(io.LimitReader(resp.Body, maxModelsDevBodyBytes+1))
				if err != nil {
					err = fmt.Errorf("failed to read response body: %w", err)
				}
			}
			resp.Body.Close()
		}
	}
	if err != nil {
		log.Warnf("direct request failed, trying with proxy: %v", err)
		httpClient, err = rhttp.Proxy()
		if err != nil {
			return nil, nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, nil, nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, nil, nil, fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxModelsDevBodyBytes+1))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}
	if len(body) > maxModelsDevBodyBytes {
		return nil, nil, nil, fmt.Errorf("models.dev response exceeds %d bytes", maxModelsDevBodyBytes)
	}
	var rawPrice map[string]modelsDevProvider
	if err := json.Unmarshal(body, &rawPrice); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse LLM info: %w", err)
	}
	capabilities := collectModelCapabilities(rawPrice)
	references := collectModelReferences(rawPrice)
	if len(capabilities) == 0 || len(references) == 0 {
		return nil, nil, nil, fmt.Errorf("models.dev returned an empty model catalog")
	}

	updatedPrices := make(map[string]model.LLMPrice)
	for provider, familyPrefixes := range developerFamilies {
		for _, priceModel := range rawPrice[provider].Models {
			modelID := strings.ToLower(priceModel.ID)
			modelFamily := strings.ToLower(priceModel.Family)

			// 仅保留包含文本输出的非嵌入模型。
			if modelID == "" || !slices.Contains(priceModel.Modalities.Output, "text") || strings.Contains(modelID, "embed") || strings.Contains(modelFamily, "embed") {
				continue
			}

			// 云平台可能同时托管第三方模型，仅接受该研发商的自研系列。
			isDeveloperModel := false
			for _, familyPrefix := range familyPrefixes {
				if strings.HasPrefix(modelFamily, familyPrefix) {
					isDeveloperModel = true
					break
				}
			}
			if !isDeveloperModel {
				continue
			}

			var cost model.LLMPrice
			if json.Unmarshal(priceModel.Cost, &cost) == nil && cost.Validate() == nil {
				updatedPrices[modelID] = cost
			}
		}
	}
	return capabilities, references, updatedPrices, nil
}

// publishCalibration updates the legacy calibration only after the catalog sync commits.
func publishCalibration(prices map[string]model.LLMPrice) {
	llmPriceLock.Lock()
	defer llmPriceLock.Unlock()
	llmPrice = prices
	lastUpdateTime = time.Now()
}

// collectModelCapabilities preserves the existing media classification when entries disagree.
func collectModelCapabilities(rawPrice map[string]modelsDevProvider) []model.ModelCapability {
	providers := make([]string, 0, len(rawPrice))
	for provider := range rawPrice {
		providers = append(providers, provider)
	}
	slices.Sort(providers)

	byName := make(map[string]model.ModelCapability, len(rawPrice))
	for _, provider := range providers {
		modelIDs := make([]string, 0, len(rawPrice[provider].Models))
		for id := range rawPrice[provider].Models {
			modelIDs = append(modelIDs, id)
		}
		slices.Sort(modelIDs)
		for _, id := range modelIDs {
			priceModel := rawPrice[provider].Models[id]
			name := strings.ToLower(strings.TrimSpace(priceModel.ID))
			if name == "" {
				continue
			}
			candidate := model.ModelCapability{
				Name:      name,
				Input:     strings.Join(priceModel.Modalities.Input, ","),
				Output:    strings.Join(priceModel.Modalities.Output, ","),
				Context:   priceModel.Limit.Context,
				MaxOutput: priceModel.Limit.Output,
				Family:    strings.ToLower(strings.TrimSpace(priceModel.Family)),
			}
			if candidate.Empty() {
				continue
			}
			existing, ok := byName[name]
			if !ok || capabilityRank(candidate) > capabilityRank(existing) {
				byName[name] = candidate
			}
		}
	}

	capabilities := make([]model.ModelCapability, 0, len(byName))
	for _, capability := range byName {
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

// capabilityRank 给能力条目排序打分: 带媒体输出 > 输出模态更全 > 输入模态更全, 用于同名去重时择优。
func capabilityRank(capability model.ModelCapability) int {
	rank := len(capability.Output)*10 + len(capability.Input)
	if capability.OutputsImage() || capability.OutputsVideo() {
		rank += 1000
	}
	return rank
}

// GetLastUpdateTime 返回最近一次价格更新时间。
func GetLastUpdateTime() time.Time {
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()
	return lastUpdateTime
}

// GetLLMPrice 根据已规范化的模型名返回 API 提供的校准价格。
func GetLLMPrice(modelName string) *model.LLMPrice {
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()

	if price, ok := llmPrice[modelName]; ok {
		return &price
	}
	// 将非字母、数字和小数点字符作为边界，使标准模型名可以出现在任意前缀或后缀之间。
	modelNameSegments := strings.FieldsFunc(modelName, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.'
	})

	// 匹配完整且连续的标准名称分段，优先选择分段最多的具体模型。
	matchedModelID := ""
	matchedSegmentCount := 0
	ambiguous := false
	for modelID, price := range llmPrice {
		modelIDSegments := strings.Split(modelID, "-")
		for start := 0; start+len(modelIDSegments) <= len(modelNameSegments); start++ {
			matched := true
			for i, segment := range modelIDSegments {
				if modelNameSegments[start+i] != segment {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if len(modelIDSegments) > matchedSegmentCount {
				matchedModelID = modelID
				matchedSegmentCount = len(modelIDSegments)
				ambiguous = false
			} else if len(modelIDSegments) == matchedSegmentCount && llmPrice[matchedModelID] != price {
				ambiguous = true
			}
			break
		}
	}
	if matchedModelID == "" || ambiguous {
		return nil
	}
	price := llmPrice[matchedModelID]
	return &price
}
