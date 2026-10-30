package handlers

import (
	"errors"
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// Webhook 出站订阅接口(P0-5)。全部要求登录态, 并按 user_id 隔离:
// 不是你的资源一律 404(不区分"不存在"与"无权"), 响应永不包含密钥(只有 has_secret)。
func init() {
	router.NewGroupRouter("/api/v1/webhook").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(webhookCreate),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(webhookList),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(webhookUpdate),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodDelete).
				Handle(webhookDelete),
		).
		AddRoute(
			router.NewRoute("/:id/test", http.MethodPost).
				Handle(webhookTest),
		).
		AddRoute(
			router.NewRoute("/:id/logs", http.MethodGet).
				Handle(webhookLogs),
		)
}

// webhookRespondError 把 op 层错误映射成状态码 + 机器可读 error_code。
// 未知错误只回通用文案: 内部细节(DB 错误串等)不外泄。
func webhookRespondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, op.ErrWebhookNotFound):
		resp.ErrorWithCode(c, http.StatusNotFound, resp.ErrResourceNotFound, resp.CodeWebhookNotFound)
	case errors.Is(err, op.ErrWebhookURLInvalid):
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid webhook url: https and port 443 only, no private addresses", resp.CodeInvalidWebhookURL)
	case errors.Is(err, op.ErrWebhookEventsInvalid):
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid webhook events", resp.CodeInvalidWebhookEvents)
	case errors.Is(err, op.ErrWebhookSecretInvalid):
		resp.ErrorWithCode(c, http.StatusBadRequest, "webhook secret must be 16-128 bytes", resp.CodeInvalidWebhookSecret)
	case errors.Is(err, op.ErrWebhookRetryInvalid):
		resp.ErrorWithCode(c, http.StatusBadRequest, "retry_count must be 0-3", resp.CodeInvalidRequest)
	case errors.Is(err, op.ErrWebhookQuota):
		resp.ErrorWithCode(c, http.StatusConflict, "webhook quota exceeded (10 per user)", resp.CodeWebhookQuotaExceeded)
	case errors.Is(err, op.ErrWebhookTestRateLimited):
		resp.ErrorWithCode(c, http.StatusTooManyRequests, "test webhook at most once per 60 seconds", resp.CodeWebhookTestRateLimited)
	default:
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
	}
}

// webhookBindID 解析路径参数 :id。
func webhookBindID(c *gin.Context) (uint64, bool) {
	var req struct {
		ID uint64 `uri:"id" binding:"required,min=1"`
	}
	if err := c.ShouldBindUri(&req); err != nil {
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid webhook id", resp.CodeInvalidRequest)
		return 0, false
	}
	return req.ID, true
}

// webhookCreate POST /api/v1/webhook
func webhookCreate(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req model.WebhookCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid request: "+err.Error(), resp.CodeInvalidRequest)
		return
	}
	view, err := op.WebhookCreate(c.Request.Context(), userID, req)
	if err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, view)
}

// webhookList GET /api/v1/webhook/list?page&page_size
func webhookList(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req model.WebhookListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid request: "+err.Error(), resp.CodeInvalidRequest)
		return
	}
	page, err := op.WebhookList(c.Request.Context(), userID, req)
	if err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, page)
}

// webhookUpdate PUT /api/v1/webhook/:id
func webhookUpdate(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	id, ok := webhookBindID(c)
	if !ok {
		return
	}
	var req model.WebhookUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid request: "+err.Error(), resp.CodeInvalidRequest)
		return
	}
	view, err := op.WebhookUpdate(c.Request.Context(), userID, id, req)
	if err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, view)
}

// webhookDelete DELETE /api/v1/webhook/:id
func webhookDelete(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	id, ok := webhookBindID(c)
	if !ok {
		return
	}
	if err := op.WebhookDelete(c.Request.Context(), userID, id); err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, gin.H{"deleted": id})
}

// webhookTest POST /api/v1/webhook/:id/test —— 只代表入队, 不代表远端已收到。
func webhookTest(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	id, ok := webhookBindID(c)
	if !ok {
		return
	}
	result, err := op.WebhookTest(c.Request.Context(), userID, id)
	if err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, result)
}

// webhookLogs GET /api/v1/webhook/:id/logs?page&page_size
func webhookLogs(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	id, ok := webhookBindID(c)
	if !ok {
		return
	}
	var req model.WebhookListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.ErrorWithCode(c, http.StatusBadRequest, "invalid request: "+err.Error(), resp.CodeInvalidRequest)
		return
	}
	page, err := op.WebhookLogList(c.Request.Context(), userID, id, req)
	if err != nil {
		webhookRespondError(c, err)
		return
	}
	resp.Success(c, page)
}
