package relay

import (
	"context"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	transformerModel "github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

type RelayStats struct {
	ChannelID int
	Model     string
	IsSuccess bool
	StartTime time.Time
	Stats     model.StatsMetrics
}

func NewRelayStats(channelId int, model string) *RelayStats {
	return &RelayStats{
		ChannelID: channelId,
		Model:     model,
		StartTime: time.Now(),
	}
}

func (r *RelayStats) UpdateUsage(usage transformerModel.Usage) {
	modelPrice := price.GetLLMPrice(r.Model)
	r.Stats.InputToken = usage.PromptTokens
	r.Stats.OutputToken = usage.CompletionTokens
	if usage.PromptTokensDetails != nil {
		r.Stats.InputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead + float64(usage.PromptTokens-usage.PromptTokensDetails.CachedTokens)*modelPrice.Input) * 1e-6
	} else {
		r.Stats.InputCost = float64(usage.PromptTokens) * modelPrice.Input * 1e-6
	}
	r.Stats.OutputCost = float64(usage.CompletionTokens) * modelPrice.Output * 1e-6
}

func (r *RelayStats) Save(success bool) {
	if success {
		r.Stats.RequestSuccess = 1
	} else {
		r.Stats.RequestFailed = 1
	}
	r.Stats.WaitTime = time.Since(r.StartTime).Milliseconds()
	op.StatsChannelUpdate(model.StatsChannel{
		ChannelID:    r.ChannelID,
		StatsMetrics: r.Stats,
	})
	op.StatsTotalUpdate(r.Stats)
	op.StatsHourlyUpdate(r.Stats)
	op.StatsDailyUpdate(context.Background(), r.Stats)
	log.Infof("channel: %d, model: %s, success: %t, wait time: %d, input token: %d, output token: %d, input cost: %f, output cost: %f total cost: %f", r.ChannelID, r.Model, success, r.Stats.WaitTime, r.Stats.InputToken, r.Stats.OutputToken, r.Stats.InputCost, r.Stats.OutputCost, r.Stats.InputCost+r.Stats.OutputCost)
}
