package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/subplatform"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/channel/subplatform").
		Use(middleware.Auth()).
		AddRoute(
			// 可选订阅平台清单: 渠道表单据此渲染平台下拉, 并把平台需要的条件(如代理)提示给用户。
			router.NewRoute("/platforms", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(listSubPlatforms),
		)
}

// listSubPlatforms 返回可用于建订阅渠道的平台。

// 与 /channel/account/signin/platforms 的分工:
//
//	前者回答"哪些账号平台能网页登录"，供应账号页使用；
//	本接口回答"哪些平台能一键建出订阅渠道"，供渠道表单使用。
//
// 两者不是同一份清单: 一个平台可以支持登录却还不能自动装配渠道(缺少地址/方言定义),
// 合并会让用户在渠道表单里看到一个建不出来的选项。
func listSubPlatforms(c *gin.Context) {
	all := subplatform.All()
	platforms := make([]gin.H, 0, len(all))
	for _, p := range all {
		platforms = append(platforms, gin.H{
			"id":               p.ID,
			"name":             p.Name,
			"description":      p.Description,
			"dialect":          p.Dialect,
			"account_platform": p.AccountPlatform,
			"base_url":         p.BaseURL,
		})
	}
	resp.Success(c, platforms)
}
