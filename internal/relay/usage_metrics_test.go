package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
)

// TestUsageMetricsInputTokenIsGrossPrompt 首页词元口径修复的核心回归:
// InputToken 必须是毛输入(prompt, 含缓存读/写), 而不是计费用的净"读"桶,
// 否则 总词元 = 输入 + 输出 会漏掉缓存命中请求的词元。
// 费用仍按四桶计价: 净读走 Input 价, 缓存读/写走各自缓存价。
func TestUsageMetricsInputTokenIsGrossPrompt(t *testing.T) {
	price := model.LLMPrice{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.75}
	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		PromptTokensDetails: &llm.PromptTokensDetails{
			CachedTokens:      300,
			WriteCachedTokens: 200,
		},
	}

	metrics := usageMetrics(price, usage)
	if metrics.InputToken != 1000 {
		t.Fatalf("input token = %d, want gross prompt 1000", metrics.InputToken)
	}
	if metrics.OutputToken != 500 {
		t.Fatalf("output token = %d, want 500", metrics.OutputToken)
	}
	// 总词元 = 输入 + 输出 = 1500(修复前只有净读700 + 输出500 = 1200)。
	if total := metrics.InputToken + metrics.OutputToken; total != 1500 {
		t.Fatalf("total tokens = %d, want 1500", total)
	}
	// 净读 = 1000 - 300 - 200 = 500; 费用 = (500*1 + 300*0.5 + 200*0.75 + 500*2)/1e6。
	wantInputCost := float64(500*1+300*0.5+200*0.75) / 1_000_000
	if diff := metrics.InputCost - wantInputCost; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("input cost = %v, want %v", metrics.InputCost, wantInputCost)
	}
	wantOutputCost := float64(500) * 2 / 1_000_000
	if diff := metrics.OutputCost - wantOutputCost; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("output cost = %v, want %v", metrics.OutputCost, wantOutputCost)
	}
}

// TestUsageMetricsCachelessUsageUnchanged 无缓存明细时毛输入 = 净读, 行为与修复前一致。
func TestUsageMetricsCachelessUsageUnchanged(t *testing.T) {
	price := model.LLMPrice{Input: 3, Output: 4}
	usage := &llm.Usage{PromptTokens: 800, CompletionTokens: 200}

	metrics := usageMetrics(price, usage)
	if metrics.InputToken != 800 || metrics.OutputToken != 200 {
		t.Fatalf("tokens = %d/%d, want 800/200", metrics.InputToken, metrics.OutputToken)
	}
	wantInputCost := float64(800) * 3 / 1_000_000
	if diff := metrics.InputCost - wantInputCost; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("input cost = %v, want %v", metrics.InputCost, wantInputCost)
	}
}

// TestUsageMetricsNilAndZeroUsage nil 用量与零用量都返回零值统计。
func TestUsageMetricsNilAndZeroUsage(t *testing.T) {
	if got := usageMetrics(model.LLMPrice{Input: 1}, nil); got != (model.StatsMetrics{}) {
		t.Fatalf("nil usage = %+v, want zero metrics", got)
	}
	zero := &llm.Usage{}
	if got := usageMetrics(model.LLMPrice{Input: 1}, zero); got != (model.StatsMetrics{}) {
		t.Fatalf("zero usage = %+v, want zero metrics", got)
	}
}
