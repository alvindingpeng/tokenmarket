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
	// 用户调用分析: 每个登录用户查看自己的调用统计。
	router.NewGroupRouter("/api/v1/user-stats").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/overview", http.MethodGet).Handle(getUserStatsOverview),
		).
		AddRoute(
			router.NewRoute("/trend", http.MethodGet).Handle(getUserStatsTrend),
		).
		AddRoute(
			router.NewRoute("/models", http.MethodGet).Handle(getUserStatsModels),
		)
}

// getUserStatsOverview 查询当前用户调用概览。
func getUserStatsOverview(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var query model.UserStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	
	overview, err := op.UserStatsOverview(c.Request.Context(), userID, query.StartDate, query.EndDate)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, overview)
}

// getUserStatsTrend 查询当前用户调用趋势。
func getUserStatsTrend(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var query model.UserStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	
	trend, err := op.UserStatsTrend(c.Request.Context(), userID, query.StartDate, query.EndDate, query.Granularity)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, trend)
}

// getUserStatsModels 查询当前用户按模型分组统计。
func getUserStatsModels(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var query model.UserStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}
	
	models, err := op.UserStatsModelBreakdown(c.Request.Context(), userID, query.StartDate, query.EndDate)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, models)
}
