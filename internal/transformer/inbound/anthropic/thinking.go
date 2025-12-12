package anthropic

// thinkingBudgetToReasoningEffort converts thinking budget tokens to reasoning effort string.
func thinkingBudgetToReasoningEffort(budgetTokens int64) string {
	// Map budget tokens to reasoning effort based on the same logic used in outbound
	if budgetTokens <= 5000 {
		return "low"
	} else if budgetTokens <= 15000 {
		return "medium"
	} else {
		return "high"
	}
}

// getDefaultReasoningEffortMapping returns the default mapping from ReasoningEffort to thinking budget tokens.
var defaultReasoningEffortMapping = map[string]int64{
	"low":    5000,
	"medium": 15000,
	"high":   30000,
}
