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
	router.NewGroupRouter("/api/v1/notification").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(notificationList),
		).
		AddRoute(
			router.NewRoute("/unread-count", http.MethodGet).
				Handle(notificationUnreadCount),
		).
		AddRoute(
			router.NewRoute("/mark-read", http.MethodPost).
				Handle(notificationMarkRead),
		).
		AddRoute(
			router.NewRoute("/mark-all-read", http.MethodPost).
				Handle(notificationMarkAllRead),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodDelete).
				Handle(notificationDelete),
		)
}

// notificationList 获取通知列表
func notificationList(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req model.NotificationListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	notifications, total, err := op.NotificationList(c.Request.Context(), userID, req)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	resp.Success(c, gin.H{
		"items":     notifications,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// notificationUnreadCount 获取未读数量
func notificationUnreadCount(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	count, err := op.NotificationUnreadCount(c.Request.Context(), userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, model.NotificationUnreadCountResponse{UnreadCount: count})
}

// notificationMarkRead 标记通知为已读
func notificationMarkRead(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req model.NotificationMarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	if err := op.NotificationMarkRead(c.Request.Context(), userID, req.IDs); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"marked": len(req.IDs)})
}

// notificationMarkAllRead 标记所有通知为已读
func notificationMarkAllRead(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if err := op.NotificationMarkAllRead(c.Request.Context(), userID); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"message": "all notifications marked as read"})
}

// notificationDelete 删除通知
func notificationDelete(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		ID uint64 `uri:"id" binding:"required,min=1"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid notification id")
		return
	}

	if err := op.NotificationDelete(c.Request.Context(), userID, req.ID); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"deleted": req.ID})
}
