package middleware

import (
	"net/http"
	"strconv"
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

// CurrentUserScope 从上下文构建包含租户信息的作用域。
func CurrentUserScope(c *gin.Context) model.Scope {
	userID, role := CurrentUser(c)
	tenantID := uint(c.GetInt("tenant_id"))
	return model.Scope{ID: userID, Role: role, TenantID: tenantID}
}

// tenantOverrideHeader 是管理员切换租户视图时携带的目标租户头。
// 只有平台管理员(admin)可用: 普通用户固定为本人租户, 带上这个头也不生效 ——
// 否则任何用户都能靠一个请求头把自己的数据作用域改到别人的租户。
const tenantOverrideHeader = "X-Tenant-ID"

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
		c.Set("tenant_id", int(user.TenantID))

		// 租户状态闸门: disabled/pending 的租户下所有用户一律拒绝。
		// 挂在认证内部而非业务层, 是让"租户被停服"这件事对全部端点一致生效 ——
		// 否则每个 handler 都要各自记得判断, 漏一个就是停服租户还能继续调用的口子。
		if tenantID := user.TenantID; tenantID > 0 {
			if usable, _ := op.TenantUsable(tenantID); !usable {
				c.SetCookie("auth", "", -1, "/", "", false, true)
				resp.Error(c, http.StatusForbidden, "tenant is disabled")
				c.Abort()
				return
			}
		}

		// 管理员显式切换租户视图: 用请求头覆盖租户上下文。
		// 只在管理员上生效, 且覆盖后的租户必须真实存在且可用 —— 前端切租户后
		// 业务请求据此落到目标租户的数据作用域, 而不是继续读本人租户的数据。
		if user.Role == model.RoleAdmin {
			if raw := strings.TrimSpace(c.GetHeader(tenantOverrideHeader)); raw != "" {
				if targetID, convErr := strconv.ParseUint(raw, 10, 32); convErr == nil && targetID > 0 {
					if usable, _ := op.TenantUsable(uint(targetID)); !usable {
						resp.Error(c, http.StatusForbidden, "tenant is disabled")
						c.Abort()
						return
					}
					c.Set("tenant_id", int(targetID))
					// 标记为跨租户覆盖, 供业务层区分"管理员本人租户"与"正在查看的租户"。
					c.Set("tenant_override", true)
				}
			}
		}
		// 默认管理员改密闸门: 初始 admin/admin 未改前, 除改密/登出/身份查询外的请求一律拒绝。
		// 攻击窗口是"部署后没人改密"的那段时间 —— 初始凭据写在文档里, 等于公开后门。
		// 挂在 Auth 内部而不是全局: 角色在此刻已定, 且登录/注册等公开端点不受影响。
		// 只认「默认账号 admin」本身: 不能按 role==admin 判, 否则新建的其他管理员也会被
		// 误拦(摩擦), 而全局标记又被任意管理员改密翻转, 反而放走仍持默认口令的 admin 账号。
		if user.Role == model.RoleAdmin && user.Username == "admin" && !op.AdminDefaultPasswordChanged() && !passwordChangeExemptPaths[c.Request.URL.Path] {
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

		// 常规凭据按指纹查; 重放自调用用签名凭据(绑定 key ID 与过期时间), 由 op 统一解析。
		apiKeyObj, err := op.APIKeyResolveCredential(apiKey, c.Request.Context())
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
		memoryCost := statsAPIKey.StatsMetrics.OutputCost + statsAPIKey.StatsMetrics.InputCost
		// max_cost 判定用有效口径: max(内存实时, billing_records 聚合兜底)。
		// 只信内存会在崩溃丢缓存后让超支 Key 复活; 兜底聚合让数据库里已结算的账目不被遗忘。
		if apiKeyObj.MaxCost > 0 && apiKeyObj.MaxCost < op.APIKeyCostEffective(c.Request.Context(), apiKeyObj.ID, memoryCost) {
			resp.Error(c, http.StatusUnauthorized, "API key has reached the max cost")
			c.Abort()
			return
		}
		// 租户状态闸门同样作用在 API Key 路径上: 租户被停服后, 它名下的钥匙
		// 也必须立即失效 —— 只拦登录态会让"停服"变成一道可以靠 API Key 绕过的栅栏。
		if apiKeyObj.TenantID > 0 {
			if usable, _ := op.TenantUsable(apiKeyObj.TenantID); !usable {
				resp.Error(c, http.StatusForbidden, "tenant is disabled")
				c.Abort()
				return
			}
		}

		c.Set("supported_models", apiKeyObj.SupportedModels)
		c.Set("routing_preference", apiKeyObj.RoutingPreference)
		c.Set("api_key_id", apiKeyObj.ID)
		c.Set("api_key_user_id", int(apiKeyObj.UserID))
		c.Set("api_key_tenant_id", int(apiKeyObj.TenantID))
		c.Next()
	}
}
