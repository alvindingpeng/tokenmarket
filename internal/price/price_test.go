package price

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestPrice(t *testing.T) {
	var rawData map[string]struct {
		Models map[string]struct {
			ID   string         `json:"id"`
			Cost model.LLMPrice `json:"cost"`
		} `json:"models"`
	}
	llmPrice := make(map[string]model.LLMPrice)
	data, err := os.ReadFile("/workplace/switchyard/data/model.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rawData); err != nil {
		t.Fatal(err)
	}
	for _, provider := range Provider {
		for _, model := range rawData[provider].Models {
			model.ID = strings.ToLower(model.ID)
			llmPrice[model.ID] = model.Cost
		}
	}
	data, err = json.Marshal(llmPrice)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile("/workplace/switchyard/data/price.json", data, 0644)
}
