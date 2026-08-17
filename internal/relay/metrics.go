package relay

import (
	"context"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/looplj/axonhub/llm"
)

// usageMetrics 将统一 Usage 转换为项目统计和费用。
func usageMetrics(modelName string, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	price, err := op.LLMGet(modelName)
	if err != nil {
		log.Warnf("failed to get price for model %s: %v", modelName, err)
		return metrics
	}

	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// applyAttemptMetric 在一次真实上游调用结束时更新渠道和模型统计。
func applyAttemptMetric(item model.GroupItem, channel *model.Channel, metrics model.StatsMetrics, duration time.Duration, healthy bool) {
	metrics.WaitTime = duration.Milliseconds()
	if healthy {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	if err := op.StatsChannelUpdate(channel.ID, metrics); err != nil {
		log.Warnf("failed to update channel %d stats: %v", channel.ID, err)
	}
	if err := op.StatsModelUpdate(model.StatsModel{ID: item.ID, Name: item.ModelName, ChannelID: channel.ID, StatsMetrics: metrics}); err != nil {
		log.Warnf("failed to update model %s stats: %v", item.ModelName, err)
	}
}

// applyCompletion 在请求完成时更新请求级持久化统计。
func applyCompletion(ctx context.Context, apiKeyID int, state RequestState, metrics model.StatsMetrics) {
	if state == RequestStateSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	if err := op.StatsTotalUpdate(metrics); err != nil {
		log.Warnf("failed to update total stats: %v", err)
	}
	if err := op.StatsHourlyUpdate(metrics); err != nil {
		log.Warnf("failed to update hourly stats: %v", err)
	}
	if err := op.StatsDailyUpdate(context.WithoutCancel(ctx), metrics); err != nil {
		log.Warnf("failed to update daily stats: %v", err)
	}
	if apiKeyID > 0 {
		if err := op.StatsAPIKeyUpdate(apiKeyID, metrics); err != nil {
			log.Warnf("failed to update API key %d stats: %v", apiKeyID, err)
		}
	}
}
