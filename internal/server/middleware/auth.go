package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// CurrentUser 从上下文取已认证用户, 未认证时返回零值。
func CurrentUser(c *gin.Context) (uint, model.Role) {
	return uint(c.GetInt("user_id")), model.Role(c.GetString("role"))
}

// Auth 登录态中间件: 校验 JWT 并把身份注入上下文, 同时校验账号状态与令牌版本。
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("auth")
		if err != nil || token == "" {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		claims, err := auth.VerifyJWTToken(token)
		if err != nil {
			c.SetCookie("auth", "", -1, "/", "", false, true)
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		user, err := op.UserGetByID(claims.UserID)
		if err != nil || user.Status != model.StatusActive || user.TokenVersion != claims.TokenVersion {
			c.SetCookie("auth", "", -1, "/", "", false, true)
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		// 角色以库内为准: 令牌里的角色只是快照, 改角色后旧令牌立即按新角色受限。
		c.Set("user_id", int(user.ID))
		c.Set("role", string(user.Role))
		c.Set("username", user.Username)
		// 默认管理员改密闸门: 初始 admin/admin 未改前, 除改密/登出/身份查询外的请求一律拒绝。
		// 攻击窗口是"部署后没人改密"的那段时间 —— 初始凭据写在文档里, 等于公开后门。
		// 挂在 Auth 内部而不是全局: 角色在此刻已定, 且登录/注册等公开端点不受影响。
		if user.Role == model.RoleAdmin && !op.AdminDefaultPasswordChanged() && !passwordChangeExemptPaths[c.Request.URL.Path] {
			resp.Error(c, http.StatusForbidden, "change the default admin password first")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireRole 角色白名单中间件; 由路由注册器按 Route.Roles 自动挂载。
func RequireRole(roles ...model.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := model.Role(c.GetString("role"))
		for _, allowed := range roles {
			if role == allowed {
				c.Next()
				return
			}
		}
		resp.Error(c, http.StatusForbidden, "insufficient permissions")
		c.Abort()
	}
}

// passwordChangeExemptPaths 默认管理员未改密时仍可访问的端点: 改密本身, 登出与身份查询。
var passwordChangeExemptPaths = map[string]bool{
	"/api/v1/user/change-password": true,
	"/api/v1/user/logout":          true,
	"/api/v1/user/status":          true,
}

func APIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		var apiKey string

		if key := c.Request.Header.Get("x-api-key"); key != "" {
			apiKey = key
		} else if authorization := c.Request.Header.Get("Authorization"); authorization != "" {
			apiKey = strings.TrimPrefix(authorization, "Bearer ")
		}

		if apiKey == "" {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}

		apiKeyObj, err := op.APIKeyGetByAPIKey(apiKey, c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		if !apiKeyObj.Enabled {
			resp.Error(c, http.StatusUnauthorized, "API key is disabled")
			c.Abort()
			return
		}
		if apiKeyObj.ExpireAt > 0 && apiKeyObj.ExpireAt < time.Now().Unix() {
			resp.Error(c, http.StatusUnauthorized, "API key has expired")
			c.Abort()
			return
		}
		statsAPIKey := op.StatsAPIKeyGet(apiKeyObj.ID)
		if apiKeyObj.MaxCost > 0 && apiKeyObj.MaxCost < statsAPIKey.StatsMetrics.OutputCost+statsAPIKey.StatsMetrics.InputCost {
			resp.Error(c, http.StatusUnauthorized, "API key has reached the max cost")
			c.Abort()
			return
		}
		c.Set("supported_models", apiKeyObj.SupportedModels)
		c.Set("routing_preference", apiKeyObj.RoutingPreference)
		c.Set("api_key_id", apiKeyObj.ID)
		c.Set("api_key_user_id", int(apiKeyObj.UserID))
		c.Next()
	}
}
