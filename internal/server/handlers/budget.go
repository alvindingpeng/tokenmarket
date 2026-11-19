package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 用户花费预算: 管理员管理, 用户侧只读(自己的预算)。判定在 relay 预扣前(op.CheckUserBudget)。
func init() {
	router.NewGroupRouter("/api/v1/budgets").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listBudgets),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(createBudget),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodPut).
				Allow(model.RoleAdmin).
				Handle(updateBudget),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(deleteBudget),
		)
}

// listBudgets 预算清单(含禁用的, 便于回看与重新启用)。
func listBudgets(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	budgets, err := op.BudgetList(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": budgets})
}

// budgetInput 创建/更新的请求体。scope 二选一由 service 层校验。
type budgetInput struct {
	UserID      uint    `json:"user_id"`
	APIKeyID    int     `json:"api_key_id"`
	Period      string  `json:"period"`
	Action      string  `json:"action"`
	LimitAmount float64 `json:"limit_amount"`
	WarnRatio   int     `json:"warn_ratio"`
	Enabled     *bool   `json:"enabled"`
}

// createBudget 新建预算。
func createBudget(c *gin.Context) {
	var input budgetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	budget, err := op.BudgetCreate(ctx, model.SpendBudget{
		UserID:      input.UserID,
		APIKeyID:    input.APIKeyID,
		Period:      input.Period,
		Action:      input.Action,
		LimitAmount: input.LimitAmount,
		WarnRatio:   input.WarnRatio,
	})
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "budget.create", fmt.Sprintf("user:%d key:%d", budget.UserID, budget.APIKeyID), budget.Period)
	resp.Success(c, gin.H{"item": budget})
}

// updateBudget 修改预算字段。
func updateBudget(c *gin.Context) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid budget id")
		return
	}
	var input budgetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	enabled := input.Enabled == nil || *input.Enabled
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	budget, err := op.BudgetUpdate(ctx, uint(id), input.Period, input.Action, input.LimitAmount, input.WarnRatio, enabled)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "budget.update", strconv.FormatUint(id, 10), budget.Period)
	resp.Success(c, gin.H{"item": budget})
}

// deleteBudget 删除预算。
func deleteBudget(c *gin.Context) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid budget id")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := op.BudgetDelete(ctx, uint(id)); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "budget.delete", strconv.FormatUint(id, 10), "")
	resp.Success(c, gin.H{"ok": true})
}

func stringsTrimSpace(s string) string {
	return strings.TrimSpace(s)
}
