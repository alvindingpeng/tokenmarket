package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 公开端点: 登录与注册; 注册默认由设置项关闭。
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/login", http.MethodPost).
				Handle(login),
		).
		AddRoute(
			router.NewRoute("/register", http.MethodPost).
				Handle(registerUser),
		).
		AddRoute(
			router.NewRoute("/register-reseller", http.MethodPost).
				Handle(registerReseller),
		).
		AddRoute(
			router.NewRoute("/email-code", http.MethodPost).
				Handle(sendEmailCode),
		).
		AddRoute(
			router.NewRoute("/forgot-password", http.MethodPost).
				Handle(forgotPassword),
		)
	router.NewGroupRouter("/api/v1/user").
		AddRoute(
			router.NewRoute("/register-config", http.MethodGet).
				Handle(registerConfig),
		)
	// 登录态端点: 本人账户操作。
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/change-password", http.MethodPost).
				Handle(changePassword),
		).
		AddRoute(
			router.NewRoute("/change-username", http.MethodPost).
				Handle(changeUsername),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(status),
		).
		AddRoute(
			router.NewRoute("/logout", http.MethodPost).
				Handle(logout),
		)
	// 管理后台: 用户管理仅管理员可用(含重置密码)。
	router.NewGroupRouter("/api/v1/user/manage").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listUsers),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(createUser),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(updateUser),
		).
		AddRoute(
			router.NewRoute("/reset-password", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(resetPassword),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(deleteUser),
		)
}

