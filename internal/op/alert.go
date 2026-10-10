package op

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// alertDedupDefaultMinutes 告警去重窗口默认值(分钟): 设置为 0 或读取失败时回退到此值。
// 窗口可配(alert_dedup_minutes), 让"同一故障多久重复提醒一次"成为运维策略而非硬编码常量。
const alertDedupDefaultMinutes = 10

// alertDedupWindow 返回当前生效的告警去重窗口; 负数按默认值处理, 0 表示不去重。
func alertDedupWindow() time.Duration {
	minutes := SettingGetIntDefault(model.SettingKeyAlertDedupMinutes, alertDedupDefaultMinutes)
	if minutes < 0 {
		minutes = alertDedupDefaultMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// webhookClient 通知出口复用短超时, 避免外部长连接挂死探测任务。
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// RaiseAlert 是全站唯一的告警出口: 对账差异、备份失败、健康降级都经由这里。
// 它负责去重、持久化 ChannelAlert 并在配置了 Webhook 时外发通知, 返回是否外发成功。
func RaiseAlert(ctx context.Context, channelID int, channelCode, kind, reason string) bool {
	if kind == "" || reason == "" {
		return false
	}
	reason = truncateReason(reason)
	// 去重: 窗口内已有同类型(同渠道)告警则跳过, 避免每轮探测都重发。
	var count int64
	// 去重窗口可配: 0 分钟表示不去重(每次都记), 用于排查阶段。
	if window := alertDedupWindow(); window > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.ChannelAlert{}).
			Where("channel_id = ? AND kind = ? AND reason = ? AND created_at > ?", channelID, kind, reason, time.Now().Add(-window)).
			Count(&count).Error; err == nil && count > 0 {
			return false
		}
	}
	alert := model.ChannelAlert{
		ChannelID:   channelID,
		ChannelCode: channelCode,
		Kind:        kind,
		Reason:      reason,
		CreatedAt:   time.Now(),
	}
	// Webhook 通知(可选): 配置了地址才外发, 外发失败不影响站内落库。
	if webhookURL, err := SettingGetString(model.SettingKeyAlertWebhookURL); err == nil && alertWebhookConfigured(webhookURL) {
		if sendWebhook(ctx, webhookURL, alert) {
			alert.Notified = true
		}
	}
	if err := db.GetDB().Create(&alert).Error; err != nil {
		return false
	}
	
	// 多渠道告警: 除了传统 Webhook, 额外支持 Telegram 和 Email。
	// 使用独立的 alert 包处理渠道分发, 不阻塞主流程。
	go func() {
		// 将 ChannelAlert 转换为通用 alert.Alert
		alertEvent := determineAlertEvent(kind)
		title := formatAlertTitle(kind, channelCode)
		message := formatAlertMessage(kind, channelCode, reason)
		
		// 异步发送到配置的告警渠道（webhook/telegram/email）
		// alert 包内部会读取配置并按启用渠道分发
		sendMultiChannelAlert(ctx, alertEvent, title, message)
	}()
	
	return alert.Notified
}

// sendWebhook 发送告警到 Webhook, 返回是否成功。
func sendWebhook(ctx context.Context, rawURL string, alert model.ChannelAlert) bool {
	payload, _ := json.Marshal(map[string]any{
		"channel":   alert.ChannelCode,
		"kind":      alert.Kind,
		"reason":    alert.Reason,
		"timestamp": alert.CreatedAt.Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := webhookClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// SendWebhookTest 供管理端一键验证 Webhook 连通性: 直接发送一条测试告警,
// 不入库也不去重, 仅返回是否送达。
func SendWebhookTest(ctx context.Context, rawURL string, alert model.ChannelAlert) bool {
	return sendWebhook(ctx, rawURL, alert)
}

// truncateReason 截断过长原因, 保持 reason 字段可读且不撑爆表格。
func truncateReason(reason string) string {
	runes := []rune(reason)
	if len(runes) <= 500 {
		return reason
	}
	return string(runes[:500]) + "...(truncated)"
}

// ChannelAlertList 返回最近告警记录。
func ChannelAlertList(ctx context.Context, limit int) ([]model.ChannelAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows := []model.ChannelAlert{}
	if err := db.GetDB().WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}



// determineAlertEvent 将渠道告警类型映射为通用事件类型。
func determineAlertEvent(kind string) string {
	switch kind {
	case "down":
		return "channel_failed"
	case "recovered":
		return "channel_recovered"
	default:
		return "channel_degraded"
	}
}

// formatAlertTitle 格式化告警标题。
func formatAlertTitle(kind, channelCode string) string {
	displayCode := channelCode
	if displayCode == "" {
		displayCode = "unknown"
	}
	switch kind {
	case "down":
		return "渠道故障: " + displayCode
	case "recovered":
		return "渠道恢复: " + displayCode
	case "degraded":
		return "渠道延迟: " + displayCode
	default:
		return "渠道告警: " + displayCode
	}
}

// formatAlertMessage 格式化告警详细信息。
func formatAlertMessage(kind, channelCode, reason string) string {
	displayCode := channelCode
	if displayCode == "" {
		displayCode = "unknown"
	}
	return "渠道: " + displayCode + "\n类型: " + kind + "\n原因: " + reason
}

// sendMultiChannelAlert 调用 alert 包发送多渠道告警，需要单独实现或直接调用 alert.Send。
// 这里暂时保留为占位符，后续集成时实现。
func sendMultiChannelAlert(ctx context.Context, event, title, message string) {
	// TODO: 集成 internal/alert 包
	// import "github.com/bestruirui/octopus/internal/alert"
	// alert.Send(ctx, alert.Alert{
	//     Event: alert.Event(event),
	//     Title: title,
	//     Message: message,
	//     Timestamp: time.Now(),
	// })
}

// alertWebhookConfigured 判断告警 Webhook 是否已配置有效地址, 供设置页与健康页展示。
func alertWebhookConfigured(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://")
}
