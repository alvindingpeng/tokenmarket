package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/admin/tenant").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listTenants),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(createTenant),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getTenant),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodPut).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(updateTenant),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(deleteTenant),
		).
		AddRoute(
			router.NewRoute("/:id/quota", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getTenantQuota),
		).
		AddRoute(
			router.NewRoute("/:id/quota", http.MethodPut).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(updateTenantQuota),
		).
		AddRoute(
			router.NewRoute("/:id/quota/usage", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getTenantQuotaUsage),
		).
		AddRoute(
			router.NewRoute("/:id/wallet", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getTenantWallet),
		).
		AddRoute(
			// 充值直接改变客户的可用额度, 属于资金操作: 与用户改余额一致要求二次验证,
			// 否则一个被盗的管理员会话就能凭空给任意租户加钱。
			router.NewRoute("/:id/wallet/recharge", http.MethodPost).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON(), middleware.RequireStepUp()).
				Handle(rechargeTenantWallet),
		).
		AddRoute(
			router.NewRoute("/:id/wallet/adjust", http.MethodPost).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON(), middleware.RequireStepUp()).
				Handle(adjustTenantWallet),
		).
		AddRoute(
			router.NewRoute("/:id/wallet/ledger", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listTenantLedger),
		).
		AddRoute(
			router.NewRoute("/:id/pricing", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listTenantPricingPolicies),
		).
		AddRoute(
			router.NewRoute("/:id/pricing", http.MethodPost).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(createTenantPricingPolicy),
		).
		AddRoute(
			router.NewRoute("/:id/pricing/:pid", http.MethodPut).
				Allow(model.RoleAdmin).
				Use(middleware.RequireJSON()).
				Handle(updateTenantPricingPolicy),
		).
		AddRoute(
			router.NewRoute("/:id/pricing/:pid", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(deleteTenantPricingPolicy),
		)
}

func listTenants(c *gin.Context) {
	tenants, err := op.TenantList()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, tenants)
}

type createTenantRequest struct {
	Name   string `json:"name" binding:"required"`
	Code   string `json:"code" binding:"required"`
	Status string `json:"status"`
}

func createTenant(c *gin.Context) {
	var req createTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	tenant, err := op.TenantCreate(req.Name, req.Code, req.Status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.create", fmt.Sprintf("tenant#%d", tenant.ID), fmt.Sprintf("name=%s code=%s status=%s", tenant.Name, tenant.Code, tenant.Status))
	resp.Success(c, tenant)
}

func getTenant(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	tenant, err := op.TenantGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, tenant)
}

type updateTenantRequest struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Status string `json:"status"`
}

func updateTenant(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var req updateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	oldTenant, _ := op.TenantGet(id)
	tenant, err := op.TenantUpdate(id, req.Name, req.Code, req.Status)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 审计记旧值与新值: 租户状态变更是停服/复服动作, 事后追溯要知道"什么时候被谁改成了什么"。
	audit(c, "tenant.update", fmt.Sprintf("tenant#%d", tenant.ID),
		fmt.Sprintf("name %s->%s code %s->%s status %s->%s", oldTenant.Name, tenant.Name, oldTenant.Code, tenant.Code, oldTenant.Status, tenant.Status))
	resp.Success(c, tenant)
}

func deleteTenant(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	if id == 1 {
		resp.Error(c, http.StatusBadRequest, "cannot delete default tenant")
		return
	}
	if err := op.TenantDelete(id); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.delete", fmt.Sprintf("tenant#%d", id), "tenant deleted")
	resp.Success(c, "tenant deleted")
}

func getTenantQuota(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	quota, err := op.TenantQuotaGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, quota)
}

type updateTenantQuotaRequest struct {
	RequestsLimit     int64  `json:"requests_limit"`
	InputTokensLimit  int64  `json:"input_tokens_limit"`
	OutputTokensLimit int64  `json:"output_tokens_limit"`
	Period            string `json:"period"`
	ResetDay          int    `json:"reset_day"`
}

func updateTenantQuota(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var req updateTenantQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.Period == "" {
		req.Period = "monthly"
	}
	if req.ResetDay <= 0 || req.ResetDay > 28 {
		req.ResetDay = 1
	}
	quota, err := op.TenantQuotaUpdate(id, req.RequestsLimit, req.InputTokensLimit, req.OutputTokensLimit, req.Period, req.ResetDay)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.quota", fmt.Sprintf("tenant#%d", id),
		fmt.Sprintf("requests=%d input=%d output=%d period=%s reset_day=%d", quota.RequestsLimit, quota.InputTokensLimit, quota.OutputTokensLimit, quota.Period, quota.ResetDay))
	resp.Success(c, quota)
}

// tenantQuotaUsageResponse 租户配额用量视图: 配额上限与当前周期用量并排,
// 前端据此直接算百分比进度条, 不必再各取一次。
type tenantQuotaUsageResponse struct {
	Quota          model.TenantQuota `json:"quota"`
	Requests       int64             `json:"requests"`
	InputTokens    int64             `json:"input_tokens"`
	OutputTokens   int64             `json:"output_tokens"`
	ResetAt        string            `json:"reset_at"`
	RequestsPct    float64           `json:"requests_pct"`
	InputTokensPct float64           `json:"input_tokens_pct"`
	OutputPct      float64           `json:"output_tokens_pct"`
}

