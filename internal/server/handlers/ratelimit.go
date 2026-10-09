package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/ratelimit"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 限流策略与监控属管理后台, 仅管理员可读写。
	router.NewGroupRouter("/api/v1/ratelimit").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/policy", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listRatePolicies),
		).
		AddRoute(
			router.NewRoute("/policy/save", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(saveRatePolicy),
		).
		AddRoute(
			router.NewRoute("/policy/delete", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(deleteRatePolicy),
		).
		AddRoute(
			router.NewRoute("/inspect", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(inspectRateLimit),
		).
		AddRoute(
			router.NewRoute("/usage", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listRateUsage),
		)
}

// ratePolicyFromRequest 从请求体解析限流策略写入参数; 数值字段按浮点解码避免前端 number 精度问题。
func ratePolicyFromRequest(c *gin.Context) (model.RateLimitPolicy, string) {
	var input struct {
		ID         uint   `json:"id"`
		ScopeType  string `json:"scope_type"`
		ScopeID    int    `json:"scope_id"`
		ModelName  string `json:"model_name"`
		RPM        int64  `json:"rpm"`
		TPM        int64  `json:"tpm"`
		Concurrent int64  `json:"concurrent"`
		Enabled    bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		return model.RateLimitPolicy{}, "invalid policy input"
	}
	return model.RateLimitPolicy{ID: input.ID, ScopeType: strings.TrimSpace(input.ScopeType), ScopeID: input.ScopeID, ModelName: strings.TrimSpace(input.ModelName), RPM: input.RPM, TPM: input.TPM, Concurrent: input.Concurrent, Enabled: input.Enabled}, ""
}

// str 读取请求体字符串字段。
func str(body map[string]interface{}, key string) string {
	if value, ok := body[key].(string); ok {
		return value
	}
	return ""
}

// num 读取请求体数值字段。
func num(body map[string]interface{}, key string) float64 {
	if value, ok := body[key].(float64); ok {
		return value
	}
	return 0
}

// listRatePolicies 返回全部限流策略。
func listRatePolicies(c *gin.Context) {
	resp.Success(c, gin.H{"items": op.RatePolicyList()})
}

// saveRatePolicy 新建或更新限流策略。
func saveRatePolicy(c *gin.Context) {
	policy, errMsg := ratePolicyFromRequest(c)
	if errMsg != "" {
		resp.Error(c, http.StatusBadRequest, errMsg)
		return
	}
	if !model.IsValidRateScope(policy.ScopeType) {
		resp.Error(c, http.StatusBadRequest, "invalid scope type")
		return
	}
	if policy.ScopeType == model.RateScopeSystem {
		policy.ScopeID = 0
	} else if policy.ScopeID <= 0 {
		resp.Error(c, http.StatusBadRequest, "scope id is required")
		return
	}
	if policy.RPM < 0 || policy.TPM < 0 || policy.Concurrent < 0 {
		resp.Error(c, http.StatusBadRequest, "limits must be non-negative")
		return
	}
	if err := op.RatePolicySave(&policy); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "ratelimit.policy-save", policy.ScopeType+":"+strconv.Itoa(policy.ScopeID),
		"model="+policy.ModelName+" rpm="+strconv.FormatInt(policy.RPM, 10)+" tpm="+strconv.FormatInt(policy.TPM, 10)+" concurrent="+strconv.FormatInt(policy.Concurrent, 10))
	resp.Success(c, policy)
}

// deleteRatePolicy 删除限流策略。
func deleteRatePolicy(c *gin.Context) {
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil || num(body, "id") == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid policy id")
		return
	}
	id := uint(num(body, "id"))
	if err := op.RatePolicyDelete(id); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "ratelimit.policy-delete", strconv.FormatUint(uint64(id), 10), "")
	c.Status(http.StatusNoContent)
}

// inspectRateLimit 返回某范围的生效限流值、当前窗口用量与触顶状态。
func inspectRateLimit(c *gin.Context) {
	scopeType := strings.TrimSpace(c.Query("scope_type"))
	if scopeType == "" {
		scopeType = model.RateScopeSystem
	}
	if !model.IsValidRateScope(scopeType) {
		resp.Error(c, http.StatusBadRequest, "invalid scope type")
		return
	}
	scopeID, _ := strconv.Atoi(c.Query("scope_id"))
	modelName := strings.TrimSpace(c.Query("model"))
	scope := ratelimit.Scope{Kind: scopeType, ID: scopeID, Model: modelName}
	models := op.RateScopeModels(scopeType, scopeID, modelName)
	canonical := scope
	canonical.Model = models[0]
	rpm, tpm, concurrentLimit := op.RateLimitsFor(scopeType, scopeID, canonical.Model)
	budgets := make([]gin.H, 0, len(models))
	for _, name := range models {
		s := scope
		s.Model = name
		r, t, con := op.RateLimitsFor(scopeType, scopeID, name)
		requests, tokens, reset := relay.Limiter().UsageExact(s)
		budgets = append(budgets, gin.H{"model_name": name, "rpm": r, "tpm": t, "concurrent": con, "requests": requests, "tokens": tokens, "active": relay.Limiter().ConcurrentUsageExact(s), "reset_at": reset})
	}
	requests, tokens, resetAt := relay.Limiter().Usage(scope)
	active := relay.Limiter().ConcurrentUsage(scope)
	resp.Success(c, gin.H{
		"budgets":    budgets,
		"scope_type": scopeType,
		"scope_id":   scopeID,
		"model_name": modelName,
		"rpm":        rpm,
		"tpm":        tpm,
		"concurrent": concurrentLimit,
		"active":     active,
		"requests":   requests,
		"tokens":     tokens,
		"reset_at":   resetAt,
		"blocked":    relay.Limiter().Blocked(scope),
	})
}

// listRateUsage 返回最近若干小时的限流用量聚合, 可按范围过滤。
func listRateUsage(c *gin.Context) {
	scopeType := strings.TrimSpace(c.Query("scope_type"))
	scopeID, _ := strconv.Atoi(c.Query("scope_id"))
	hours, _ := strconv.Atoi(c.Query("hours"))
	rows, err := op.RateUsageList(scopeType, scopeID, hours)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}
