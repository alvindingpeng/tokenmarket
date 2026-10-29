package middleware

import (
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func Cors() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	config.ExposeHeaders = []string{"Content-Disposition"}
	// CORS 白名单:
	// - 为空: 不允许跨域
	// - "*": 允许所有来源
	// - 逗号分隔的域名列表: 只允许指定的域名 (如 "https://example.com,https://example2.com")
	config.AllowOriginFunc = func(origin string) bool {
		allowed, err := op.SettingGetString(model.SettingKeyCORSAllowOrigins)
		if err != nil {
			return false
		}
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			return false
		}
		// AllowCredentials 恒为 true(会话是 cookie 鉴权), 而 "*" + 凭据等于「任意恶意网页都能
		// 以登录用户身份发跨域请求并读取响应」——等同浏览器内账户接管。故 "*" 一律拒绝,
		// 必须配置带 scheme 的完整 origin 白名单(如 https://example.com)。
		if allowed == "*" {
			return false
		}

		origin = strings.TrimSpace(origin)
		if origin == "" {
			return false
		}

		// 提取 origin 的 host 部分用于匹配
		originHost := origin
		if idx := strings.Index(origin, "://"); idx != -1 {
			originHost = origin[idx+3:]
		}
		originHost = strings.TrimRight(originHost, "/")

		for _, item := range strings.Split(allowed, ",") {
			item = strings.TrimSpace(item)
			item = strings.TrimRight(item, "/")
			if item == "" {
				continue
			}
			// 支持完整 origin (https://example.com) 或仅域名 (example.com)
			if item == origin || item == originHost {
				return true
			}
		}
		return false
	}
	return cors.New(config)
}
