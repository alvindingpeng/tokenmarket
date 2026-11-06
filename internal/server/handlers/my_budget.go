package handlers

import (
	"context"
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

// 用户自管预算: 与管理端 /api/v1/budgets 同表同校验, 但 scope 锁定本人 user 级。
// 用户能看到当前周期实时用量(spent/ratio), 预算到的提醒在 relay 判定时已发(去重)。
func init() {
	group := router.NewGroupRouter("/api/v1/user/budgets").Use(middleware.Auth())
	group.AddRoute(router.NewRoute("", http.MethodGet).Handle(myListBudgets)).
		AddRoute(router.NewRoute("", http.MethodPost).Handle(myCreateBudget)).
		AddRoute(router.NewRoute("/:id", http.MethodPut).Handle(myUpdateBudget)).
		AddRoute(router.NewRoute("/:id", http.MethodDelete).Handle(myDeleteBudget))
}

func currentUserID(c *gin.Context) uint {
	id, _ := middleware.CurrentUser(c)
	return id
}

// myListBudgets 本人预算清单(含禁用与实时用量)。
func myListBudgets(c *gin.Context) {
	userID := currentUserID(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, err := op.MyBudgetList(ctx, userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": items})
}

// myCreateBudget 用户自建预算(user 级)。校验复用 op.BudgetCreate。
func myCreateBudget(c *gin.Context) {
	var input budgetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID := currentUserID(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	budget, err := op.MyBudgetCreate(ctx, userID, model.SpendBudget{
		Period:      input.Period,
		Action:      input.Action,
		LimitAmount: input.LimitAmount,
		WarnRatio:   input.WarnRatio,
	})
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": budget})
}

func myUpdateBudget(c *gin.Context) {
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
	userID := currentUserID(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	budget, err := op.MyBudgetUpdate(ctx, userID, uint(id), input.Period, input.Action, input.LimitAmount, input.WarnRatio, enabled)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": budget})
}

func myDeleteBudget(c *gin.Context) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid budget id")
		return
	}
	userID := currentUserID(c)
	if userID == 0 {
		resp.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := op.MyBudgetDelete(ctx, userID, uint(id)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"ok": true})
}
