package handlers

import (
	"fmt"
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 渠道准入审核(P1): 提交/队列对管理员与渠道商开放, 批准/驳回仅管理员。
	// 列表按作用域过滤(管理员全量, 渠道商只看自有), op 层已裁剪, handler 只做参数与审计。
	router.NewGroupRouter("/api/v1/channel/review").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/submit", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(submitChannelReview),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listChannelReviews),
		).
		AddRoute(
			router.NewRoute("/approve", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(approveChannelReview),
		).
		AddRoute(
			router.NewRoute("/reject", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(rejectChannelReview),
		)
}

// channelReviewRequest 是提交/批准/驳回的共同形状: 驳回原因走 note, 批准意见可留空。
type channelReviewRequest struct {
	ID   int    `json:"id"`
	Note string `json:"note"`
}

// submitChannelReview 提交审核: draft|rejected|stale → pending; 管理员免审渠道提交即通过。
func submitChannelReview(c *gin.Context) {
	var request channelReviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	channel, err := op.ChannelReviewSubmit(request.ID, request.Note, scopeOf(c), c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.review_submit", fmt.Sprintf("channel#%d", channel.ID), fmt.Sprintf("status=%s note=%s", channel.ReviewStatus, channel.ReviewNote))
	resp.Success(c, gin.H{"id": channel.ID, "review_status": channel.ReviewStatus})
}

// listChannelReviews 返回审核队列; status 过滤可选, 空则给全部状态。
func listChannelReviews(c *gin.Context) {
	items := op.ChannelReviewList(c.Query("status"), scopeOf(c))
	resp.Success(c, gin.H{"items": items})
}

// approveChannelReview 批准审核: pending → approved 并写入配置指纹作复审基线。
func approveChannelReview(c *gin.Context) {
	var request channelReviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	scope := scopeOf(c)
	channel, err := op.ChannelReviewApprove(request.ID, request.Note, scope.ID, scope, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.review_approve", fmt.Sprintf("channel#%d", channel.ID), fmt.Sprintf("note=%s", channel.ReviewNote))
	resp.Success(c, gin.H{"id": channel.ID, "review_status": channel.ReviewStatus})
}

// rejectChannelReview 驳回审核: pending → rejected, 原因必填(op 层校验)。
func rejectChannelReview(c *gin.Context) {
	var request channelReviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	scope := scopeOf(c)
	channel, err := op.ChannelReviewReject(request.ID, request.Note, scope.ID, scope, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.review_reject", fmt.Sprintf("channel#%d", channel.ID), fmt.Sprintf("note=%s", channel.ReviewNote))
	resp.Success(c, gin.H{"id": channel.ID, "review_status": channel.ReviewStatus})
}
