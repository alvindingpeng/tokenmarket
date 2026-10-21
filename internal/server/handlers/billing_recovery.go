package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/ops/billing-recovery").Use(middleware.Auth()).
		AddRoute(router.NewRoute("/list", http.MethodGet).Allow(model.RoleAdmin).Handle(listBillingRecovery)).
		AddRoute(router.NewRoute("/run", http.MethodPost).Allow(model.RoleAdmin).Handle(runBillingRecovery))
}

func listBillingRecovery(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 200 {
		resp.Error(c, http.StatusBadRequest, "limit must be between 1 and 200")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	rows, err := op.BillingRecoveryList(ctx, limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to list billing recovery tasks")
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// Only runs due, retryable tasks. No endpoint can bypass a manual-review hold or change a snapshot.
func runBillingRecovery(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	actorID, _ := middleware.CurrentUser(c)
	if err := op.AuditLogAdd(ctx, actorID, "billing.recovery.run", "billing-recovery", "requested processing of at most 50 due tasks; manual review holds are not overridden"); err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to audit recovery request; run was not started")
		return
	}
	if err := op.BillingRecoveryRun(ctx, 50); err != nil {
		resp.Error(c, http.StatusInternalServerError, "billing recovery run failed; inspect task status and alerts")
		return
	}
	resp.Success(c, gin.H{"accepted": true})
}
