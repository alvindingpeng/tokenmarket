package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
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

// setTrustedDeviceCookie 下发信任设备令牌。
//
// HttpOnly 必开: 令牌是"免第二因子"的等价物, 让 JS 读得到等于把绕过 2FA 的能力交给任何一处 XSS。
// Secure 跟随实际协议(与 auth cookie 同一判据): 恒 true 会让内网 http 部署永远登不上,
// 恒 false 会让凭据在明文链路上裸奔。
func setTrustedDeviceCookie(c *gin.Context, token string, maxAge int) {
	c.SetCookie(model.TrustedDeviceCookieName, token, maxAge, "/", "", requestIsHTTPS(c), true)
}

// clearTrustedDeviceCookie 清掉浏览器侧的令牌(服务端记录已删或不该继续用)。
func clearTrustedDeviceCookie(c *gin.Context) {
	c.SetCookie(model.TrustedDeviceCookieName, "", -1, "/", "", false, true)
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
		).
		AddRoute(
			// 恢复码自助重生成: 门槛与解绑同档(step-up 由组中间件提供, 动态码/恢复码由 op 校验),
			// 因为它输出的是"能绕过第二因子的 10 把钥匙"。
			router.NewRoute("/2fa/recovery-codes/regenerate", http.MethodPost).
				Handle(twoFactorRegenerateRecovery),
		)

	// 信任设备清单单独成组: **只读**列表不该要求 step-up(用户点开设置页看一眼有几台设备,
	// 不该被弹一次密码确认), 但两个吊销操作必须 step-up —— 它们能砍掉"免码"旁路, 属于安全写。
	router.NewGroupRouter("/api/v1/user").
		Use(twoFactorNoStore).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/2fa/trusted-devices", http.MethodGet).
				Handle(twoFactorTrustedDevices),
		).
		AddRoute(
			router.NewRoute("/2fa/trusted-devices", http.MethodDelete).
				Use(middleware.RequireStepUp()).
				Handle(twoFactorTrustedDeviceRevokeAll),
		).
		AddRoute(
			router.NewRoute("/2fa/trusted-devices/:id", http.MethodDelete).
				Use(middleware.RequireStepUp()).
				Handle(twoFactorTrustedDeviceRevoke),
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

// twoFactorRegenerateRecovery 重新签发 10 个恢复码, 旧的立即全部失效。
// 明文只在本次响应里出现一次; 审计只记"重新生成过", 绝不记码。
func twoFactorRegenerateRecovery(c *gin.Context) {
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
	codes, err := op.UserTOTPRegenerateRecovery(userID, req.Code)
	if err != nil {
		twoFactorError(c, err)
		return
	}
	audit(c, "user.totp-recovery-regenerate", "", "regenerated")
	resp.Success(c, gin.H{"recovery_codes": codes})
}

// twoFactorTrustedDevices 列出仍在有效期内、能免输动态码的设备。
// 过期记录不返回(见 op.TrustedDeviceList): 列表里混着"早已失效"的条目会让人误判还有旁路可用。
func twoFactorTrustedDevices(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	devices, err := op.TrustedDeviceList(userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	now := time.Now()
	items := make([]model.TrustedDeviceView, 0, len(devices))
	for _, device := range devices {
		items = append(items, device.View(now))
	}
	resp.Success(c, gin.H{"items": items})
}

// twoFactorTrustedDeviceRevoke 吊销单台设备。id 来自路径, 但删除条件同时带 user_id ——
// 只按 id 删会让任何登录用户吊销别人的设备(越权写)。
func twoFactorTrustedDeviceRevoke(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	deviceID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || deviceID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	revoked, err := op.TrustedDeviceRevoke(userID, uint(deviceID))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	audit(c, "user.totp-trusted-device-revoke", "", "revoked")
	resp.Success(c, gin.H{"revoked": revoked})
}

// twoFactorTrustedDeviceRevokeAll 一刀切吊销(换机、怀疑被入侵时的动作)。
// Cookie 一并清掉: 服务端删了记录之后那张 Cookie 已经无效, 但留着会让用户以为还处于信任状态。
func twoFactorTrustedDeviceRevokeAll(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	revoked, err := op.TrustedDeviceRevokeAll(userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	audit(c, "user.totp-trusted-device-revoke-all", "", "revoked")
	clearTrustedDeviceCookie(c)
	resp.Success(c, gin.H{"revoked": revoked})
}
