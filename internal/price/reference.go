package price

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// These developers also publish entries without canonical_model_id.
var developerCatalogs = []string{
	"ai21", "alibaba", "anthropic", "cohere", "deepseek", "google", "inception",
	"meituan", "minimax", "mistral", "moonshotai", "openai", "perplexity",
	"stepfun", "xiaomi", "xai", "zhipuai",
}

type referenceCandidate struct {
	model.ModelReference
	canonical string
	native    bool
}

func collectModelReferences(feed map[string]modelsDevProvider) []model.ModelReference {
	byName := make(map[string][]referenceCandidate)
	nativePrices := make(map[string]referenceCandidate)
	conflictingPrices := make(map[string]bool)
	for provider, catalog := range feed {
		for _, entry := range catalog.Models {
			name := strings.ToLower(strings.TrimSpace(entry.ID))
			if name == "" {
				continue
			}
			canonical := strings.ToLower(strings.TrimSpace(entry.Canonical))
			native := canonical == provider+"/"+name
			if canonical == "" && slices.Contains(developerCatalogs, provider) && !strings.Contains(name, "/") {
				canonical = provider + "/" + name
				native = true
			}
			candidate := referenceCandidate{
				ModelReference: model.ModelReference{
					Name:          name,
					ContextLength: max(0, entry.Limit.Context),
					MaxOutput:     max(0, entry.Limit.Output),
				},
				canonical: canonical,
				native:    native,
			}
			if native {
				if cost := officialCost(entry.Cost); cost != nil {
					candidate.PriceProvider = provider
					candidate.OfficialPrice = cost
				}
				// Treat conflicting native prices as unknown too.
				if previous, exists := nativePrices[canonical]; exists &&
					string(previous.OfficialPrice) != string(candidate.OfficialPrice) {
					conflictingPrices[canonical] = true
				}
				nativePrices[canonical] = candidate
				byName[canonical] = append(byName[canonical], candidate)
			}
			byName[name] = append(byName[name], candidate)
		}
	}

	references := make([]model.ModelReference, 0, len(byName))
	for name, candidates := range byName {
		// Prefer a developer's entry for its own ID. For other exact IDs,
		// publish each limit only when the available observations agree.
		native := slices.ContainsFunc(candidates, func(c referenceCandidate) bool { return c.native })
		contexts, outputs := make(map[int]struct{}), make(map[int]struct{})
		identities := make(map[string]struct{})
		for _, candidate := range candidates {
			if native && !candidate.native {
				continue
			}
			if candidate.ContextLength > 0 {
				contexts[candidate.ContextLength] = struct{}{}
			}
			if candidate.MaxOutput > 0 {
				outputs[candidate.MaxOutput] = struct{}{}
			}
			identities[candidate.canonical] = struct{}{}
		}
		reference := model.ModelReference{Name: name, ContextLength: uniqueValue(contexts), MaxOutput: uniqueValue(outputs)}
		if identity := uniqueValue(identities); identity != "" && !conflictingPrices[identity] {
			price := nativePrices[identity]
			reference.OfficialPrice = price.OfficialPrice
			reference.PriceProvider = price.PriceProvider
		}
		if reference.ContextLength > 0 || reference.MaxOutput > 0 || reference.OfficialPrice != nil {
			references = append(references, reference)
		}
	}
	return references
}

func uniqueValue[T comparable](values map[T]struct{}) T {
	if len(values) == 1 {
		for value := range values {
			return value
		}
	}
	var zero T
	return zero
}

func officialCost(raw json.RawMessage) json.RawMessage {
	var rates struct {
		Input  *float64 `json:"input"`
		Output *float64 `json:"output"`
	}
	var cost model.LLMPrice
	if json.Unmarshal(raw, &rates) != nil || (rates.Input == nil && rates.Output == nil) ||
		json.Unmarshal(raw, &cost) != nil || cost.Validate() != nil {
		return nil
	}
	return raw
}
