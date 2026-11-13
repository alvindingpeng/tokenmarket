
package handlers

// 上游账号 JSON 的导入接口。
//
// 放在渠道路由下而不是新开一组: 账号凭据就是渠道下的 ChannelKey, 归属校验、权限与
// 渠道编辑完全一致 —— 谁有权改这个渠道, 谁才有权往里塞凭据。

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/channel/account").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			// 支持的平台与字段形状: 每种平台的具体键名见 model/account_json.go 的解析分支。
			router.NewRoute("/import", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(importChannelAccount),
		).
		AddRoute(
			router.NewRoute("/:id/refresh", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(refreshChannelAccount),
		)
}

type importChannelAccountRequest struct {
	ChannelID int    `json:"channel_id"`
	Platform  string `json:"platform"`
	Name      string `json:"name"`
	Raw       string `json:"raw"`
	Enabled   *bool  `json:"enabled"`
	Priority  int    `json:"priority"`
}

// importChannelAccount 导入一份账号 JSON 作为渠道凭据。
//
// 返回体里 refreshed_now 与 warning 两件事要分开看:
//   - refreshed_now=true 表示导入时已经刷过一次令牌, 账号当场可用;
//   - 带 warning 表示账号已落库但首刷失败 —— 这是成功响应, 不是错误,
//     因为账号 JSON 本身可能没问题(只是上游此刻不通)。若把它当错误返回, 使用者会重复导入,
//     然后撞上"凭据名已存在"。
func importChannelAccount(c *gin.Context) {
	var request importChannelAccountRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	userID, role := middleware.CurrentUser(c)
	channel, err := op.ChannelGet(request.ChannelID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "渠道不存在")
		return
	}
	if !(model.Scope{ID: userID, Role: role}).Owns(channel.UserID) {
		// 不归属时按 404 处理: 不暴露渠道存在性。
		resp.Error(c, http.StatusNotFound, "渠道不存在")
		return
	}

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	result, err := op.AccountImport(c.Request.Context(), channel.ID, op.AccountImportSpec{
		Platform: request.Platform,
		Name:     request.Name,
		Raw:      request.Raw,
		Enabled:  enabled,
		Priority: request.Priority,
	}, time.Now())

	// 首刷失败是"部分成功": 账号已落库, 只把警告带回去, 不当成请求失败。
	var refreshWarning *op.AccountImportRefreshWarning
	if err != nil && !errors.As(err, &refreshWarning) {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	body := gin.H{
		"channel_key_id": result.ChannelKeyID,
		"name":           result.Name,
		"platform":       result.Platform,
		"kind":           result.Kind,
		"email":          result.Email,
		"refreshed_now":  result.RefreshedNow,
		// 续期能力必须让界面能展示: cookie 类账号没有 refresh 概念,
		// 失效后只能人工重导, 这一点若不说清, 使用者会以为"系统会自己处理"。
		"auto_renewable": result.Kind == model.AccountKindOAuth,
	}
	if result.ExpiresAt != nil {
		body["expires_at"] = result.ExpiresAt.Format(time.RFC3339)
	}
	if refreshWarning != nil {
		body["warning"] = refreshWarning.Error()
	}
	resp.Success(c, body)
}

// refreshChannelAccount 手动触发一次刷新, 用于运营在导入后立刻验证账号是否可用。
func refreshChannelAccount(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "凭据 ID 非法")
		return
	}
	userID, role := middleware.CurrentUser(c)
	channelKey, err := op.ChannelKeyLookup(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "凭据不存在")
		return
	}
	channel, err := op.ChannelGet(channelKey.ChannelID)
	if err != nil || !(model.Scope{ID: userID, Role: role}).Owns(channel.UserID) {
		resp.Error(c, http.StatusNotFound, "凭据不存在")
		return
	}
	if channelKey.AccountKind != model.AccountKindOAuth {
		// cookie 类没有续期能力: 明确拒绝而不是"刷了个空"。
		resp.Error(c, http.StatusBadRequest, "该凭据不是可自动续期的账号(cookie 类账号无法刷新)")
		return
	}

	if err := op.AccountRefresh(c.Request.Context(), id, time.Now()); err != nil {
		var persistErr *op.AccountRefreshPersistError
		if errors.As(err, &persistErr) {
			// 回写失败必须用最高级别提示: 上游已轮换旧令牌而新值没落库, 这个账号可能已经废了。
			resp.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"refreshed": true})
}
