// Package totp 实现 RFC 6238 基于时间的一次性密码(TOTP), 仅用标准库, 不引入外部依赖。
//
// 采用认证器 App 的通行参数: HMAC-SHA1、30 秒步长、6 位十进制码。密钥以 base32(RFC 4648,
// 不带填充)文本表示, 与 Google Authenticator / 1Password 等互通。
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period 步长(秒), RFC 6238 默认。
	Period = 30
	// Digits 码长, RFC 6238 默认 6。
	Digits = 6
	// SecretBytes 密钥熵(字节)。20 字节 = 160 位, 与 HMAC-SHA1 块大小对齐, 是通行推荐值。
	SecretBytes = 20
)

// b32 是无填充的 base32 编码器, 与认证器 App 的密钥表示一致。
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret 生成一段新的 TOTP 密钥(加密随机), 返回其 base32 文本。
func GenerateSecret() (string, error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return EncodeSecret(raw), nil
}

// EncodeSecret 把原始字节编码为 base32 文本(大写、无填充)。
func EncodeSecret(raw []byte) string {
	return strings.ToUpper(b32.EncodeToString(raw))
}

// decodeSecret 解析 base32 密钥文本, 兼容带/不带填充、大小写混排与分组分隔符。
func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.ReplaceAll(s, " ", "")
	// 认证器密钥常按 4 字一组分隔展示, 去掉分隔符。
	s = strings.ReplaceAll(s, "-", "")
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	raw, err := base32.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid totp secret: %w", err)
	}
	return raw, nil
}

// Generate 按给定 Unix 时间与密钥计算 6 位动态码。
func Generate(secret string, unixTime int64) (string, error) {
	raw, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return generateWithRaw(raw, unixTime), nil
}

func generateWithRaw(raw []byte, unixTime int64) string {
	counter := uint64(unixTime / Period)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, raw)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	// 动态截断: 取末 4 位作偏移, 从该偏移起读 4 字节, 屏蔽最高位得到 31 位整数。
	offset := sum[len(sum)-1] & 0x0f
	binaryCode := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	code := binaryCode % pow10(Digits)
	return fmt.Sprintf("%0*d", Digits, code)
}

// Validate 校验一个动态码是否匹配当前时间窗口, 允许前后各 window 个步长的容差以容忍时钟漂移。
// 空码或格式非法一律不通过; 比较用常数时间, 避免逐位短路泄漏。
func Validate(secret, code string, window int, now time.Time) bool {
	_, ok := MatchStep(secret, code, window, now)
	return ok
}

// MatchStep returns the matched counter for persistent replay protection.
func MatchStep(secret, code string, window int, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits || !isDigits(code) {
		return 0, false
	}
	raw, err := decodeSecret(secret)
	if err != nil || len(raw) == 0 || window < 0 || window > 10 || now.Unix() < 0 {
		return 0, false
	}
	t := now.Unix()
	for i := -window; i <= window; i++ {
		candidate := generateWithRaw(raw, t+int64(i)*Period)
		if hmac.Equal([]byte(candidate), []byte(code)) {
			return (t + int64(i)*Period) / Period, true
		}
	}
	return 0, false
}

// OtpauthURL 生成可交给认证器 App 的 otpauth:// 链接(供二维码编码)。
func OtpauthURL(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeSecretMust(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", Digits))
	q.Set("period", fmt.Sprintf("%d", Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// EncodeSecretMust 规范化密钥文本; 输入非法时原样大写返回, 仅用于展示场景。
func EncodeSecretMust(secret string) string {
	raw, err := decodeSecret(secret)
	if err != nil {
		return strings.ToUpper(strings.TrimSpace(secret))
	}
	return EncodeSecret(raw)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func pow10(n int) uint32 {
	var p uint32 = 1
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}
