package price

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/client"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

const llmPriceUrl = "https://models.dev/api.json"

var Provider = []string{
	"openai",     // GPT 系列
	"anthropic",  // Claude 系列
	"google",     // Gemini 系列
	"deepseek",   // DeepSeek 系列
	"xai",        // Grok 系列
	"alibaba",    // Qwen 系列
	"zhipuai",    // GLM 系列
	"minimax",    // MiniMax 系列
	"moonshotai", // Kimi/Moonshot
	"v0",         // v0 系列
}

var lastUpdateTime time.Time

func UpdateLLMPriceTask() {
	intervalHours, err := op.SettingGetInt(model.SettingKeyModelInfoUpdateInterval)
	if err != nil {
		log.Errorf("failed to get model info update interval: %v", err)
		return
	}
	interval := time.Duration(intervalHours) * time.Hour
	if time.Since(lastUpdateTime) < interval {
		return
	}
	if err := UpdateLLMPrice(context.Background()); err != nil {
		log.Warnf("failed to update price info: %v", err)
		return
	}
	lastUpdateTime = time.Now()
	log.Infof("LLM price updated successfully")
}

func UpdateLLMPrice(ctx context.Context) error {
	client, err := client.NewHTTPClient(false)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
	}
	var rawPrice map[string]struct {
		Models map[string]struct {
			ID   string         `json:"id"`
			Cost model.LLMPrice `json:"cost"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawPrice); err != nil {
		return fmt.Errorf("failed to parse LLM info: %w", err)
	}
	llmPriceLock.Lock()
	for _, provider := range Provider {
		for _, model := range rawPrice[provider].Models {
			model.ID = strings.ToLower(model.ID)
			llmPrice[model.ID] = model.Cost
		}
	}
	llmPriceLock.Unlock()
	return nil
}

func GetLLMPrice(modelName string) (model.LLMPrice, error) {
	price, err := op.LLMGet(modelName)
	if err == nil {
		return price, nil
	}
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()
	price, ok := llmPrice[modelName]
	if !ok {
		return model.LLMPrice{}, fmt.Errorf("model not found")
	}
	return price, nil
}
