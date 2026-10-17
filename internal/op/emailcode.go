package op

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 邮箱验证码: 注册与找回密码共用一套发放/校验, purpose 区分用途防跨用挪用。
// 防轰炸三道闸: 同邮箱冷却 60s、单邮箱小时上限、单 IP 小时限额(内存计量, 多实例部署需换共享存储)。

const (
	emailCodeTTL       = 10 * time.Minute // 验证码有效期
	emailCodeCooldown  = 60 * time.Second // 同邮箱重发冷却
	emailCodePerEmailH = 5                // 单邮箱每小时最多发 5 封
	emailCodePerIPH    = 20               // 单 IP 每小时最多发 20 封
	emailCodeMaxVerify = 5                // 单码最多试 5 次, 超了作废重来
)

// emailCodeSendLog 发送限流的内存计量: key -> 最近发送与小时窗口计数。
type emailCodeSendLog struct {
	last     time.Time
	hour     time.Time
	count    int
	ipHour   time.Time
	ipCount  int
}

var emailCodeLogs = map[string]*emailCodeSendLog{}

// EmailCodeSend 生成并发送验证码; 返回剩余冷却秒数为 0 表示已发出。
func EmailCodeSend(ctx context.Context, email, purpose, clientIP string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !model.ValidEmail(email) {
		return fmt.Errorf("invalid email")
	}
	if purpose != model.EmailCodePurposeRegister && purpose != model.EmailCodePurposeReset {
		return fmt.Errorf("invalid purpose")
	}
	if !MailConfigured() {
		return fmt.Errorf("mail service not configured")
	}
	if err := emailCodeRateOK(email, clientIP); err != nil {
		return err
	}

	code, err := randomEmailCode()
	if err != nil {
		return err
	}
	now := time.Now()
	// 同邮箱同用途只保留一条未用码: 重发直接作废旧码, 避免多个码同时可用。
	if err := db.GetDB().WithContext(ctx).
		Model(&model.EmailCode{}).
		Where("email = ? AND purpose = ? AND used_at IS NULL", email, purpose).
		Updates(map[string]any{"used_at": now}).Error; err != nil {
		return fmt.Errorf("invalidate old code: %w", err)
	}
	record := model.EmailCode{
		Email:     email,
		Purpose:   purpose,
		CodeHash:  hashCode(code),
		ExpiresAt: now.Add(emailCodeTTL),
	}
	if err := db.GetDB().WithContext(ctx).Create(&record).Error; err != nil {
		return fmt.Errorf("save code: %w", err)
	}
	if err := MailSend(email, emailCodeSubject(purpose), emailCodeBody(purpose, code)); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}

// EmailCodeVerify 校验验证码并作废(一次性); 错误信息不区分"不存在/过期/错码", 防探测。
func EmailCodeVerify(ctx context.Context, email, purpose, code string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("invalid verification code")
	}
	var record model.EmailCode
	err := db.GetDB().WithContext(ctx).
		Where("email = ? AND purpose = ? AND used_at IS NULL", email, purpose).
		Order("id DESC").First(&record).Error
	if err != nil {
		return fmt.Errorf("invalid verification code")
	}
	if time.Now().After(record.ExpiresAt) {
		return fmt.Errorf("invalid verification code")
	}
	if record.Attempts >= emailCodeMaxVerify {
		return fmt.Errorf("invalid verification code")
	}
	if hashCode(code) != record.CodeHash {
		db.GetDB().WithContext(ctx).Model(&record).Update("attempts", record.Attempts+1)
		return fmt.Errorf("invalid verification code")
	}
	now := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&record).
		Updates(map[string]any{"used_at": now, "attempts": record.Attempts + 1}).Error; err != nil {
		return fmt.Errorf("consume code: %w", err)
	}
	return nil
}

// emailCodeRateOK 发送限流检查; 通过后立即记账。
func emailCodeRateOK(email, clientIP string) error {
	now := time.Now()
	log := emailCodeLogs[email]
	if log == nil {
		log = &emailCodeSendLog{}
		emailCodeLogs[email] = log
	}
	if !log.last.IsZero() && now.Sub(log.last) < emailCodeCooldown {
		return fmt.Errorf("please wait %d seconds before requesting another code", int(emailCodeCooldown.Seconds()))
	}
	if now.Sub(log.hour) >= time.Hour {
		log.hour, log.count = now, 0
	}
	if log.count >= emailCodePerEmailH {
		return fmt.Errorf("too many codes requested, try again later")
	}
	// IP 维度单独计量: 公共入口下同一 IP 换邮箱刷发也要拦住。
	ipKey := "ip:" + clientIP
	ipLog := emailCodeLogs[ipKey]
	if ipLog == nil {
		ipLog = &emailCodeSendLog{}
		emailCodeLogs[ipKey] = ipLog
	}
	if now.Sub(ipLog.ipHour) >= time.Hour {
		ipLog.ipHour, ipLog.ipCount = now, 0
	}
	if ipLog.ipCount >= emailCodePerIPH {
		return fmt.Errorf("too many codes requested, try again later")
	}
	log.last, log.count = now, log.count+1
	ipLog.ipHour, ipLog.ipCount = now, ipLog.ipCount+1
	return nil
}

// randomEmailCode 生成 6 位数字验证码, 用 crypto/rand 避免可预测。
func randomEmailCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashCode 验证码只存 SHA-256: 备份导出含全表, 明文落库等于随备份外泄。
func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func emailCodeSubject(purpose string) string {
	if purpose == model.EmailCodePurposeReset {
		return "[Octopus] Password Reset Code"
	}
	return "[Octopus] Registration Verification Code"
}

func emailCodeBody(purpose, code string) string {
	action := "complete your registration"
	if purpose == model.EmailCodePurposeReset {
		action = "reset your password"
	}
	return "Your verification code is: " + code + "\n\n" +
		"This code expires in 10 minutes and can only be used once.\n" +
		"If you did not request this code to " + action + ", please ignore this email."
}
