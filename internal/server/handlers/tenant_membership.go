package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// adminTenantID 解析正整数租户 ID, 避免 0/负数在 DB 层被解释成默认作用域。
func adminTenantID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 32)
	if err != nil || id == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return 0, false
	}
	return uint(id), true
}

func init() {
	// 独立路由组, 不改 Lead 正在编辑的 tenant.go。
	router.NewGroupRouter("/api/v1/admin/tenant").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/:id/members", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listAdminTenantMembers),
		).
		AddRoute(
			router.NewRoute("/:id/members", http.MethodPost).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(addAdminTenantMember),
		).
		AddRoute(
			router.NewRoute("/:id/members/:uid", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(removeAdminTenantMember),
		)

	router.NewGroupRouter("/api/v1/admin/user").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/:id/tenant", http.MethodPut).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(moveAdminUserTenant),
		)
}

func listAdminTenantMembers(c *gin.Context) {
	tenantID, ok := adminTenantID(c)
	if !ok {
		return
	}
	members, err := op.TenantListMembers(tenantID)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, members)
}

type addTenantMemberRequest struct {
	UserID uint   `json:"user_id" binding:"required,gt=0"`
	Role   string `json:"role" binding:"required,oneof=member admin owner"`
}

func addAdminTenantMember(c *gin.Context) {
	tenantID, ok := adminTenantID(c)
	if !ok {
		return
	}
	var req addTenantMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.TenantAddMember(req.UserID, tenantID, req.Role); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.member.add", fmt.Sprintf("tenant#%d/user#%d", tenantID, req.UserID), "role="+req.Role)
	resp.Success(c, gin.H{"user_id": req.UserID, "tenant_id": tenantID, "role": req.Role})
}

func removeAdminTenantMember(c *gin.Context) {
	tenantID, ok := adminTenantID(c)
	if !ok {
		return
	}
	userID, err := strconv.ParseUint(strings.TrimSpace(c.Param("uid")), 10, 32)
	if err != nil || userID == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := op.TenantRemoveMember(uint(userID), tenantID); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.member.remove", fmt.Sprintf("tenant#%d/user#%d", tenantID, userID), "")
	resp.Success(c, gin.H{"user_id": userID, "tenant_id": tenantID, "removed": true})
}

type moveUserTenantRequest struct {
	TenantID uint `json:"tenant_id" binding:"required,gt=0"`
}

func moveAdminUserTenant(c *gin.Context) {
	userID, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 32)
	if err != nil || userID == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid user id")
		return
	}
	var req moveUserTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	previous, err := op.TenantMoveUser(uint(userID), req.TenantID)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// TenantMoveUser 已在事务提交后调用 UserTouchBalance 刷新用户缓存, 否则认证读到的还是旧 tenant_id。
	// 迁租改的是租户归属, 不改租户状态, 因此不需要 TenantStatusInvalidate。
	audit(c, "user.tenant.move", fmt.Sprintf("user#%d", userID), fmt.Sprintf("from=%d to=%d", previous, req.TenantID))
	resp.Success(c, gin.H{"user_id": userID, "previous_tenant_id": previous, "tenant_id": req.TenantID})
}
