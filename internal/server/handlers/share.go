package handlers

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// sharePageTpl 分享页模板: 纯前端渲染 + 公开 API 取数, 不依赖控制台构建产物, 无需登录即可访问。
//
//go:embed share.html
var sharePageTpl []byte

func init() {
	router.NewGroupRouter("/api/v1/share").
		AddRoute(router.NewRoute("/:code", http.MethodGet).Handle(shareModels))
	router.NewGroupRouter("/share").
		AddRoute(router.NewRoute("/:code", http.MethodGet).Handle(sharePage))
}

// shareModels 公开返回发布编码下的模型清单与对客价, 供分享页与下游对接方查询。
// quality 是脱敏质量摘要(只有区间与等级, 无精确值), 无近期数据时该字段为 null。
func shareModels(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	models, quality, err := op.ShareChannelDetail(code)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, gin.H{"share_code": code, "models": models, "quality": quality})
}

// sharePage 返回公开分享页; 编码无效也返回页面, 由页面展示未找到状态(避免暴露存在性差异的时序)。
func sharePage(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", sharePageTpl)
}
