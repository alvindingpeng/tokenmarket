package op

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 邮件发送(SMTP): 服务注册验证码与找回密码。
//
// 读的是「告警邮件」那一套键(alert_email_*): 站内只保留一份发信配置, 管理后台也就只有一处
// 可配。此前这里另读一套 smtp_* 键, 两处都叫「SMTP 配置」却互不影响, 管理员填了不生效无从判断。
// 未配置时调用方拿到明确错误, 不静默吞掉。

// MailConfigured 返回 SMTP 是否已配置完整; 界面据此决定是否展示发码入口。
func MailConfigured() bool {
	host, _ := SettingGetString(model.SettingKeyAlertEmailSMTPHost)
	username, _ := SettingGetString(model.SettingKeyAlertEmailUsername)
	from, _ := SettingGetString(model.SettingKeyAlertEmailFrom)
	return host != "" && username != "" && from != ""
}

// MailSend 发送一封纯文本邮件; 465 端口走隐式 TLS, 其余由 net/smtp 自行协商 STARTTLS。
func MailSend(to, subject, body string) error {
	host, _ := SettingGetString(model.SettingKeyAlertEmailSMTPHost)
	portStr, _ := SettingGetString(model.SettingKeyAlertEmailSMTPPort)
	username, _ := SettingGetString(model.SettingKeyAlertEmailUsername)
	password, _ := SettingGetString(model.SettingKeyAlertEmailPassword)
	from, _ := SettingGetString(model.SettingKeyAlertEmailFrom)

	if host == "" || username == "" || from == "" {
		return fmt.Errorf("smtp not configured")
	}
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 587
	}

	msg := []byte("From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" + body)

	auth := smtp.PlainAuth("", username, password, host)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if port == 465 {
		return sendMailImplicitTLS(addr, host, auth, from, []string{to}, msg)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}

// sendMailImplicitTLS 465 隐式 TLS 发送: 先 TLS 握手, 再在加密连接上跑 SMTP 会话。
func sendMailImplicitTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
