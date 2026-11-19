package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
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
		// 提现审批是资金高危操作: 审核/打款/回滚统一要求 step-up(密码二次确认后才放行),
		// 申请/取消/查询不动别人的钱, 不加这道闸。
		AddRoute(
			router.NewRoute("/review", http.MethodPost).
				Use(middleware.RequireJSON()).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(reviewWithdrawal),
		).
		AddRoute(
			router.NewRoute("/mark-paid", http.MethodPost).
				Use(middleware.RequireJSON()).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(payWithdrawal),
		).
		AddRoute(
			router.NewRoute("/batch-review", http.MethodPost).
				Use(middleware.RequireJSON()).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(batchReviewWithdrawals),
		).
		AddRoute(
			router.NewRoute("/batch-paid", http.MethodPost).
				Use(middleware.RequireJSON()).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(batchPayWithdrawals),
		).
		AddRoute(
			router.NewRoute("/rollback", http.MethodPost).
				Use(middleware.RequireJSON()).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(rollbackWithdrawal),
		)
}

func init() {
	// 管理面批量提现端点使用 /admin 前缀; 保留旧 /withdrawal 单笔路由兼容。
	router.NewGroupRouter("/api/v1/admin/withdrawal").Use(middleware.Auth()).
		AddRoute(router.NewRoute("/batch-review", http.MethodPost).Use(middleware.RequireJSON()).Use(middleware.RequireStepUp()).Allow(model.RoleAdmin).Handle(batchReviewWithdrawals)).
		AddRoute(router.NewRoute("/batch-paid", http.MethodPost).Use(middleware.RequireJSON()).Use(middleware.RequireStepUp()).Allow(model.RoleAdmin).Handle(batchPayWithdrawals))
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
		Amount         float64 `json:"amount"`
		Method         string  `json:"method"`
		AccountName    string  `json:"account_name"`
		AccountNo      string  `json:"account_no"`
		PayoutCurrency string  `json:"payout_currency"` // 可选到账币种; 缺省 CNY(存量口径)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	order, err := op.WithdrawApply(c.Request.Context(), op.WithdrawSpec{
		UserID:         userID,
		Role:           role,
		Amount:         req.Amount,
		Method:         req.Method,
		AccountName:    req.AccountName,
		AccountNo:      req.AccountNo,
		PayoutCurrency: req.PayoutCurrency,
	})
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 审计不带收款账号(敏感), 只记单据与金额; 币种含在明细里供换币追溯。
	audit(c, "withdrawal.apply", fmt.Sprintf("#%d", order.ID),
		fmt.Sprintf("amount=%g fee=%g actual=%g method=%s payout=%s(%s)", order.Amount, order.Fee, order.Actual, order.Method, order.Currency, order.RateDay))
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
// actual_currency 可选: 换币打款时改写单上打款币种(白名单校验), 原样进审计。
func payWithdrawal(c *gin.Context) {
	var req struct {
		ID             uint   `json:"id"`
		PaidRef        string `json:"paid_ref"`
		ActualCurrency string `json:"actual_currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.WithdrawMarkPaid(c.Request.Context(), req.ID, req.PaidRef, req.ActualCurrency, operatorName(c)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	detail := "paid_ref " + strings.TrimSpace(req.PaidRef)
	if currency := strings.ToUpper(strings.TrimSpace(req.ActualCurrency)); currency != "" {
		detail += " actual_currency=" + currency
	}
	audit(c, "withdrawal.paid", fmt.Sprintf("#%d", req.ID), detail)
	resp.Success(c, gin.H{"id": req.ID, "actual_currency": strings.ToUpper(strings.TrimSpace(req.ActualCurrency))})
}

// batchWithdrawalResult 单笔批处理结果; 单笔失败不阻断后续单据。
type batchWithdrawalResult struct {
	ID    uint   `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// batchReviewWithdrawals 逐单复用单笔审核操作, 部分失败逐条回报。
func batchReviewWithdrawals(c *gin.Context) {
	actorID, _ := middleware.CurrentUser(c)
	var req struct {
		IDs     []uint `json:"ids"`
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 500 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	results := make([]batchWithdrawalResult, 0, len(req.IDs))
	succeeded := 0
	for _, id := range req.IDs {
		result := batchWithdrawalResult{ID: id}
		err := op.WithdrawReview(c.Request.Context(), id, req.Approve, req.Note, actorID, operatorName(c))
		if err != nil {
			result.Error = err.Error()
		} else {
			result.OK = true
			succeeded++
		}
		results = append(results, result)
	}
	verdict := "rejected"
	if req.Approve {
		verdict = "approved"
	}
	audit(c, "withdrawal.batch_review", "", fmt.Sprintf("ids=%d succeeded=%d failed=%d verdict=%s", len(req.IDs), succeeded, len(req.IDs)-succeeded, verdict))
	resp.Success(c, gin.H{"results": results, "succeeded": succeeded, "failed": len(req.IDs) - succeeded})
}

// batchPayWithdrawals 逐单复用单笔打款操作, actual_currency 可用于整批共同兑付币种。
func batchPayWithdrawals(c *gin.Context) {
	var req struct {
		IDs            []uint `json:"ids"`
		PaidRef        string `json:"paid_ref"`
		ActualCurrency string `json:"actual_currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 500 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	results := make([]batchWithdrawalResult, 0, len(req.IDs))
	succeeded := 0
	for _, id := range req.IDs {
		result := batchWithdrawalResult{ID: id}
		err := op.WithdrawMarkPaid(c.Request.Context(), id, req.PaidRef, req.ActualCurrency, operatorName(c))
		if err != nil {
			result.Error = err.Error()
		} else {
			result.OK = true
			succeeded++
		}
		results = append(results, result)
	}
	audit(c, "withdrawal.batch_paid", "", fmt.Sprintf("ids=%d succeeded=%d failed=%d actual_currency=%s", len(req.IDs), succeeded, len(req.IDs)-succeeded, strings.ToUpper(strings.TrimSpace(req.ActualCurrency))))
	resp.Success(c, gin.H{"results": results, "succeeded": succeeded, "failed": len(req.IDs) - succeeded})
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
