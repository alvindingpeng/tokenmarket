package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	transformerModel "github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

// RelayMetrics 统一管理请求的日志记录和统计信息
type RelayMetrics struct {
	// 基础信息
	ChannelID      int
	APIKeyID       int
	ChannelName    string // 渠道名称
	RequestModel   string // 请求的模型名称
	ActualModel    string // 实际使用的模型名称
	StartTime      time.Time
	FirstTokenTime time.Time // 首个 Token 时间（流式场景）

	// 请求和响应内容
	InternalRequest  *transformerModel.InternalLLMRequest
	InternalResponse *transformerModel.InternalLLMResponse

	// 统计指标
	Stats model.StatsMetrics
}

// NewRelayMetrics 创建新的 RelayMetrics
func NewRelayMetrics(requestModel string) *RelayMetrics {
	return &RelayMetrics{
		RequestModel: requestModel,
		StartTime:    time.Now(),
	}
}

func (m *RelayMetrics) SetAPIKeyID(apiKeyID int) {
	m.APIKeyID = apiKeyID
}

// SetChannel 设置通道信息
func (m *RelayMetrics) SetChannel(channelID int, channelName string, actualModel string) {
	m.ChannelID = channelID
	m.ChannelName = channelName
	m.ActualModel = actualModel
}

// SetFirstTokenTime 设置首个 Token 时间
func (m *RelayMetrics) SetFirstTokenTime(t time.Time) {
	m.FirstTokenTime = t
}

// SetInternalRequest 设置内部请求
func (m *RelayMetrics) SetInternalRequest(req *transformerModel.InternalLLMRequest) {
	m.InternalRequest = req
}

// SetInternalResponse 设置内部响应并计算费用
func (m *RelayMetrics) SetInternalResponse(resp *transformerModel.InternalLLMResponse) {
	m.InternalResponse = resp

	// 从响应中提取 Usage 并计算费用
	if resp == nil || resp.Usage == nil {
		return
	}

	usage := resp.Usage
	m.Stats.InputToken = usage.PromptTokens
	m.Stats.OutputToken = usage.CompletionTokens

	// 计算费用
	modelPrice := price.GetLLMPrice(m.ActualModel)
	if modelPrice == nil {
		return
	}
	if usage.PromptTokensDetails != nil {
		m.Stats.InputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead +
			float64(usage.PromptTokens-usage.PromptTokensDetails.CachedTokens)*modelPrice.Input) * 1e-6
	} else {
		m.Stats.InputCost = float64(usage.PromptTokens) * modelPrice.Input * 1e-6
	}
	m.Stats.OutputCost = float64(usage.CompletionTokens) * modelPrice.Output * 1e-6
}

// Save 保存日志和统计信息
// success: 请求是否成功
// err: 失败时的错误信息，成功时为 nil
func (m *RelayMetrics) Save(ctx context.Context, success bool, err error) {
	duration := time.Since(m.StartTime)

	// 保存统计信息
	m.saveStats(success, duration)

	// 保存日志
	m.saveLog(ctx, err, duration)
}

// saveStats 保存统计信息
func (m *RelayMetrics) saveStats(success bool, duration time.Duration) {
	if success {
		m.Stats.RequestSuccess = 1
	} else {
		m.Stats.RequestFailed = 1
	}
	m.Stats.WaitTime = duration.Milliseconds()

	op.StatsChannelUpdate(m.ChannelID, m.Stats)
	op.StatsTotalUpdate(m.Stats)
	op.StatsHourlyUpdate(m.Stats)
	op.StatsDailyUpdate(context.Background(), m.Stats)
	op.StatsAPIKeyUpdate(m.APIKeyID, m.Stats)

	log.Infof("channel: %d, model: %s, success: %t, wait time: %d, input token: %d, output token: %d, input cost: %f, output cost: %f total cost: %f",
		m.ChannelID, m.ActualModel, success, m.Stats.WaitTime,
		m.Stats.InputToken, m.Stats.OutputToken,
		m.Stats.InputCost, m.Stats.OutputCost, m.Stats.InputCost+m.Stats.OutputCost)
}

// saveLog 保存日志
func (m *RelayMetrics) saveLog(ctx context.Context, err error, duration time.Duration) {
	relayLog := model.RelayLog{
		Time:             m.StartTime.Unix(),
		RequestModelName: m.RequestModel,
		ChannelName:      m.ChannelName,
		ChannelId:        m.ChannelID,
		ActualModelName:  m.ActualModel,
		UseTime:          int(duration.Milliseconds()),
	}

	// 设置首字时间（流式场景）
	if !m.FirstTokenTime.IsZero() {
		relayLog.Ftut = int(m.FirstTokenTime.Sub(m.StartTime).Milliseconds())
	}

	// 设置 Usage 信息
	if m.InternalResponse != nil && m.InternalResponse.Usage != nil {
		relayLog.InputTokens = int(m.InternalResponse.Usage.PromptTokens)
		relayLog.OutputTokens = int(m.InternalResponse.Usage.CompletionTokens)
		relayLog.Cost = m.Stats.InputCost + m.Stats.OutputCost
	}

	// 设置请求内容
	if m.InternalRequest != nil {
		if reqJSON, jsonErr := json.Marshal(m.InternalRequest); jsonErr == nil {
			relayLog.RequestContent = string(reqJSON)
		}
	}

	// 设置响应内容
	if m.InternalResponse != nil {
		if respJSON, jsonErr := json.Marshal(m.InternalResponse); jsonErr == nil {
			relayLog.ResponseContent = string(respJSON)
		}
	}

	// 设置错误信息
	if err != nil {
		relayLog.Error = err.Error()
	}

	if logErr := op.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save relay log: %v", logErr)
	}
}
