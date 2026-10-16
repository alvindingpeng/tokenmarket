package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 提现接口: 申请/查询/取消对渠道商, 审核/打款/回滚对管理员; 普通用户整个提现功能屏蔽(路由级403, 界面隐藏入口)。
func init() {
	router.NewGroupRouter("/api/v1/withdrawal").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/summary", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(withdrawSummary),
		).
		AddRoute(
			router.NewRoute("/apply", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(applyWithdrawal),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(listWithdrawals),
		).
		AddRoute(
			router.NewRoute("/cancel", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(cancelWithdrawal),
		).
		AddRoute(
			router.NewRoute("/review", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(reviewWithdrawal),
		).
		AddRoute(
			router.NewRoute("/mark-paid", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(payWithdrawal),
		).
		AddRoute(
			router.NewRoute("/rollback", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(rollbackWithdrawal),
		)
}

// withdrawSummary 提现口径: 可提余额/冻结/已提 + 规则参数。
func withdrawSummary(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	summary, err := op.WithdrawSummaryGet(c.Request.Context(), userID, role)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, summary)
}

// applyWithdrawal 提现申请: 收益池条件冻结后建单, 可提不足直接拒绝。
func applyWithdrawal(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	var req struct {
		Amount      float64 `json:"amount"`
		Method      string  `json:"method"`
		AccountName string  `json:"account_name"`
		AccountNo   string  `json:"account_no"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	order, err := op.WithdrawApply(c.Request.Context(), op.WithdrawSpec{
		UserID:      userID,
		Role:        role,
		Amount:      req.Amount,
		Method:      req.Method,
		AccountName: req.AccountName,
		AccountNo:   req.AccountNo,
	})
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 审计不带收款账号(敏感), 只记单据与金额。
	audit(c, "withdrawal.apply", fmt.Sprintf("#%d", order.ID),
		fmt.Sprintf("amount=%g fee=%g actual=%g method=%s", order.Amount, order.Fee, order.Actual, order.Method))
	resp.Success(c, gin.H{"order": order})
}

// listWithdrawals 提现记录: 用户看自己(账号脱敏), 管理员可看全部(账号完整, 打款要用)。
func listWithdrawals(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	if role == model.RoleAdmin {
		userID = 0
		if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
			if parsed, ok := parsePositiveUint(raw); ok {
				userID = parsed
			} else {
				resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
				return
			}
		}
	}
	orders, err := op.WithdrawList(c.Request.Context(), userID, c.Query("status"), mustAtoi(c.DefaultQuery("limit", "50")))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if role != model.RoleAdmin {
		for index := range orders {
			orders[index].AccountNo = model.MaskAccount(orders[index].AccountNo)
		}
	}
	resp.Success(c, gin.H{"items": orders})
}

// parsePositiveUint 解析正整数 ID; 非法字符或 0 视为非法。
func parsePositiveUint(raw string) (uint, bool) {
	if raw == "" {
		return 0, false
	}
	number := uint(0)
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		number = number*10 + uint(ch-'0')
	}
	return number, number > 0
}

// cancelWithdrawal 用户取消: 仅本人 pending 可取消。
func cancelWithdrawal(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.WithdrawCancel(c.Request.Context(), userID, req.ID, ""); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "withdrawal.cancel", fmt.Sprintf("#%d", req.ID), "user cancel")
	resp.Success(c, gin.H{"id": req.ID})
}

// reviewWithdrawal 管理员审核: 通过进 approved, 拒绝解冻。
func reviewWithdrawal(c *gin.Context) {
	actorID, _ := middleware.CurrentUser(c)
	var req struct {
		ID      uint   `json:"id"`
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.WithdrawReview(c.Request.Context(), req.ID, req.Approve, req.Note, actorID, operatorName(c)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	verdict := "rejected"
	if req.Approve {
		verdict = "approved"
	}
	audit(c, "withdrawal.review", fmt.Sprintf("#%d", req.ID), verdict+" "+req.Note)
	resp.Success(c, gin.H{"id": req.ID})
}

// payWithdrawal 管理员打款: approved → paid, 凭证必填。
func payWithdrawal(c *gin.Context) {
	var req struct {
		ID      uint   `json:"id"`
		PaidRef string `json:"paid_ref"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.WithdrawMarkPaid(c.Request.Context(), req.ID, req.PaidRef, operatorName(c)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "withdrawal.paid", fmt.Sprintf("#%d", req.ID), "paid_ref "+strings.TrimSpace(req.PaidRef))
	resp.Success(c, gin.H{"id": req.ID})
}

// rollbackWithdrawal 打款回滚: paid → approved(打款失败纠错)。
func rollbackWithdrawal(c *gin.Context) {
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.WithdrawRollback(c.Request.Context(), req.ID, operatorName(c)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "withdrawal.rollback", fmt.Sprintf("#%d", req.ID), "paid rollback")
	resp.Success(c, gin.H{"id": req.ID})
}