func getTenantQuotaUsage(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	quota, err := op.TenantQuotaGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	requests, inputTokens, outputTokens, err := op.GetTenantQuotaUsage(c.Request.Context(), uint(id))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	view := tenantQuotaUsageResponse{
		Quota:        quota,
		Requests:     requests,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	// 百分比只在有上限时计算; 0 表示不限, 前端据此隐藏进度条而不是显示 0%。
	if quota.RequestsLimit > 0 {
		view.RequestsPct = float64(requests) / float64(quota.RequestsLimit) * 100
	}
	if quota.InputTokensLimit > 0 {
		view.InputTokensPct = float64(inputTokens) / float64(quota.InputTokensLimit) * 100
	}
	if quota.OutputTokensLimit > 0 {
		view.OutputPct = float64(outputTokens) / float64(quota.OutputTokensLimit) * 100
	}
	resp.Success(c, view)
}

func getTenantWallet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	wallet, err := op.TenantWalletGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, wallet)
}

// tenantWalletFundRequest 充值/调账共用的请求体。
type tenantWalletFundRequest struct {
	Amount float64 `json:"amount"`
	Note   string  `json:"note"`
}

func rechargeTenantWallet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var req tenantWalletFundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID, _ := middleware.CurrentUser(c)
	actorName := c.GetString("username")
	wallet, err := op.TenantWalletRecharge(c.Request.Context(), uint(id), req.Amount, actorID, actorName, req.Note)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.wallet.recharge", fmt.Sprintf("tenant#%d", id), fmt.Sprintf("amount=%.6f note=%s", req.Amount, req.Note))
	resp.Success(c, wallet)
}

func adjustTenantWallet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var req tenantWalletFundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID, _ := middleware.CurrentUser(c)
	actorName := c.GetString("username")
	wallet, err := op.TenantWalletAdjust(c.Request.Context(), uint(id), req.Amount, actorID, actorName, req.Note)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.wallet.adjust", fmt.Sprintf("tenant#%d", id), fmt.Sprintf("delta=%.6f note=%s", req.Amount, req.Note))
	resp.Success(c, wallet)
}

func listTenantLedger(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, total, err := op.TenantLedgerList(c.Request.Context(), op.TenantLedgerQuery{
		TenantID: uint(id),
		Kind:     c.Query("kind"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows, "total": total})
}

func listTenantPricingPolicies(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	policies, err := op.TenantPricingPolicyList(id)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, policies)
}

type createTenantPricingPolicyRequest struct {
	ChannelID   *int    `json:"channel_id"`
	ModelName   string  `json:"model_name"`
	MarkupRatio float64 `json:"markup_ratio"`
	Currency    string  `json:"currency"`
}

func createTenantPricingPolicy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	var req createTenantPricingPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	policy, err := op.TenantPricingPolicyCreate(id, req.ChannelID, req.ModelName, req.MarkupRatio, req.Currency)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.pricing.create", fmt.Sprintf("tenant#%d policy#%d", id, policy.ID),
		fmt.Sprintf("model=%s markup=%v currency=%s", policy.ModelName, policy.MarkupRatio, policy.Currency))
	resp.Success(c, policy)
}

type updateTenantPricingPolicyRequest struct {
	ChannelID   *int    `json:"channel_id"`
	ModelName   string  `json:"model_name"`
	MarkupRatio float64 `json:"markup_ratio"`
	Currency    string  `json:"currency"`
}

func updateTenantPricingPolicy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	pid, err := strconv.Atoi(c.Param("pid"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid policy id")
		return
	}
	// 策略必须属于路径里的租户: 否则可用任意 pid 改别的租户的定价。
	if !op.TenantPricingPolicyBelongsTo(pid, id) {
		resp.Error(c, http.StatusNotFound, "pricing policy not found")
		return
	}
	var req updateTenantPricingPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	policy, err := op.TenantPricingPolicyUpdate(pid, req.ChannelID, req.ModelName, req.MarkupRatio, req.Currency)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.pricing.update", fmt.Sprintf("tenant#%d policy#%d", id, pid),
		fmt.Sprintf("model=%s markup=%v currency=%s", policy.ModelName, policy.MarkupRatio, policy.Currency))
	resp.Success(c, policy)
}

func deleteTenantPricingPolicy(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid tenant id")
		return
	}
	pid, err := strconv.Atoi(c.Param("pid"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid policy id")
		return
	}
	// 同上: 删除也要校验归属, 避免跨租户删除。
	if !op.TenantPricingPolicyBelongsTo(pid, id) {
		resp.Error(c, http.StatusNotFound, "pricing policy not found")
		return
	}
	if err := op.TenantPricingPolicyDelete(pid); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "tenant.pricing.delete", fmt.Sprintf("tenant#%d policy#%d", id, pid), "pricing policy deleted")
	resp.Success(c, "pricing policy deleted")
}

func getUserTenants(c *gin.Context) {
	scope := middleware.CurrentUserScope(c)
	if scope.ID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenants, err := op.GetUserTenants(scope.ID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, tenants)
}

func getUserDefaultTenant(c *gin.Context) {
	scope := middleware.CurrentUserScope(c)
	if scope.ID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	tenant, err := op.GetUserDefaultTenant(scope.ID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, tenant)
}

// User tenant routes (authenticated users, not admin only)
func init() {
	router.NewGroupRouter("/api/v1/user/tenant").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getUserTenants),
		).
		AddRoute(
			router.NewRoute("/default", http.MethodGet).
				Handle(getUserDefaultTenant),
		)
}
