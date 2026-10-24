package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/golang-jwt/jwt/v5"
)

// 高危操作二次验证(step-up): 管理员改余额、审批提现、导入备份、导出敏感数据、生成结算、
// 计费恢复、改系统设置/用户管理等写操作, 除登录态外还要求"刚用密码重新确认过身份"。
//
// 采用密码重新认证而不是 TOTP: 部署没有统一的第二因素, 密码是人人都有的凭据。
// 重新认证通过后签发一枚短期 step-up 凭证, 受保护请求带在 X-Step-Up-Token 头上。
//
// 凭证三重绑定, 任何一环变了就作废:
//   - 用户: uid 必须等于当前登录身份, 不能跨账号复用;
//   - 令牌版本: ver 比对库里 token_version, 改密/禁用/登出会递增, 旧凭证当场失效;
//   - 会话: sid 是当前登录 JWT 的指纹(step-up 签发与校验都对同一个 auth cookie 取指纹),
//     凭证换到另一台登录会话(或另一枚 JWT)上就对不上, 防止凭证被跨会话挪用。
// 签名走服务端全局密钥(HS256), 与登录令牌同一套 secret: 伪造要么不知道密钥, 要么过期。

const (
	// StepUpPurpose 凭证用途标记: 拒绝把登录 JWT 或其它用途的令牌当 step-up 凭证使。
	StepUpPurpose = "step_up"
	// StepUpTTL 凭证有效期: 只覆盖"一次高危操作从确认到提交"的窗口, 不做长效通行证。
	StepUpTTL = 5 * time.Minute
)

// StepUpClaims step-up 凭证载荷; RegisteredClaims 只用 IssuedAt/ExpiresAt/Issuer。
// 字段名与登录 Claims(uid/ver)刻意错开: 两种令牌共用同一签名密钥, 载荷同名会让
// step-up 凭证被当成登录 JWT 接受(登录校验只看 uid+ver)。现在反向也不成立:
// 登录 JWT 没有 pus=step_up, 拿来当 step-up 凭证会被 VerifyStepUpToken 拒掉。
type StepUpClaims struct {
	UserID       uint   `json:"suid"`
	TokenVersion int    `json:"sver"`
	Session      string `json:"sid"`
	Purpose      string `json:"pus"`
	jwt.RegisteredClaims
}

// SessionFingerprint 登录会话指纹: 对会话 JWT 取 SHA-256 摘要(截断)。
// 只存指纹不存原值, 凭证本身不携带会话令牌, 泄露凭证也带不走登录态。
func SessionFingerprint(sessionToken string) string {
	sum := sha256.Sum256([]byte(sessionToken))
	return hex.EncodeToString(sum[:16])
}

// IssueStepUpToken 为"当前登录会话"签发短期 step-up 凭证, 返回凭证与有效期秒数。
// sessionToken 必须是本次登录的 JWT: 没有会话就没有绑定对象, 直接拒绝签发。
func IssueStepUpToken(user model.User, sessionToken string) (string, int, error) {
	if sessionToken == "" {
		return "", 0, fmt.Errorf("cannot issue step-up token without a login session")
	}
	now := time.Now()
	claims := &StepUpClaims{
		UserID:       user.ID,
		TokenVersion: user.TokenVersion,
		Session:      SessionFingerprint(sessionToken),
		Purpose:      StepUpPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    conf.APP_NAME,
			ExpiresAt: jwt.NewNumericDate(now.Add(StepUpTTL)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", 0, err
	}
	return token, int(StepUpTTL.Seconds()), nil
}

// VerifyStepUpToken 校验 step-up 凭证; 任何一项对不上都返回错误, 由调用方统一按"需要二次验证"处理。
// user 与 sessionToken 必须取自当前请求的登录态: 凭证的 uid/ver/sid 就是与它们比对的。
func VerifyStepUpToken(token, sessionToken string, user model.User) error {
	if token == "" || sessionToken == "" {
		return fmt.Errorf("step-up credential missing")
	}
	claims := &StepUpClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	}, strictJWTParserOptions()...)
	if err != nil || !parsed.Valid {
		return fmt.Errorf("invalid step-up credential")
	}
	if claims.Purpose != StepUpPurpose {
		return fmt.Errorf("credential is not a step-up credential")
	}
	if claims.UserID != user.ID {
		return fmt.Errorf("step-up credential belongs to another user")
	}
	if claims.TokenVersion != user.TokenVersion {
		return fmt.Errorf("step-up credential was invalidated by a credential change")
	}
	if claims.Session != SessionFingerprint(sessionToken) {
		return fmt.Errorf("step-up credential belongs to another login session")
	}
	return nil
}

// step-up 密码验证限流: 与登录防爆破同一套闸门(指数退避), 但计数主体独立,
// 互不挤占对方的失败额度。key 前缀隔离, 避免和登录主体撞名。
func stepUpKeys(clientIP string, userID uint) (string, string) {
	return "stepup:ip:" + clientIP, "stepup:u:" + strconv.FormatUint(uint64(userID), 10)
}

// StepUpAllowed 判定当前主体是否允许再试一次密码验证; 不允许时返回建议等待时长。
func StepUpAllowed(clientIP string, userID uint) (time.Duration, bool) {
	keyIP, keyUser := stepUpKeys(clientIP, userID)
	if retry, ok := LoginAllowed(keyIP); !ok {
		return retry, false
	}
	return LoginAllowed(keyUser)
}

// StepUpFailure 记录一次密码验证失败(IP 与用户两个维度各记一次, 任一超限即锁)。
func StepUpFailure(clientIP string, userID uint) {
	keyIP, keyUser := stepUpKeys(clientIP, userID)
	LoginFailure(keyIP)
	LoginFailure(keyUser)
}

// StepUpSuccess 密码验证成功后清零该主体的失败记录。
func StepUpSuccess(clientIP string, userID uint) {
	keyIP, keyUser := stepUpKeys(clientIP, userID)
	LoginSuccess(keyIP)
	LoginSuccess(keyUser)
}
