package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
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
	account, err := op.UserVerify(user.Username, user.Password)
	if err != nil {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
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
	c.SetCookie("auth", token, maxAge, "/", "", false, false)
	resp.Success(c, account.View())
}

// registerConfig 返回注册开关, 供登录页决定是否展示注册入口; 不泄露其他设置。
func registerConfig(c *gin.Context) {
	userEnabled, _ := op.SettingGetBool(model.SettingKeyRegisterUserEnabled)
	resellerEnabled, _ := op.SettingGetBool(model.SettingKeyRegisterResellerEnabled)
	siteName, _ := op.SettingGetString(model.SettingKeySiteName)
	siteDescription, _ := op.SettingGetString(model.SettingKeySiteDescription)
	siteContact, _ := op.SettingGetString(model.SettingKeySiteContact)
	resp.Success(c, gin.H{
		"user_enabled":     userEnabled,
		"reseller_enabled": resellerEnabled,
		// 站点信息: 登录页与关于处展示, 留空则由前端回退默认文案。
		"site_name":        siteName,
		"site_description": siteDescription,
		"site_contact":     siteContact,
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
	status := model.StatusActive
	if approvalRequired, _ := op.SettingGetBool(model.SettingKeyRegisterApproval); approvalRequired {
		status = model.StatusPending
	}
	account, err := op.UserCreate(req.Username, req.Password, role, status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": account.ID, "username": account.Username, "status": account.Status})
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
	account, err := op.UserCreate(req.Username, req.Password, req.Role, status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "user.create", fmt.Sprintf("%s#%d", account.Username, account.ID), fmt.Sprintf("role=%s status=%s", account.Role, account.Status))
	resp.Success(c, account.View())
}

// updateUser 管理员修改账号: 用户名、角色、状态、余额、密码按需更新, 留空即不改。
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
	if req.Balance != nil && *req.Balance != account.Balance {
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
