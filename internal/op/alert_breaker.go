package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 熔断类告警。
//
// 凭据与渠道模型被熔断过去只写进库里的 disabled_until 字段, 管理员只能自己发现"某家渠道怎么不接量了"。
// 熔断本身就是一次自动降级决策, 必须让管理员知道: 哪家渠道、哪个凭据/模型、什么错误、停到几点。

// raiseKeyBreakerAlert 在凭据被熔断时告警。
//
// reason 里带凭据名与错误类型而不带时间: 去重按 (渠道, kind, reason) 精确匹配,
// 掺入熔断时刻会让每一轮失败都变成一条新告警, 十秒内推爆管理员的手机。
func raiseKeyBreakerAlert(key model.ChannelKey, errorType string, until time.Time, threshold int, trips int) {
	subject := channelName(key.ChannelID)
	minutes := int(time.Until(until).Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	RaiseAlertFacts(context.Background(), AlertFacts{
		ChannelID: key.ChannelID,
		Subject:   subject,
		Kind:      "key_disabled",
		Reason:    fmt.Sprintf("channel key %s disabled by circuit breaker: %s", key.Name, errorType),
		Fields: []alert.Field{
			{Label: "渠道", Value: subject},
			{Label: "凭据", Value: fmt.Sprintf("%s (#%d)", key.Name, key.ID)},
			{Label: "错误类型", Value: errorClassLabel(errorType)},
			{Label: "熔断时长", Value: fmt.Sprintf("%d 分钟", minutes)},
			{Label: "自动恢复", Value: until.Format("2006-01-02 15:04:05")},
			{Label: "触发计数", Value: formatBreakerTrigger(threshold)},
			{Label: "累计熔断", Value: fmt.Sprintf("第 %d 次(成功后清零)", trips)},
		},
	})
}

// raiseModelBreakerAlert 在渠道模型被熔断时告警。
func raiseModelBreakerAlert(channelModel model.ChannelModel, errorType string, until time.Time, threshold int, trips int) {
	subject := channelName(channelModel.ChannelID)
	minutes := int(time.Until(until).Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	RaiseAlertFacts(context.Background(), AlertFacts{
		ChannelID: channelModel.ChannelID,
		Subject:   subject,
		Kind:      "model_disabled",
		Reason:    fmt.Sprintf("channel model %s disabled by circuit breaker: %s", channelModel.Name, errorType),
		Fields: []alert.Field{
			{Label: "渠道", Value: subject},
			{Label: "渠道模型", Value: fmt.Sprintf("%s (#%d)", channelModel.Name, channelModel.ID)},
			{Label: "错误类型", Value: errorClassLabel(errorType)},
			{Label: "熔断时长", Value: fmt.Sprintf("%d 分钟", minutes)},
			{Label: "自动恢复", Value: until.Format("2006-01-02 15:04:05")},
			{Label: "触发计数", Value: formatBreakerTrigger(threshold)},
		},
	})
}

// formatBreakerTrigger 说明这次熔断是被哪条规则放大的: 计数阈值, 还是鉴权/额度的立即熔断。
func formatBreakerTrigger(threshold int) string {
	if threshold <= 0 {
		return "凭据失效/额度耗尽立即熔断(无计数阈值)"
	}
	return fmt.Sprintf("连续错误达到阈值 %d 次", threshold)
}

// channelName 返回渠道的真实名称; 渠道已被删除或缓存未命中时退化成 ID, 不让告警反过来丢信息。
func channelName(channelID int) string {
	if channelID <= 0 {
		return ""
	}
	if channel, err := ChannelGet(channelID); err == nil && channel.Name != "" {
		return channel.Name
	}
	return fmt.Sprintf("渠道 #%d", channelID)
}
