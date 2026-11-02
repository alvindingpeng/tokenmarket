package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// SettingsProvider 读取一条告警相关设置, 由启动时注入(见 cmd/start.go)。
// alert 不直接 import op: op 需要调用告警外发, alert 又需要 op 读设置, 直接互相依赖会形成导入环。
// 这里只声明"取设置"的函数类型, 由外部把 op.SettingGetString 接上。
var SettingsProvider func(key model.SettingKey) (string, error)

// settingString 读取一条设置; 未注入 provider 时按"未配置"处理并说明原因, 不让调用方误判为已配置。
func settingString(key model.SettingKey) (string, error) {
	if SettingsProvider == nil {
		return "", fmt.Errorf("alert settings provider not injected")
	}
	return SettingsProvider(key)
}

// Event 告警事件类型, 供 webhook 消费端按类型分流。
type Event string

// 事件名与站内告警 kind 一一对应, 消费端可据此分流(例如只把 critical 转到值班群)。
const (
	EventChannelFailed        Event = "channel_failed"         // 渠道连续失败。
	EventChannelRecovered     Event = "channel_recovered"      // 渠道恢复。
	EventChannelDegraded      Event = "channel_degraded"       // 渠道延迟降级。
	EventChannelKeyDisabled   Event = "channel_key_disabled"   // 渠道凭据被熔断。
	EventChannelModelDisabled Event = "channel_model_disabled" // 渠道模型被熔断。
	EventBalanceLow           Event = "balance_low"            // 余额不足。
	EventQuotaExceeded        Event = "quota_exceeded"         // 配额超限。
	EventBillingMismatch      Event = "billing_mismatch"       // 计费对账不平。
	EventBillingFailed        Event = "billing_failed"         // 计费结算失败。
	EventBackupFailed         Event = "backup_failed"          // 自动备份失败。
	EventArchiveFailed        Event = "archive_failed"         // 日志归档失败。
	EventTaskFailed           Event = "task_failed"            // 后台任务连续失败。
	EventTaskRecovered        Event = "task_recovered"         // 后台任务恢复正常。
	EventUpdateAvailable      Event = "update_available"       // 发现新版本。
	EventTest                 Event = "test"                   // 告警通道连通性测试。
	EventSystemAlert          Event = "system_alert"           // 未归类的系统告警。
)

// Alert 表示一个待发送的告警。
//
// Fields/Kind/Scope 是本次改造新增的结构化部分: 改造前外发内容只有 Title 与 Message 两段文本,
// 而 Message 里塞的是渠道共享编码, 管理员既看不出是哪家渠道, 也拿不到模型名/错误类型这类事实。
// 现在由 op 侧在产生告警时把事实填成 Fields, 各出口(Telegram/邮件/Webhook)按同一份数据排版。
type Alert struct {
	Event     Event
	Title     string
	Message   string
	Timestamp time.Time

	Severity string  // critical | warning | info; 空值由 Kind 推导。
	Kind     string  // 站点内部告警类型(channel_alerts.kind), 决定标题前缀与图标。
	Scope    string  // 归属主体, 例如渠道名或"系统"。
	Fields   []Field // 明细行, 按给定顺序输出。
}

// Field 一条告警明细: 标签 + 值。
type Field struct {
	Label string
	Value string
}

// SendOnce 指定单个渠道发送一条告警, 供管理面板做连通性测试; 配置缺失或发送失败原样返回错误。
func SendOnce(ctx context.Context, channel string, alert Alert) error {
	switch channel {
	case "webhook":
		return sendWebhook(ctx, alert)
	case "telegram":
		return sendTelegram(ctx, alert)
	case "email":
		return sendEmail(ctx, alert)
	default:
		return fmt.Errorf("unknown alert channel %q", channel)
	}
}

// Send 根据配置的渠道发送告警: webhook, telegram, email; 返回是否至少有一个渠道发送成功。
// 这是全站唯一的外发入口, 调用方(op.RaiseAlert)据返回值回填"已通知"标记。
//
// 失败的渠道会把原因带出来(见 SendDetailed): 只回一个 false 的话, 调用方只能记
// "没发出去", 却不知道是 token 没配、chat_id 写错还是实体解析被拒 —— 补发任务也就无从判断
// 该重试还是该放弃。
func Send(ctx context.Context, alert Alert) bool {
	ok, _ := SendDetailed(ctx, alert)
	return ok
}

