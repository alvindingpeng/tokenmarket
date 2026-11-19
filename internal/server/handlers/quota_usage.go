package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 用户自助: 只能看自己的配额用量。
	router.NewGroupRouter("/api/v1/quota-usage").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).Handle(getSelfQuotaUsage),
		)
	// 管理员: 可查任意用户, 供客服排查"为什么他被限流"。
	router.NewGroupRouter("/api/v1/ops/quota-usage").
		Use(middleware.Auth(), middleware.RequireRole(model.RoleAdmin)).
		AddRoute(
			router.NewRoute("", http.MethodGet).Handle(getUserQuotaUsageByAdmin),
		)
}

// getSelfQuotaUsage 当前登录用户的配额与用量。
func getSelfQuotaUsage(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, "未登录")
		return
	}
	serveQuotaUsage(c, userID)
}

// getUserQuotaUsageByAdmin 管理员查询指定用户的配额与用量。
func getUserQuotaUsageByAdmin(c *gin.Context) {
	var query struct {
		UserID uint   `form:"user_id" binding:"required"`
		Model  string `form:"model"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	serveQuotaUsage(c, query.UserID)
}

// serveQuotaUsage 两个入口共用同一查询与其错误映射。
func serveQuotaUsage(c *gin.Context, userID uint) {
	modelName := c.Query("model")
	usage, err := op.UserQuotaUsage(c.Request.Context(), userID, modelName)
	if err != nil {
		// 查询失败必须报错: 返回全零会被读成"额度充足", 正好掩盖真正的问题。
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	rpm, tpm, concurrent := usage.QuotaRemain()
	resp.Success(c, gin.H{
		"usage":             usage,
		"remain_rpm":        rpm,
		"remain_tpm":        tpm,
		"remain_concurrent": concurrent,
	})
}
