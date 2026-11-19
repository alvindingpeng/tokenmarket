package handlers

import (
	"net/http"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 渠道质量(P3): 归属者看自有渠道, 管理员看全部; 非归属按 404 语义(op 层 scope.Owns 已裁剪)。
	// 与审核队列分开分组: 质量是持续观测, 审核是一次性决策, 两者的鉴权与刷新节奏都不同。
	router.NewGroupRouter("/api/v1/channel/quality").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/:id", http.MethodGet).
				Handle(getChannelQuality),
		).
		AddRoute(
			router.NewRoute("/snapshot/:id", http.MethodGet).
				Handle(getChannelQualityTrend),
		)
}

// getChannelQuality 返回渠道当前质量概览: 等级 + 观察期进度 + 昨日指标 + 近 30 天趋势。
// days 可选, 默认 30, 上限 180(与快照裁剪窗口一致)。
func getChannelQuality(c *gin.Context) {
	channelID, ok := parseChannelIDParam(c)
	if !ok {
		return
	}
	days := queryInt(c, "days", 30)
	summary, err := op.ChannelQualityGet(channelID, days, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, summary)
}

// getChannelQualityTrend 只返回日快照趋势(不含观察期进度), 供图表按需单独拉取。
func getChannelQualityTrend(c *gin.Context) {
	channelID, ok := parseChannelIDParam(c)
	if !ok {
		return
	}
	days := queryInt(c, "days", 30)
	summary, err := op.ChannelQualityGet(channelID, days, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, gin.H{"channel_id": channelID, "trend": summary.Trend})
}

// parseChannelIDParam 解析 :id 路径参数; 非法时直接写 400 并返回 false, 调用方据此提前返回。
func parseChannelIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid channel id")
		return 0, false
	}
	return id, true
}

// queryInt 读取整数查询参数, 缺失或非法时回退默认值; 范围由 op 层裁剪。
func queryInt(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
