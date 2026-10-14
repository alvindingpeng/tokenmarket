package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 兑换码接口: 用户侧兑换与记录, 管理侧批次生成与码管理。
func init() {
	router.NewGroupRouter("/api/v1/redemption").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/redeem", http.MethodPost).
				Handle(redeemCode),
		).
		AddRoute(
			router.NewRoute("/history", http.MethodGet).
				Handle(redemptionHistory),
		).
		AddRoute(
			router.NewRoute("/batch/create", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(createRedemptionBatch),
		).
		AddRoute(
			router.NewRoute("/batch/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listRedemptionBatches),
		).
		AddRoute(
			router.NewRoute("/code/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listRedemptionCodes),
		).
		AddRoute(
			router.NewRoute("/code/disable", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(disableRedemptionCode),
		)
}

// redeemCode 兑换兑换码入账; 连续错码 5 次锁 10 分钟(按用户与来源 IP 双计)。
func redeemCode(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	entry, err := op.RedemptionRedeem(context.Background(), userID, req.Code, c.ClientIP())
	if err != nil {
		if errors.Is(err, op.ErrRedeemLocked) {
			resp.Error(c, http.StatusTooManyRequests, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "redemption.redeem", entry.Code, fmt.Sprintf("credits=%.6f", entry.Credits))
	user, _ := op.UserGetByID(userID)
	resp.Success(c, gin.H{"credits": entry.Credits, "balance": user.Balance})
}

// redemptionHistory 本人兑换记录。
func redemptionHistory(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := op.RedemptionHistory(context.Background(), userID, limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// createRedemptionBatch 生成兑换码批次; 返回全部新码供管理员复制或导出。
func createRedemptionBatch(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		Name         string  `json:"name"`
		Credits      float64 `json:"credits"`
		Count        int     `json:"count"`
		Prefix       string  `json:"prefix"`
		PerUserLimit int     `json:"per_user_limit"`
		ExpiresAt    *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	spec := op.RedemptionBatchSpec{
		Name:         req.Name,
		Credits:      req.Credits,
		Count:        req.Count,
		Prefix:       req.Prefix,
		PerUserLimit: req.PerUserLimit,
		CreatedBy:    userID,
	}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		spec.ExpiresAt = &expires
	}
	batch, codes, err := op.RedemptionBatchCreate(context.Background(), spec)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "redemption.generate", fmt.Sprintf("batch:%d", batch.ID),
		fmt.Sprintf("name=%s credits=%.6f count=%d", batch.Name, batch.Credits, batch.Count))
	resp.Success(c, gin.H{"batch": batch, "codes": codes})
}

// listRedemptionBatches 批次列表(含各状态张数)。
func listRedemptionBatches(c *gin.Context) {
	rows, err := op.RedemptionBatchList(context.Background())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// listRedemptionCodes 兑换码分页列表。
func listRedemptionCodes(c *gin.Context) {
	batchID, _ := strconv.Atoi(c.Query("batch_id"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, total, err := op.RedemptionCodeList(context.Background(), op.RedemptionCodeQuery{
		BatchID: uint(batchID),
		Status:  c.Query("status"),
		Keyword: c.Query("keyword"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows, "total": total})
}

// disableRedemptionCode 禁用未兑兑换码。
func disableRedemptionCode(c *gin.Context) {
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.RedemptionCodeDisable(context.Background(), req.ID); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "redemption.disable", fmt.Sprintf("code:%d", req.ID), "")
	resp.Success(c, gin.H{"id": req.ID})
}
