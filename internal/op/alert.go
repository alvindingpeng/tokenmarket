package op

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
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
// 它负责去重、持久化 ChannelAlert, 并把外发交给注入的分发器(按 alert_channels 决定渠道)。
// 返回是否已记录(命中去重窗口返回 false); 外发是异步的, 成功后回填 notified 标记。
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
	// 局部变量不可命名为 alert: 那会遮蔽 alert 包名, 使本文件无法引用包内的分发函数。
	record := model.ChannelAlert{
		ChannelID:   channelID,
		ChannelCode: channelCode,
		Kind:        kind,
		Reason:      reason,
		CreatedAt:   time.Now(),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		return false
	}

	// 外发只走这一条路: 分发器按 alert_channels 决定渠道(默认含 webhook)并使用 alert_webhook_url。
	// 这里不再单独发一次 webhook —— 否则 alert_channels 含 webhook 时同一条告警会外发两次。
	event := determineAlertEvent(kind)
	title := formatAlertTitle(kind, channelCode)
	message := formatAlertMessage(kind, channelCode, reason)
	// 外发要在调用方返回之后继续, 故脱掉请求的取消信号(保留其中的值):
	// 直接用请求 ctx 的话, HTTP 处理函数一返回 ctx 就被取消, 发送会半路失败。
	sendCtx := context.WithoutCancel(ctx)
	go func() {
		if sendMultiChannelAlert(sendCtx, event, title, message) {
			// 回填「已通知」, 让界面能区分「已外发」与「仅站内」。
			db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", record.ID).Update("notified", true)
		}
	}()

	return true
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

// AlertDispatcher 把一条站内告警外发到设置里启用的渠道(webhook/telegram/email), 由启动时注入(见 cmd/start.go)。
// op 不直接 import internal/alert: alert 读取告警设置需要 op, 两边直接互相依赖会形成导入环,
// 故这里只声明分发函数的签名, 由 cmd 把 alert.Send 接上。未注入时(例如单元测试)只保留站内告警, 不外发。
// 返回是否至少有一个渠道发送成功。
var AlertDispatcher func(ctx context.Context, event, title, message string) bool

// alertDispatchMissingOnce 让「未接线」只提示一次, 避免每条告警都刷日志。
var alertDispatchMissingOnce sync.Once

// sendMultiChannelAlert 把告警交给注入的分发器; 分发器内部按 alert_channels 决定实际渠道。
func sendMultiChannelAlert(ctx context.Context, event, title, message string) bool {
	if AlertDispatcher == nil {
		// 未接线时告警只会留在站内, 配好的 Telegram/Email 收不到。
		// 这正是曾经的静默失效点, 故留下一条可见日志而不是直接 return。
		alertDispatchMissingOnce.Do(func() {
			log.Warnf("alert dispatcher not injected: 站内告警不会外发到配置的告警渠道 (event=%s)", event)
		})
		return false
	}
	return AlertDispatcher(ctx, event, title, message)
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
