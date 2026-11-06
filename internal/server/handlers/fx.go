package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 汇率快照只读: 改价工作台折算预览与账单复核用。写路径只有后台任务, 不开 API。
	router.NewGroupRouter("/api/v1/fx").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/rates", http.MethodGet).
			Allow(model.RoleAdmin).
			Handle(getFxRates))
}

// getFxRates 当前生效的牌价快照(账面币种 USD 为基准)。
func getFxRates(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	day, items, err := op.FxRateSnapshotView(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"day": day, "items": items})
}