// SendDetailed 同 Send, 额外返回各渠道的失败原因(按 "渠道: 原因" 拼接)。
// 只要有一个渠道成功就认为整条告警已送达(ok=true), 部分失败的原因仍然返回给调用方记录。
func SendDetailed(ctx context.Context, alert Alert) (ok bool, err error) {
	channelsRaw, settingErr := settingString(model.SettingKeyAlertChannels)
	if settingErr != nil || channelsRaw == "" {
		channelsRaw = "webhook" // 默认仅 webhook
	}
	channels := strings.Split(channelsRaw, ",")

	var failures []string
	sent := false
	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		var sendErr error
		switch ch {
		case "webhook":
			sendErr = sendWebhook(ctx, alert)
		case "telegram":
			sendErr = sendTelegram(ctx, alert)
		case "email":
			sendErr = sendEmail(ctx, alert)
		default:
			continue
		}
		if sendErr != nil {
			log.Errorf("failed to send %s alert: %v", ch, sendErr)
			failures = append(failures, ch+": "+sendErr.Error())
			continue
		}
		sent = true
	}
	if len(failures) > 0 {
		return sent, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return sent, nil
}

// sendWebhook 发送 Webhook 告警。
func sendWebhook(ctx context.Context, alert Alert) error {
	url, err := settingString(model.SettingKeyAlertWebhookURL)
	if err != nil || url == "" {
		return fmt.Errorf("webhook url not configured")
	}

	body := alertPayload(alert)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// alertPayload 把告警序列化成 Webhook 请求体。
// title/message 仍是渲染好的整段文本, 既有消费端无需改动; 新增字段供需要按类型分流、
// 或要拿渠道名/模型名做二次处理的消费端使用。
func alertPayload(alert Alert) []byte {
	payload := map[string]interface{}{
		"event":     string(alert.Event),
		"kind":      alert.Kind,
		"severity":  levelOf(alert),
		"title":     alert.renderTitle(),
		"message":   alert.renderMessage(),
		"timestamp": alert.Timestamp.Unix(),
	}
	if alert.Scope != "" {
		payload["scope"] = alert.Scope
	}
	if len(alert.Fields) > 0 {
		fields := make([]map[string]string, 0, len(alert.Fields))
		for _, field := range alert.Fields {
			fields = append(fields, map[string]string{"label": field.Label, "value": field.Value})
		}
		payload["fields"] = fields
	}

	body, _ := json.Marshal(payload)
	return body
}

// SendWebhookTo 向指定地址发送一条告警, 供管理面板做连通性测试。
// 与 sendWebhook 的区别只在地址来自参数而不是设置: 连通性测试要回答的问题是
// "界面上这一行 URL 收不收得到", 拿设置里的值测出来的成功说明不了别的地址行不行。
func SendWebhookTo(ctx context.Context, rawURL string, alert Alert) error {
	if rawURL == "" {
		return fmt.Errorf("webhook url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", rawURL, bytes.NewReader(alertPayload(alert)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// telegramAPIBase Telegram Bot API 基址。声明为变量而非字面量, 测试才能指向本地假服务
// 验证"Markdown 实体解析失败 → 纯文本降级重发"这条真实路径(纯函数测试覆盖不到)。
var telegramAPIBase = "https://api.telegram.org"

// sendTelegram 发送 Telegram 告警。
func sendTelegram(ctx context.Context, alert Alert) error {
	botToken, err := settingString(model.SettingKeyAlertTelegramBotToken)
	if err != nil || botToken == "" {
		return fmt.Errorf("telegram bot token not configured")
	}
	chatID, err := settingString(model.SettingKeyAlertTelegramChatID)
	if err != nil || chatID == "" {
		return fmt.Errorf("telegram chat id not configured")
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, botToken)

	err = postTelegramMessage(ctx, url, chatID, telegramText(alert), "Markdown")
	// Markdown 实体解析失败时降级重发纯文本: 转义规则再全也可能漏掉新的可变文本来源,
	// 而"排版丢失"远好过"告警彻底发不出去"。只有实体解析错误才降级 —— token/chat_id 这类
	// 参数错误重发同样失败, 徒增一次无效请求并把真实原因埋在后面。
	if err != nil && isTelegramEntityParseError(err) {
		log.Warnf("telegram markdown rejected, resending as plain text: %v", err)
		if plainErr := postTelegramMessage(ctx, url, chatID, telegramPlainText(alert), ""); plainErr == nil {
			return nil
		} else {
			err = plainErr
		}
	}
	if err != nil {
		return telegramRequestError(err, botToken)
	}
	return nil
}

// postTelegramMessage 发一条 Telegram 消息; parseMode 为空表示纯文本。
// 网络层错误原样返回, 由调用方统一做 token 脱敏。
func postTelegramMessage(ctx context.Context, url, chatID, text, parseMode string) error {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return telegramError(resp.StatusCode, respBody)
	}
	return nil
}

// isTelegramEntityParseError 判断错误是否为 Markdown 实体解析失败(可降级重发)。
func isTelegramEntityParseError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "can't parse entities") ||
		strings.Contains(message, "can't find end of")
}

// escapeTelegramMarkdown 转义 Telegram legacy Markdown 的实体起始字符。
// Title 与 Message 是任意文本, 错误信息里下划线/星号/反引号很常见, 不转义会被 parse_mode
// 当成实体起点, Telegram 直接回 400 "Can't parse entities", 整条告警发不出去。
func escapeTelegramMarkdown(s string) string {
	return strings.NewReplacer("\\", "\\\\", "_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[").Replace(s)
}

// telegramRequestError 脱敏网络错误中的 Telegram bot token, 避免 token URL 被日志记录。
func telegramRequestError(err error, botToken string) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), botToken, "[REDACTED]")
	return fmt.Errorf("telegram request failed: %s", message)
}

