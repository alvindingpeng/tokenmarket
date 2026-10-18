package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 钱包接口: 摘要给钱包页首屏; 后续充值与提现接口挂同一分组下。
func init() {
	router.NewGroupRouter("/api/v1/wallet").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/summary", http.MethodGet).
				Handle(walletSummary),
		)
}

// walletSummary 余额口径与累计口径一次取回。
func walletSummary(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	user, err := op.UserGetByID(userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"balance":          user.Balance,
		"frozen":           user.Frozen,
		"total_spent":      user.TotalSpent,
		"total_revenue":    user.TotalRevenue,
		"total_recharged":  user.TotalRecharged,
		"total_withdrawn":  user.TotalWithdrawn,
		"withdraw_frozen":  user.WithdrawFrozen,
		"withdraw_available": user.TotalRevenue - user.TotalWithdrawn - user.WithdrawFrozen,
	})
}
