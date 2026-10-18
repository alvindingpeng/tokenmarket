package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alvindingpeng/switchyard/internal/conf"
)

// requestIsHTTPS 判断调用方是否经 HTTPS 访问本站。
//
// 依据有两个: 本进程自己终结了 TLS, 或者可信反向代理声明了 X-Forwarded-Proto: https。
// 转发头只在配置了 server.trusted_proxies 时采信 —— 否则任何调用方都能自称 https,
// 这类"自己声明自己安全"的头一旦被无条件相信, 判断就失去了意义。
func requestIsHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if len(conf.AppConfig.Server.TrustedProxies) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
}
