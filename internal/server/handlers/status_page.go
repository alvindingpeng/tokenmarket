package handlers

import (
	_ "embed"
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// statusPageTpl 公开渠道状态页模板: 纯前端渲染 + 公开统计 API 轮询取数,
// 不依赖控制台构建产物, 无需登录即可访问。
//
//go:embed status.html
var statusPageTpl []byte

func init() {
	// 路由空路径("")即分组根 /status; 再补一条 "/" 让 /status/ 也直接命中,
	// 免得公开链接带不带尾斜杠两种写法落到 gin 的 301 重定向上。
	router.NewGroupRouter("/status").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(statusPage)).
		AddRoute(router.NewRoute("/", http.MethodGet).Handle(statusPage))
}

// statusPage 返回公开渠道状态页; 页面自行轮询 /api/v1/public/channel/stats,
// 取数失败也在前端降级展示(保留最近一次成功快照), 因此这里恒返回页面本体。
func statusPage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", statusPageTpl)
}