// telegramError 把 Telegram 的错误响应还原成可读错误。
// description 才是真正原因(例如 Bad Request: chat not found), 只报 HTTP 状态码等于把排查线索丢掉;
// chat not found 额外给出最常见的成因, 免得在设置页对着一个 400 无从下手。
func telegramError(status int, body []byte) error {
	var payload struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Description != "" {
		if strings.Contains(payload.Description, "chat not found") {
			return fmt.Errorf("telegram api %d: %s(chat_id 无效, 或 bot 尚未加入该会话; 请确认 chat_id 符号与位数)", status, payload.Description)
		}
		return fmt.Errorf("telegram api %d: %s", status, payload.Description)
	}
	return fmt.Errorf("telegram api returned status %d", status)
}

// sendEmail 发送邮件告警。
func sendEmail(ctx context.Context, alert Alert) error {
	smtpHost, _ := settingString(model.SettingKeyAlertEmailSMTPHost)
	smtpPortStr, _ := settingString(model.SettingKeyAlertEmailSMTPPort)
	username, _ := settingString(model.SettingKeyAlertEmailUsername)
	password, _ := settingString(model.SettingKeyAlertEmailPassword)
	from, _ := settingString(model.SettingKeyAlertEmailFrom)
	toRaw, _ := settingString(model.SettingKeyAlertEmailTo)

	if smtpHost == "" || username == "" || password == "" || from == "" || toRaw == "" {
		return fmt.Errorf("email settings incomplete")
	}

	smtpPort, _ := strconv.Atoi(smtpPortStr)
	if smtpPort == 0 {
		smtpPort = 587
	}

	recipients := strings.Split(toRaw, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	subject := fmt.Sprintf("Subject: [Switchyard Alert] %s\r\n", alert.Title)
	body := fmt.Sprintf("Content-Type: text/plain; charset=UTF-8\r\n\r\n%s", emailBody(alert))
	msg := []byte("From: " + from + "\r\n" +
		"To: " + strings.Join(recipients, ",") + "\r\n" +
		subject + body)

	auth := smtp.PlainAuth("", username, password, smtpHost)
	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)
	return smtp.SendMail(addr, auth, from, recipients, msg)
}
