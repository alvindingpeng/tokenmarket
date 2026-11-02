package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// TOTP 双因素认证端点。安全口径:
//
// setup   登录态 + step-up —— 生成待确认密钥，不改变登录要求;
// enable  登录态 + step-up(重输密码) + 有效动态码 —— 启用是"锁人"方向的高危写,
//
//	不加 step-up 的话, 会话被劫持即可换上攻击者自己的认证器把号主锁死;
//
// disable 登录态 + step-up + 有效动态码或一次性恢复码。
func twoFactorNoStore(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }

func twoFactorError(c *gin.Context, err error) {
	if err == op.ErrTOTPThrottled {
		c.Header("Retry-After", "300")
		resp.Error(c, http.StatusTooManyRequests, err.Error())
		return
	}
	resp.Error(c, http.StatusBadRequest, err.Error())
}

func init() {
	router.NewGroupRouter("/api/v1/user").
		Use(twoFactorNoStore).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		Use(middleware.RequireStepUp()).
		AddRoute(
			router.NewRoute("/2fa/setup", http.MethodPost).
				Handle(twoFactorSetup),
		).
		AddRoute(
			router.NewRoute("/2fa/enable", http.MethodPost).
				Handle(twoFactorEnable),
		).
		AddRoute(
			router.NewRoute("/2fa/disable", http.MethodPost).
				Handle(twoFactorDisable),
		)
}

// twoFactorCodeRequest 只收动态码; 身份以登录态为准, 不接受请求体自报用户。
type twoFactorCodeRequest struct {
	Code string `json:"code"`
}

// twoFactorSetup 生成并落库待启用的 TOTP 密钥, 返回明文密钥与 otpauth 链接供前端渲染二维码。
// 密钥只在这一步下发一次; 启用后不再可读, 丢失设备只能解绑重绑。
func twoFactorSetup(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	secret, otpauthURL, err := op.UserTOTPSetup(userID)
	if err != nil {
		twoFactorError(c, err)
		return
	}
	resp.Success(c, gin.H{
		"secret":      secret,
		"otpauth_url": otpauthURL,
	})
}

// twoFactorEnable 校验动态码后正式启用 2FA。
func twoFactorEnable(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	var req twoFactorCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	codes, err := op.UserTOTPEnable(userID, req.Code)
	if err != nil {
		twoFactorError(c, err)
		return
	}
	// 审计只记结论, 绝不写密钥或动态码。
	audit(c, "user.totp-enable", "", "enabled")
	c.SetCookie("auth", "", -1, "/", "", requestIsHTTPS(c), true)
	resp.Success(c, gin.H{"recovery_codes": codes})
}

// twoFactorDisable 校验动态码后解绑 2FA 并清除密钥。
func twoFactorDisable(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	var req twoFactorCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserTOTPDisable(userID, req.Code); err != nil {
		twoFactorError(c, err)
		return
	}
	audit(c, "user.totp-disable", "", "disabled")
	c.SetCookie("auth", "", -1, "/", "", requestIsHTTPS(c), true)
	resp.Success(c, "two-factor authentication disabled")
}
