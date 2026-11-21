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
		).
		AddRoute(
			router.NewRoute("/latency", http.MethodGet).Handle(getUserStatsLatency),
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
	// 按模型拆分只有明细能算: 汇总表没有 model 维度, 超出留存期(默认 180 天)的拆分不可得。
	// 这条边界不放进 JSON 体(响应是数组, 加字段会破坏既有契约), 改用响应头如实告知 ——
	// 前端据此提示"此区间之前无模型明细", 而不是把缺失的模型读成 0 用量。
	// 概览响应里同名语义的字段是 model_breakdown_coverage_start。
	if coverageStart, err := op.UserStatsModelCoverageStart(c.Request.Context(), userID, query.StartDate, query.EndDate); err == nil && coverageStart != "" {
		c.Header("X-Model-Coverage-Start", coverageStart)
	}
	resp.Success(c, models)
}

// getUserStatsLatency 查询当前用户调用延迟指标。
// 返回体会带上真实数据覆盖起点与调用日志留存天数: 日志按留存期滚动删除,
// 查询区间可能只有末尾少数几天有数据, 前端必须能如实提示而不是假装覆盖全区间。
func getUserStatsLatency(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var query model.UserStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		resp.Error(c, http.StatusBadRequest, "参数错误: "+err.Error())
		return
	}

	latency, err := op.UserStatsLatency(c.Request.Context(), userID, query.StartDate, query.EndDate)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, latency)
}
