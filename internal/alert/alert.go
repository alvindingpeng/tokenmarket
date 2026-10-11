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

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// Event 告警事件类型。
type Event string

const (
	EventChannelFailed    Event = "channel_failed"    // 渠道连续失败。
	EventChannelRecovered Event = "channel_recovered" // 渠道恢复。
	EventBalanceLow       Event = "balance_low"       // 余额不足。
	EventQuotaExceeded    Event = "quota_exceeded"    // 配额超限。
)

// Alert 表示一个待发送的告警。
type Alert struct {
	Event     Event
	Title     string
	Message   string
	Timestamp time.Time
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

// Send 根据配置的渠道发送告警: webhook, telegram, email。
func Send(ctx context.Context, alert Alert) {
	channelsRaw, err := op.SettingGetString(model.SettingKeyAlertChannels)
	if err != nil || channelsRaw == "" {
		channelsRaw = "webhook" // 默认仅 webhook
	}
	channels := strings.Split(channelsRaw, ",")

	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		switch ch {
		case "webhook":
			if err := sendWebhook(ctx, alert); err != nil {
				log.Errorf("failed to send webhook alert: %v", err)
			}
		case "telegram":
			if err := sendTelegram(ctx, alert); err != nil {
				log.Errorf("failed to send telegram alert: %v", err)
			}
		case "email":
			if err := sendEmail(ctx, alert); err != nil {
				log.Errorf("failed to send email alert: %v", err)
			}
		}
	}
}

// sendWebhook 发送 Webhook 告警。
func sendWebhook(ctx context.Context, alert Alert) error {
	url, err := op.SettingGetString(model.SettingKeyAlertWebhookURL)
	if err != nil || url == "" {
		return fmt.Errorf("webhook url not configured")
	}

	payload := map[string]interface{}{
		"event":     string(alert.Event),
		"title":     alert.Title,
		"message":   alert.Message,
		"timestamp": alert.Timestamp.Unix(),
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

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// sendTelegram 发送 Telegram 告警。
func sendTelegram(ctx context.Context, alert Alert) error {
	botToken, err := op.SettingGetString(model.SettingKeyAlertTelegramBotToken)
	if err != nil || botToken == "" {
		return fmt.Errorf("telegram bot token not configured")
	}
	chatID, err := op.SettingGetString(model.SettingKeyAlertTelegramChatID)
	if err != nil || chatID == "" {
		return fmt.Errorf("telegram chat id not configured")
	}

	text := fmt.Sprintf("🔔 *%s*\n\n%s\n\n_%s_",
		escapeTelegramMarkdown(alert.Title),
		escapeTelegramMarkdown(alert.Message),
		escapeTelegramMarkdown(alert.Timestamp.Format("2006-01-02 15:04:05")))

	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
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

// escapeTelegramMarkdown 转义 Telegram legacy Markdown 的实体起始字符。
// Title 与 Message 是任意文本, 错误信息里下划线/星号/反引号很常见, 不转义会被 parse_mode
// 当成实体起点, Telegram 直接回 400 "Can't parse entities", 整条告警发不出去。
func escapeTelegramMarkdown(s string) string {
	return strings.NewReplacer("\\", "\\\\", "_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[").Replace(s)
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
	smtpHost, _ := op.SettingGetString(model.SettingKeyAlertEmailSMTPHost)
	smtpPortStr, _ := op.SettingGetString(model.SettingKeyAlertEmailSMTPPort)
	username, _ := op.SettingGetString(model.SettingKeyAlertEmailUsername)
	password, _ := op.SettingGetString(model.SettingKeyAlertEmailPassword)
	from, _ := op.SettingGetString(model.SettingKeyAlertEmailFrom)
	toRaw, _ := op.SettingGetString(model.SettingKeyAlertEmailTo)

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

	subject := fmt.Sprintf("Subject: [Octopus Alert] %s\r\n", alert.Title)
	body := fmt.Sprintf("Content-Type: text/plain; charset=UTF-8\r\n\r\n%s\n\nTime: %s", alert.Message, alert.Timestamp.Format("2006-01-02 15:04:05"))
	msg := []byte("From: " + from + "\r\n" +
		"To: " + strings.Join(recipients, ",") + "\r\n" +
		subject + body)

	auth := smtp.PlainAuth("", username, password, smtpHost)
	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)
	return smtp.SendMail(addr, auth, from, recipients, msg)
}
