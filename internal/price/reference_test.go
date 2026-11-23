package price

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestModelReferencesUseExactLimitsAndDeveloperPrices(t *testing.T) {
	const fixture = `{
		"openai":{"models":{
			"x":{"id":"gpt-test","canonical_model_id":"openai/gpt-test","limit":{"context":100000,"output":8000},"cost":{"input":0,"output":2.5,"tiers":[{"input":1}]}},
			"y":{"id":"gpt-no-price","limit":{"context":16000},"cost":null}
		}},
		"gateway":{"models":{
			"x":{"id":"gpt-test","canonical_model_id":"openai/gpt-test","limit":{"context":80000},"cost":{"input":9,"output":9}},
			"y":{"id":"gpt-test:small","canonical_model_id":"openai/gpt-test","limit":{"context":4000}},
			"z":{"id":"vendor/ambiguous","limit":{"context":10000,"output":1000},"cost":{"input":4,"output":5}}
		}},
		"gateway-2":{"models":{
			"z":{"id":"vendor/ambiguous","limit":{"context":20000,"output":1000}}
		}}
	}`
	var feed map[string]modelsDevProvider
	if err := json.Unmarshal([]byte(fixture), &feed); err != nil {
		t.Fatal(err)
	}
	references := collectModelReferences(feed)
	for _, row := range references {
		switch row.Name {
		case "gpt-test", "openai/gpt-test", "gpt-test:small":
			wantContext := 100000
			if row.Name == "gpt-test:small" {
				wantContext = 4000
			}
			if row.ContextLength != wantContext || row.PriceProvider != "openai" ||
				!strings.Contains(string(row.OfficialPrice), `"input":0`) ||
				!strings.Contains(string(row.OfficialPrice), `"tiers"`) {
				t.Fatalf("incorrect developer reference for %q: %+v", row.Name, row)
			}
		case "vendor/ambiguous":
			if row.ContextLength != 0 || row.MaxOutput != 1000 || row.OfficialPrice != nil {
				t.Fatalf("ambiguous context or reseller price was published: %+v", row)
			}
		case "gpt-no-price", "openai/gpt-no-price":
			if row.ContextLength != 16000 || row.OfficialPrice != nil {
				t.Fatalf("missing price was treated as free: %+v", row)
			}
		default:
			t.Fatalf("unexpected model reference %q", row.Name)
		}
	}
	if len(references) != 6 {
		t.Fatalf("expected six model references, got %d", len(references))
	}
}

func TestModelsDevSnapshot(t *testing.T) {
	path := os.Getenv("TTMARKET_MODELS_DEV_SNAPSHOT")
	if path == "" {
		t.Skip("no models.dev snapshot provided")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var feed map[string]modelsDevProvider
	if err := json.Unmarshal(body, &feed); err != nil {
		t.Fatal(err)
	}
	references := collectModelReferences(feed)
	var limits, prices int
	for _, row := range references {
		if row.ContextLength > 0 {
			limits++
		}
		if row.OfficialPrice != nil {
			prices++
		}
	}
	t.Logf("models.dev snapshot: %d exact IDs, %d context windows, %d developer price references", len(references), limits, prices)
	if limits == 0 || prices == 0 {
		t.Fatal("snapshot has no usable limits or prices")
	}
}