func login(c *gin.Context) {
	var user model.UserLogin
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	// 登录防爆破: 按 IP+账号 两个维度独立度量, 任一超限即拒绝, 避免凭据填充与 IP 绕行。
	keyIP := "ip:" + c.ClientIP()
	keyUser := "u:" + user.Username
	if retryAfter, ok := auth.LoginAllowed(keyIP); !ok {
		c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		resp.Error(c, http.StatusTooManyRequests, "login throttled, try again later")
		return
	}
	if retryAfter, ok := auth.LoginAllowed(keyUser); !ok {
		c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		resp.Error(c, http.StatusTooManyRequests, "login throttled, try again later")
		return
	}
	account, err := op.UserVerify(user.Username, user.Password)
	if err != nil {
		auth.LoginFailure(keyIP)
		auth.LoginFailure(keyUser)
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	auth.LoginSuccess(keyIP)
	auth.LoginSuccess(keyUser)
	// 维护模式: 开启后只放行管理员, 其余登录一律拒绝并返回维护提示。
	if maintenance, _ := op.SettingGetBool(model.SettingKeyMaintenanceMode); maintenance && account.Role != model.RoleAdmin {
		notice, _ := op.SettingGetString(model.SettingKeyMaintenanceNotice)
		if notice == "" {
			notice = "service under maintenance"
		}
		resp.Error(c, http.StatusServiceUnavailable, notice)
		return
	}
	token, maxAge, err := auth.GenerateJWTToken(account, user.Expire)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	// Secure 跟随实际协议: 上了 TLS 就只允许 HTTPS 回传, 隧道/内网 http 访问则保持可用。
	// 恒为 false 会让会话 cookie 在明文链路上也能被读走, 恒为 true 又会让 http 访问直接登不上。
	c.SetCookie("auth", token, maxAge, "/", "", requestIsHTTPS(c), true)
	resp.Success(c, account.View())
}

// logout 服务端登出: 自增 TokenVersion 使当前令牌立即失效 (同时也会清掉此账号其他端的登录态)。
// 若需要"只退本端不踢其他端", 客户端自行清 cookie 即可; 服务端登出是轻量安全手段而非 multi-tenant 方案。
func logout(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if err := op.UserBumpTokenVersion(userID); err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	c.SetCookie("auth", "", -1, "/", "", false, true)
	audit(c, "user.logout", "", "")
	resp.Success(c, "logged out")
}

// registerConfig 返回注册开关, 供登录页决定是否展示注册入口; 不泄露其他设置。
func registerConfig(c *gin.Context) {
	userEnabled, _ := op.SettingGetBool(model.SettingKeyRegisterUserEnabled)
	resellerEnabled, _ := op.SettingGetBool(model.SettingKeyRegisterResellerEnabled)
	// approval_required 决定注册成功后提示"请登录"还是"等待审批": 此前漏发, 前端恒走前者。
	approvalRequired, _ := op.SettingGetBool(model.SettingKeyRegisterApproval)
	// 站点文案(名称/描述/联系方式)不在这里下发: 那是 /api/v1/site/config 的职责,
	// 两处都发同一个值只会让前端出现两个数据来源。
	resp.Success(c, gin.H{
		"user_enabled":      userEnabled,
		"reseller_enabled":  resellerEnabled,
		"approval_required": approvalRequired,
	})
}

// registerUser 用户注册; 角色固定为 user, 注册开关默认关闭。
func registerUser(c *gin.Context) {
	register(c, model.RoleUser, model.SettingKeyRegisterUserEnabled)
}

// registerReseller 渠道商注册; 走独立端点, 不接受请求体自报角色, 防提权。
func registerReseller(c *gin.Context) {
	register(c, model.RoleReseller, model.SettingKeyRegisterResellerEnabled)
}

func register(c *gin.Context, role model.Role, switchKey model.SettingKey) {
	enabled, err := op.SettingGetBool(switchKey)
	if err != nil || !enabled {
		resp.Error(c, http.StatusForbidden, "registration is disabled")
		return
	}
	var req model.UserRegister
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	// 注册硬性要求: 密码输入两遍且不短于 8 位, 邮箱必填且验证码校验通过。
	if len(req.Password) < model.PasswordMinLen {
		resp.Error(c, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", model.PasswordMinLen))
		return
	}
	if req.Password != req.PasswordConfirm {
		resp.Error(c, http.StatusBadRequest, "passwords do not match")
		return
	}
	if !model.ValidEmail(req.Email) {
		resp.Error(c, http.StatusBadRequest, "invalid email")
		return
	}
	if err := op.EmailCodeVerify(c.Request.Context(), req.Email, model.EmailCodePurposeRegister, req.EmailCode); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	status := model.StatusActive
	if approvalRequired, _ := op.SettingGetBool(model.SettingKeyRegisterApproval); approvalRequired {
		status = model.StatusPending
	}
	account, err := op.UserCreate(req.Username, req.Email, req.Password, role, status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": account.ID, "username": account.Username, "status": account.Status})
}

// sendEmailCode 发送邮箱验证码(注册/找回密码共用); 按邮箱+IP 双维度限流防轰炸。
func sendEmailCode(c *gin.Context) {
	var req model.UserEmailCode
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.EmailCodeSend(c.Request.Context(), req.Email, req.Purpose, c.ClientIP()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "code sent")
}

// forgotPassword 找回密码: 邮箱验证码通过后重设密码, 旧会话全部吊销。
func forgotPassword(c *gin.Context) {
	var req model.UserForgotPassword
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if len(req.Password) < model.PasswordMinLen {
		resp.Error(c, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", model.PasswordMinLen))
		return
	}
	if req.Password != req.PasswordConfirm {
		resp.Error(c, http.StatusBadRequest, "passwords do not match")
		return
	}
	if err := op.EmailCodeVerify(c.Request.Context(), req.Email, model.EmailCodePurposeReset, req.EmailCode); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.UserForgotPassword(req.Email, req.Password); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "password reset successfully")
}

func changePassword(c *gin.Context) {
	var user model.UserChangePassword
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	userID, _ := middleware.CurrentUser(c)
	if err := op.UserChangePassword(userID, user.OldPassword, user.NewPassword); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "password changed successfully")
}

func changeUsername(c *gin.Context) {
	var user model.UserChangeUsername
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	userID, _ := middleware.CurrentUser(c)
	if err := op.UserChangeUsername(userID, user.NewUsername); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "username changed successfully")
}

// status 返回当前登录身份, 前端按角色裁剪导航与功能。
func status(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	account, err := op.UserGetByID(userID)
	if err != nil {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	resp.Success(c, account.View())
}

func listUsers(c *gin.Context) {
	resp.Success(c, op.UserList())
}

// createUser 管理员建号: 角色与状态均由请求指定。
func createUser(c *gin.Context) {
	var req model.UserManageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	status := req.Status
	if status == "" {
		status = model.StatusActive
	}
	account, err := op.UserCreate(req.Username, req.Email, req.Password, req.Role, status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "user.create", fmt.Sprintf("%s#%d", account.Username, account.ID), fmt.Sprintf("role=%s status=%s", account.Role, account.Status))
	resp.Success(c, account.View())
}

// updateUser 管理员修改账号: 用户名、邮箱、角色、状态、余额、密码按需更新, 留空即不改。
func updateUser(c *gin.Context) {
	var req model.UserManageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	account, err := op.UserGetByID(req.ID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	target := fmt.Sprintf("%s#%d", account.Username, account.ID)
	if req.Username != "" && req.Username != account.Username {
		old := account.Username
		if err := op.UserChangeUsername(req.ID, req.Username); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.update", target, fmt.Sprintf("username: %s -> %s", old, req.Username))
	}
	if req.Email != "" && req.Email != account.Email {
		old := account.Email
		if err := op.UserUpdateEmail(req.ID, req.Email); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.update", target, fmt.Sprintf("email: %s -> %s", old, req.Email))
	}
	if req.Role != "" && req.Role != account.Role {
		old := account.Role
		if err := op.UserUpdateRole(req.ID, req.Role); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.update", target, fmt.Sprintf("role: %s -> %s", old, req.Role))
	}
	if req.Status != "" && req.Status != account.Status {
		old := account.Status
		if err := op.UserUpdateStatus(req.ID, req.Status); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.update", target, fmt.Sprintf("status: %s -> %s", old, req.Status))
	}
	// 即使目标值等于缓存余额也交由资金层核对数据库, 避免缓存过期时跳过调账。
	if req.Balance != nil {
		old := account.Balance
		actorID, _ := middleware.CurrentUser(c)
		if err := op.UserSetBalance(req.ID, *req.Balance, actorID, "", "管理员调整"); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.balance", target, fmt.Sprintf("balance: %.6f -> %.6f", old, *req.Balance))
	}
	if req.Password != "" {
		if err := op.UserResetPassword(req.ID, req.Password); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "user.reset-password", target, "")
	}
	account, err = op.UserGetByID(req.ID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, account.View())
}

// resetPassword 管理员重置任意账号密码(渠道商无用户管理, 不存在此能力)。
func resetPassword(c *gin.Context) {
	var req model.UserManageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserResetPassword(req.ID, req.Password); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if account, err := op.UserGetByID(req.ID); err == nil {
		audit(c, "user.reset-password", fmt.Sprintf("%s#%d", account.Username, account.ID), "")
	}
	resp.Success(c, "password reset successfully")
}

func deleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	currentID, _ := middleware.CurrentUser(c)
	if uint(id) == currentID {
		resp.Error(c, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	target := strconv.Itoa(id)
	if account, err := op.UserGetByID(uint(id)); err == nil {
		target = fmt.Sprintf("%s#%d", account.Username, account.ID)
	}
	if err := op.UserDelete(uint(id)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "user.delete", target, "")
	resp.Success(c, nil)
}
