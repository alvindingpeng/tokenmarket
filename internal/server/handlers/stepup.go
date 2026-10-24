package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 高危操作二次验证(step-up)端点: 已登录用户用"重新输入密码"换取一枚短期凭证,
// 之后的高危写操作(余额调整/提现审批/备份导入/敏感导出/结算生成/计费恢复/系统设置/用户管理)
// 带上凭证才放行。密码是唯一前提, 不假设部署了 TOTP 之类的第二因素。
func init() {
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			// 本端点自身不需要 step-up: 它就是"换取 step-up 凭证"的入口。
			// 默认管理员改密闸门同样对它生效(不在豁免名单里), 未改初始密码前拿不到任何高危通道。
			router.NewRoute("/step-up", http.MethodPost).
				Handle(stepUp),
		)
}

// userStepUpRequest 重新认证请求; 只收密码, 身份以登录态为准, 不接受请求体自报用户。
type userStepUpRequest struct {
	Password string `json:"password"`
}

// stepUp 验证密码后签发短期 step-up 凭证。
//
// 安全口径:
//   - 限流: 密码验证与登录同一套防爆破闸门(独立计数主体), 超限 429 + Retry-After;
//   - 凭证绑定 用户 + token_version + 当前登录会话: 改密/登出/换会话任一发生即失效, 详见 auth.IssueStepUpToken;
//   - 审计: 记录谁在什么时候完成了/尝试了二次验证, 明细里绝不写密码或凭证本身。
func stepUp(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req userStepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	userID, _ := middleware.CurrentUser(c)
	if retryAfter, ok := auth.StepUpAllowed(c.ClientIP(), userID); !ok {
		c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		resp.Error(c, http.StatusTooManyRequests, "password verification throttled, try again later")
		return
	}
	account, err := op.UserGetByID(userID)
	if err != nil {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	target := fmt.Sprintf("%s#%d", account.Username, account.ID)
	sessionToken, err := c.Cookie("auth")
	if err != nil || sessionToken == "" {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	if err := account.ComparePassword(req.Password); err != nil {
		// 失败也留痕: 这是"有人在高危操作前反复试密码"的唯一记录点。
		// 只记结论, 不碰密码内容。
		auth.StepUpFailure(c.ClientIP(), userID)
		audit(c, "user.step-up", target, "password rejected")
		resp.Error(c, http.StatusUnauthorized, "password verification failed")
		return
	}
	auth.StepUpSuccess(c.ClientIP(), userID)
	token, expiresIn, err := auth.IssueStepUpToken(account, sessionToken)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	// 审计里不写凭证: 凭证等于高危操作的临时通行证, 进了日志就等于公开。
	audit(c, "user.step-up", target, fmt.Sprintf("password confirmed, high-risk operations unlocked for %ds", expiresIn))
	resp.Success(c, gin.H{"token": token, "expires_in": expiresIn})
}

// 高危操作覆盖面: 受 RequireStepUp 保护的路由在各自 handler 的注册处显式标注,
// 读列表类端点一律不挂, 避免"看一眼数据也要输密码"。
