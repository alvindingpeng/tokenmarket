package handlers

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// docsPage 控制台外的 API 文档页: 原生 HTML 渲染 openapi.json, 不依赖外部 CDN, 离线可用。
//
//go:embed docs.html
var docsPage []byte

// serveDocs 返回自渲染 API 文档页。
func serveDocs(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", docsPage)
}
