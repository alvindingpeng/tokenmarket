package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// BodyLimitRule 为某个路径前缀单独指定请求体上限; MaxBytes <= 0 表示该前缀不受限。
// 上限的缺省值由 conf 包给出(server.max_body_bytes / server.relay_max_body_bytes)。
type BodyLimitRule struct {
	Prefix   string
	MaxBytes int64
}

// BodyLimit 限制请求体大小, 超限直接回 413, 不把请求交给业务处理函数。
//
// rules 按顺序匹配, 命中的第一条生效。典型用法: 管理端走全局上限,
// 转发接口(/v1)走更宽的一档, 整库导入完全豁免 —— 生产库里单是日志表就上百 MB,
// 用统一上限只会把合法的运维动作变成打不开的 413。
func BodyLimit(defaultMax int64, rules ...BodyLimitRule) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !bytesLimited(c.Request.Method) {
			c.Next()
			return
		}
		maxBytes, limited := resolveBodyLimit(defaultMax, rules, c.Request.URL.Path)
		if !limited || maxBytes <= 0 {
			c.Next()
			return
		}
		// Content-Length 已知时提前拒绝, 省掉真把超限字节读一遍的开销。
		if c.Request.ContentLength > maxBytes {
			rejectTooLarge(c, maxBytes)
			return
		}
		// Content-Length 缺失或谎报时由 MaxBytesReader 兜底: 读到第 n+1 个字节即报错,
		// 处理函数拿到 "request body too large" 后按自己的方式报错。
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// resolveBodyLimit 找出该路径适用的上限; 第二个返回值 false 表示无人限制。
func resolveBodyLimit(defaultMax int64, rules []BodyLimitRule, path string) (int64, bool) {
	for _, rule := range rules {
		if rule.Prefix != "" && strings.HasPrefix(path, rule.Prefix) {
			return rule.MaxBytes, rule.MaxBytes > 0
		}
	}
	return defaultMax, defaultMax > 0
}

// bytesLimited 判断该方法是否可能携带请求体; GET/HEAD/OPTIONS 不必检查。
func bytesLimited(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// rejectTooLarge 回 413 并留下一条日志: 被限制挡下的请求若完全静默, 排障时只能猜。
// 响应体走 resp.Error, 与全站错误形状一致, 前端不需要为这个状态码特判。
func rejectTooLarge(c *gin.Context, maxBytes int64) {
	log.Warnf("request body too large: %s %s (limit %d bytes)", c.Request.Method, c.Request.URL.Path, maxBytes)
	resp.Error(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds %d bytes", maxBytes))
}
