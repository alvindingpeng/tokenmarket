package middleware

import (
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// StepUpTokenHeader 高危操作二次验证(step-up)凭据的传递头。
// 放头不放 query/cookie: 头不会进访问日志与浏览器历史; cookie 会把凭据变成"自动携带"的隐式授权,
// 而 step-up 要的就是"每次高危操作都由本次交互明确授权"。
const StepUpTokenHeader = "X-Step-Up-Token"

// RequireStepUp 高危操作闸门: 挂在写路由上, 要求请求携带有效的 step-up 凭证。
// 必须排在 Auth() 之后(路由级中间件天然在组级之后): 用户身份与登录会话都来自 Auth 注入的上下文。
//
// 校验四项: 凭证签名与有效期、归属用户、令牌版本(改密/禁用即失效)、会话指纹(与本次登录绑定)。
// 任何一项不过都统一回 428 + step_up_required —— 不区分原因, 免得变成探测接口;
// 且一律在进入业务处理前拦下, 前端"确认密码后原样重发"因此不可能把同一笔交易执行两遍。
func RequireStepUp() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := CurrentUser(c)
		if userID == 0 {
			resp.StepUpRequired(c)
			c.Abort()
			return
		}
		sessionToken, err := c.Cookie("auth")
		if err != nil || sessionToken == "" {
			resp.StepUpRequired(c)
			c.Abort()
			return
		}
		user, err := op.UserGetByID(userID)
		if err != nil {
			resp.StepUpRequired(c)
			c.Abort()
			return
		}
		if err := auth.VerifyStepUpToken(c.GetHeader(StepUpTokenHeader), sessionToken, user); err != nil {
			resp.StepUpRequired(c)
			c.Abort()
			return
		}
		c.Next()
	}
}
