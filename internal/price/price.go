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
	"github.com/alvindingpeng/switchyard/internal/rhttp"
	"github.com/charmbracelet/log"
)

const llmPriceUrl = "https://models.dev/api.json"

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

// UpdateLLMPrice 从 models.dev 更新自研文本输出模型的价格, 并返回同一次抓取得到的模型能力登记。
// 能力登记交给调用方落库: price 只负责解析官方数据, 落库与缓存属于 op 的职责,
// 由调用方串联两者, price 便无需依赖 op(否则 op 侧改价要用价格表就会形成导入环)。
func UpdateLLMPrice(ctx context.Context) ([]model.ModelCapability, error) {
	log.Debugf("update LLM price task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("update LLM price task finished, update time: %s", time.Since(startTime))
	}()
	var body []byte
	httpClient, err := rhttp.Direct()
	if err == nil {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, requestErr := httpClient.Do(req)
		if requestErr != nil {
			err = requestErr
		} else {
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
			} else {
				body, err = io.ReadAll(resp.Body)
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
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
		}
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}
	var rawPrice map[string]struct {
		Models map[string]struct {
			ID         string `json:"id"`     // 模型标识。
			Family     string `json:"family"` // 模型所属系列。
			Modalities struct {
				Input  []string `json:"input"`  // 模型接受的输入类型。
				Output []string `json:"output"` // 模型支持的输出类型。
			} `json:"modalities"`
			Limit struct {
				Context int `json:"context"` // 上下文窗口(token)。
				Output  int `json:"output"`  // 单次最大输出(token)。
			} `json:"limit"`
			Cost model.LLMPrice `json:"cost"` // 模型价格。
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &rawPrice); err != nil {
		return nil, fmt.Errorf("failed to parse LLM info: %w", err)
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

			updatedPrices[modelID] = priceModel.Cost
		}
	}
	llmPriceLock.Lock()
	llmPrice = updatedPrices
	lastUpdateTime = time.Now()
	llmPriceLock.Unlock()

	// 能力登记(输入输出模态与规模上限)与价格同源, 一起返回给调用方: 探测渠道模型时据此
	// 预勾选生图/生视频协议位, 不再靠模型名关键词猜。
	return collectModelCapabilities(rawPrice), nil
}

// collectModelCapabilities 把 models.dev 的原始响应摊平成能力登记。
// 同一个模型可能在多个厂商下重复登记且模态不同(聚合平台会补自己的元数据), 故按名称去重:
// 优先保留带媒体输出的那条(媒体能力是这里唯一稀缺的信号), 同级再按输出模态更全定序,
// 保证同一份上游数据每次跑出的结果完全一致 —— 能力登记会落库并被界面展示, 不能随 map 遍历顺序变化。
func collectModelCapabilities(rawPrice map[string]struct {
	Models map[string]struct {
		ID         string `json:"id"`
		Family     string `json:"family"`
		Modalities struct {
			Input  []string `json:"input"`
			Output []string `json:"output"`
		} `json:"modalities"`
		Limit struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
		Cost model.LLMPrice `json:"cost"`
	} `json:"models"`
}) []model.ModelCapability {
	providers := make([]string, 0, len(rawPrice))
	for provider := range rawPrice {
		providers = append(providers, provider)
	}
	slices.Sort(providers)

	byName := make(map[string]model.ModelCapability, len(rawPrice))
	for _, provider := range providers {
		for _, priceModel := range rawPrice[provider].Models {
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
