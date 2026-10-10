package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	EventChannelFailed    Event = "channel_failed"     // 渠道连续失败。
	EventChannelRecovered Event = "channel_recovered"  // 渠道恢复。
	EventBalanceLow       Event = "balance_low"        // 余额不足。
	EventQuotaExceeded    Event = "quota_exceeded"     // 配额超限。
)

// Alert 表示一个待发送的告警。
type Alert struct {
	Event     Event
	Title     string
	Message   string
	Timestamp time.Time
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
		alert.Title, alert.Message, alert.Timestamp.Format("2006-01-02 15:04:05"))

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

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram api returned status %d", resp.StatusCode)
	}
	return nil
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
