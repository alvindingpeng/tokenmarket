package op

import (
	"context"
	"fmt"
	"github.com/alvindingpeng/switchyard/internal/alert"
	"net/http"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// channelHealthClient 专用探测 HTTP 客户端, 超时较短避免长阻塞。
var channelHealthClient = &http.Client{Timeout: 15 * time.Second}

// alertFailStreakDefault 连续失败次数阈值默认值: 达到此数才判定宕机, 避免单次抖动误报。
// 实际取值来自设置 alert_fail_streak, 未配置时回退到此默认值。
const alertFailStreakDefault = 5

// ChannelHealthCheckAll 对所有启用渠道逐一探测并记录结果。
func ChannelHealthCheckAll(ctx context.Context) error {
	channels := []model.Channel{}
	if err := db.GetDB().WithContext(ctx).Where("enabled = ?", true).Find(&channels).Error; err != nil {
		return fmt.Errorf("load channels: %w", err)
	}
	for _, channel := range channels {
		checkOne(ctx, channel)
	}
	return nil
}

// checkOne 探测单个渠道并落库健康记录, 随后评估是否触发告警。
func checkOne(ctx context.Context, channel model.Channel) {
	record := model.ChannelHealthRecord{
		ChannelID:   channel.ID,
		ChannelCode: channel.ShareCode,
		CheckedAt:   time.Now(),
	}
	start := time.Now()
	probeURL := channel.BaseURL + channel.OpenAIChatCompletionPath
	if channel.OpenAIChatCompletionPath == "" {
		probeURL = channel.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		record.Error = err.Error()
		saveHealthRecord(record)
		evaluateAlerts(ctx, channel, record)
		return
	}
	resp, err := channelHealthClient.Do(req)
	record.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		record.Error = err.Error()
		saveHealthRecord(record)
		evaluateAlerts(ctx, channel, record)
		return
	}
	defer resp.Body.Close()
	record.StatusCode = resp.StatusCode
	record.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 500
	if !record.Healthy {
		record.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	saveHealthRecord(record)
	evaluateAlerts(ctx, channel, record)
}

// saveHealthRecord 落库探测结果, 只保留最近 1000 条避免无限增长。
func saveHealthRecord(record model.ChannelHealthRecord) {
	if err := db.GetDB().Create(&record).Error; err != nil {
		return
	}
	db.GetDB().Where("channel_id = ?", record.ChannelID).
		Where("id NOT IN (SELECT id FROM channel_health_records WHERE channel_id = ? ORDER BY id DESC LIMIT 1000)", record.ChannelID).
		Delete(&model.ChannelHealthRecord{})
}

// evaluateAlerts 基于本次探测结果与历史记录评估告警(带抖动抑制):
// 连续 alert_fail_streak 次异常才判定 down; 恢复时若最近告警是 down/degraded 则补发 recovered;
// 健康但延迟超过 health_latency_ms 阈值则发 degraded。同类型告警的去重由 RaiseAlert 负责。
//
// 主体一律留空交给 resolveSubject 补真实渠道名 —— 改造前传的是 ShareCode,
// 管理员在 Telegram 里收到的是"8fKd2xQ 渠道告警"这种读不出来的随机码。共享编码仍进 ChannelCode 列。
func evaluateAlerts(ctx context.Context, channel model.Channel, record model.ChannelHealthRecord) {
	if record.Healthy {
		// 延迟告警: 健康但超过阈值; 阈值 0 表示关闭该规则。
		threshold := SettingGetIntDefault(model.SettingKeyHealthLatencyMS, 1000)
		if threshold > 0 && record.LatencyMs >= int64(threshold) {
			RaiseAlertFacts(ctx, AlertFacts{
				ChannelID: channel.ID,
				Kind:      "degraded",
				Reason:    fmt.Sprintf("probe latency over threshold %dms", threshold),
				Fields: healthFields(channel, record, []alert.Field{
					{Label: "延迟阈值", Value: fmt.Sprintf("%d ms", threshold)},
					{Label: "说明", Value: "渠道仍可响应, 只是明显变慢"},
				}),
			})
			return
		}
		// 恢复: 本渠道最近一条告警是 down 或 degraded 时补发 recovered。
		last := model.ChannelAlert{}
		err := db.GetDB().WithContext(ctx).
			Where("channel_id = ?", channel.ID).
			Order("id DESC").First(&last).Error
		if err == nil && (last.Kind == "down" || last.Kind == "degraded") {
			RaiseAlertFacts(ctx, AlertFacts{
				ChannelID: channel.ID,
				Kind:      "recovered",
				Reason:    "channel healthy again",
				Fields: healthFields(channel, record, []alert.Field{
					{Label: "此前告警", Value: alertKindLabel(last.Kind)},
				}),
			})
		}
		return
	}

	// 异常: 连续 N 次都失败才视为宕机, 单次抖动不告警; N 由 alert_fail_streak 配置。
	streak := SettingGetIntDefault(model.SettingKeyAlertFailStreak, alertFailStreakDefault)
	if streak < 1 {
		streak = 1
	}
	recent := []model.ChannelHealthRecord{}
	if err := db.GetDB().WithContext(ctx).
		Where("channel_id = ?", channel.ID).
		Order("id DESC").Limit(streak).
		Find(&recent).Error; err != nil {
		return
	}
	if len(recent) < streak {
		return
	}
	for _, r := range recent {
		if r.Healthy {
			return
		}
	}
	RaiseAlertFacts(ctx, AlertFacts{
		ChannelID: channel.ID,
		Kind:      "down",
		Reason:    fmt.Sprintf("%d consecutive failed probes", streak),
		Fields: healthFields(channel, record, []alert.Field{
			{Label: "连续失败", Value: fmt.Sprintf("%d 次", streak)},
		}),
	})
}

// healthFields 组装探测类告警的公共明细行, 末尾再接该告警特有的几行。
// 管理员需要一眼看到"哪家渠道、探的哪个地址、这次到底怎么了", 而不是只有一句结论。
func healthFields(channel model.Channel, record model.ChannelHealthRecord, extra []alert.Field) []alert.Field {
	status := "无响应"
	if record.StatusCode > 0 {
		status = fmt.Sprintf("HTTP %d", record.StatusCode)
	}
	fields := []alert.Field{
		{Label: "渠道", Value: channelDisplayName(channel)},
		{Label: "探测地址", Value: channel.BaseURL},
		{Label: "延迟", Value: fmt.Sprintf("%d ms", record.LatencyMs)},
		{Label: "状态", Value: status},
	}
	// 没有错误就不写"探测错误"这行: 降级告警里出现一句占位的错误描述,
	// 管理员会以为是探测本身失败了, 而实际只是慢。
	if record.Error != "" {
		fields = append(fields, alert.Field{Label: "探测错误", Value: record.Error})
	}
	fields = append(fields, alert.Field{Label: "探测时间", Value: record.CheckedAt.Format("2006-01-02 15:04:05")})
	return append(fields, extra...)
}

// alertKindLabel 站内告警类型的中文说明(恢复告警里回指此前那条用)。
func alertKindLabel(kind string) string {
	switch kind {
	case "down":
		return "渠道故障"
	case "degraded":
		return "渠道延迟降级"
	case "key_disabled":
		return "渠道凭据熔断"
	case "model_disabled":
		return "渠道模型熔断"
	}
	return kind
}

// ChannelHealthList 返回某渠道的最近健康记录。
func ChannelHealthList(ctx context.Context, channelID int, limit int) ([]model.ChannelHealthRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows := []model.ChannelHealthRecord{}
	query := db.GetDB().WithContext(ctx).Order("checked_at DESC")
	if channelID > 0 {
		query = query.Where("channel_id = ?", channelID)
	}
	if err := query.Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
