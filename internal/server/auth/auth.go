package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/golang-jwt/jwt/v5"
)

// Claims 携带身份与令牌版本: 多用户下 token 必须自证是谁, 令牌版本用于改密/禁用后吊销既有会话。
type Claims struct {
	UserID       uint       `json:"uid"`
	Role         model.Role `json:"role"`
	TokenVersion int        `json:"ver"`
	jwt.RegisteredClaims
}

// secret 全局签名密钥, 首次启动生成并持久化到 settings 的内部键; 不再使用用户名+密码派生,
// 否则任何用户改密都会打掉所有人的会话, 也无法承载多用户。
var secret []byte

// InitSecret 读取或生成全局签名密钥; 必须在 settings 缓存就绪后调用。
func InitSecret() error {
	value, err := op.SettingGetString(model.SettingKeyAuthSecret)
	if err != nil || value == "" {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("failed to generate auth secret: %w", err)
		}
		value = hex.EncodeToString(buf)
		if err := op.SettingUpsert(model.SettingKeyAuthSecret, value); err != nil {
			return fmt.Errorf("failed to persist auth secret: %w", err)
		}
	}
	secret = []byte(value)
	return nil
}

func GenerateJWTToken(user model.User, expiresSec int) (string, int, error) {
	now := time.Now()
	maxAge := int((15 * time.Minute).Seconds())
	if expiresSec > 0 {
		maxAge = expiresSec
	} else if expiresSec == -1 {
		maxAge = int((30 * 24 * time.Hour).Seconds())
	}
	claims := &Claims{
		UserID:       user.ID,
		Role:         user.Role,
		TokenVersion: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    conf.APP_NAME,
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(maxAge) * time.Second)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", 0, err
	}
	return token, maxAge, nil
}

// VerifyJWTToken 校验令牌并返回身份; 令牌有效但账号已禁用/改密/角色变化时由调用方按 claims 与库内账号比对。
func VerifyJWTToken(token string) (*Claims, error) {
	claims := &Claims{}
	jwtToken, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil || !jwtToken.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func GenerateAPIKey() string {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	maxI := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, maxI)
		if err != nil {
			return ""
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b)
}
