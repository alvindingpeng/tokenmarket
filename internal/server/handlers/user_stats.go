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
	// 这条边界不放进 JSON 体(响应是数组, 加字段会破坏既有契约), 改用响应头如实告知 ——
	// 前端据此提示"此区间之前拆不到模型", 而不是把缺失的模型读成 0 用量。
	// 概览响应里同名语义的字段是 model_breakdown_coverage_start。
	//
	// 覆盖起点 = min(区间内最早明细日, 区间内最早的 model 维度汇总日)。自迁移 45 起
	// billing_rollups 多了 model 维度, 超出留存期(默认 180 天)的拆分**仍然有数**: 明细算不出的
	// 部分由汇总补上。真正补不回来的只有迁移 45 之前就被清理掉的日子。
	//
	// **响应头只在非空时下发, 这是刻意的**: 头不存在 = "本响应没声明覆盖边界",
	// 而空串 = "区间内确实没有任何按模型数据"。两者语义不同; 若为了"总是有头"把头写成空串,
	// 前端与第三方消费者都会把"不知道"读成"确定没有"。JSON 体里的
	// model_breakdown_coverage_start 是恒下发的, 需要区分这两种情况请读它。
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
