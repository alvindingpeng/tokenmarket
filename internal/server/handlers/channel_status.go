package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 公开状态页: 无需登录, 只暴露聚合后的渠道模型健康视图。
	// 不挂 middleware.Auth(): 与 /api/v1/share 同为公开面, DTO 在 op 侧按最小白名单构造
	// (渠道身份只有发布编码, 无渠道名/归属者/主键/凭据), 匿名暴露面可控。
	router.NewGroupRouter("/api/v1/public").
		AddRoute(router.NewRoute("/channel/stats", http.MethodGet).Handle(publicChannelStats))
}

// publicChannelStats 返回全站已上线渠道模型的公开状态快照。
// 15 分钟滚动窗口 + 15 个一分钟历史桶, 快照 10s 缓存, 建议 15s 刷新(见 DTO 的 refresh_seconds)。
func publicChannelStats(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	snapshot, err := op.ChannelStatusSnapshot(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	resp.Success(c, snapshot)
}
