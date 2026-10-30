package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 余额预警配置: 每个登录用户管理自己的阈值。三条路由同组, 鉴权一次。
	group := router.NewGroupRouter("/api/v1/user/balance-alert").Use(middleware.Auth())
	group.AddRoute(
		router.NewRoute("", http.MethodGet).Handle(getBalanceAlert),
	)
	group.AddRoute(
		router.NewRoute("", http.MethodPost).Use(middleware.RequireJSON()).Handle(setBalanceAlert),
	)
	group.AddRoute(
		router.NewRoute("", http.MethodDelete).Handle(deleteBalanceAlert),
	)
}

// getBalanceAlert 返回当前用户的余额预警配置(未配置时返回禁用状态)。
func getBalanceAlert(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	view, err := op.BalanceAlertGet(c.Request.Context(), userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, view)
}

// setBalanceAlert 保存当前用户的余额预警阈值与开关。
func setBalanceAlert(c *gin.Context) {
	var req model.BalanceAlertUpsert
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	userID, _ := middleware.CurrentUser(c)
	view, err := op.BalanceAlertSet(c.Request.Context(), userID, req)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, view)
}

// deleteBalanceAlert 取消当前用户的余额预警。
func deleteBalanceAlert(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if err := op.BalanceAlertDelete(c.Request.Context(), userID); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"deleted": true})
}
