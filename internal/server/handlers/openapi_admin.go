package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/openapi-admin.json").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(serveOpenAPIAdmin))
}

// serveOpenAPIAdmin 运行时生成管理面(控制台)API 的 OpenAPI 文档。
// 与转发面(openapi.go)分成两份是有意为之: 转发面对外集成方稳定, 管理面随控制台功能频繁增减,
// 混在一份里会让外部客户端看到一堆只有同源控制台用得上的 Cookie 接口。
func serveOpenAPIAdmin(c *gin.Context) {
	c.JSON(http.StatusOK, openAPIAdminSpec())
}

// adminNewLine 是文档字符串里的段落分隔, 写成常量以免在每个字面量里重复转义。
const adminNewLine = "\n"

func openAPIAdminSpec() gin.H {
	return gin.H{
		"openapi": "3.0.3",
		"info": gin.H{
			"title":   "Octopus 管理面 API (内置控制台)",
			"version": conf.Version,
			"description": "内置控制台使用的管理接口: 账号、渠道、分组、模型价格库、API Key、设置、日志、统计、运维、限流与审计。\n\n" +
				"鉴权: 除登录/注册/站点配置等公开端点外, 全部依赖会话 Cookie `auth`(HttpOnly, JWT), 由 /api/v1/user/login 下发。" +
				"本系统没有 CSRF 令牌机制, 跨站携带 Cookie 的写请求不受额外保护, 因此管理面只应由同源控制台调用。\n\n" +
				"角色: 路由分三档 —— 公开(无需登录)、登录即可(普通用户/渠道商/管理员都可用)、仅管理员(注册表里 Allow(admin) 收紧)。" +
				"登录即可的接口一律按数据归属(scope)隔离: 普通用户只看到自己名下的资源, 渠道商看到自有渠道, 管理员看到全站。\n\n" +
				"响应: 除流式(SSE)与文件下载接口外, 一律包在统一外壳里 `{ code, message, data }`; data 为空时该字段省略。" +
				"业务失败按语义返回 400/401/403/404/409/429/500/502, message 是可读原因, 会被控制台直接展示。",
		},
		"servers": []gin.H{{"url": "/", "description": "当前站点"}},
		"tags": []gin.H{
			{"name": "auth", "description": "账号、登录态与用户管理"},
			{"name": "channel", "description": "上游渠道、凭据、渠道模型、授权与发布"},
			{"name": "group", "description": "分组(客户端可见模型)与路由策略"},
			{"name": "model", "description": "上游模型价格库"},
			{"name": "apikey", "description": "API Key 与配额"},
			{"name": "setting", "description": "系统设置与整库导入导出"},
			{"name": "log", "description": "转发日志、请求体重放与实时流"},
			{"name": "stats", "description": "用量、账单与营收统计"},
			{"name": "ops", "description": "运维: 概览、对账、备份、健康探测、告警与指标令牌"},
			{"name": "ratelimit", "description": "限流策略与实时预算"},
			{"name": "audit", "description": "操作审计"},
			{"name": "update", "description": "在线升级"},
			{"name": "site", "description": "站点公开配置"},
		},
		"paths": openAPIAdminPaths(),
		"components": gin.H{
			"securitySchemes": gin.H{
				"session": gin.H{
					"type":        "apiKey",
					"in":          "cookie",
					"name":        "auth",
					"description": "登录后的会话 Cookie(HttpOnly, JWT)。改密、登出或管理员重置令牌版本都会让既有 Cookie 立即失效。",
				},
			},
			"schemas": openAPIAdminSchemas(),
		},
	}
}

// adminPath 组装一条需要会话 Cookie 的管理面操作。
//
// roleGuard 对应 router 注册表里的 Allow(...): true 表示该路由声明了角色白名单,
// 已登录但角色不符会拿到 403(白名单具体是谁写在 description 里);
// false 表示只挂了 Auth(), 登录即可访问 —— 后端按数据归属(scope)过滤可见范围。
func adminPath(tag, summary, description, bodyRef string, responses gin.H, roleGuard bool, params ...gin.H) gin.H {
	op := gin.H{
		"tags":      []string{tag},
		"summary":   summary,
		"security":  []gin.H{{"session": []string{}}},
		"responses": responses,
	}
	if description != "" {
		op["description"] = description
	}
	if bodyRef != "" {
		op["requestBody"] = jsonBody(bodyRef)
	}
	// 角色白名单路由要显式给出 403, 让调用方知道"登录了但角色不够"是另一种失败。
	if roleGuard {
		responses["403"] = gin.H{"description": "已登录但角色不符(不在该路由的角色白名单内)", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})}
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op
}

// adminPathPublic 组装一条不需要登录的接口(登录、注册、站点配置等)。
func adminPathPublic(tag, summary, description, bodyRef string, responses gin.H) gin.H {
	op := adminPath(tag, summary, description, bodyRef, responses, false)
	op["security"] = []gin.H{}
	return op
}

// adminOk 无返回体的成功: 后端省略 data 字段。
func adminOk() gin.H {
	return adminEnvelope("操作成功", nil)
}

// adminItem 成功响应, data 为单个对象引用。
func adminItem(desc, ref string) gin.H {
	return adminEnvelope(desc, gin.H{"$ref": "#/components/schemas/" + ref})
}

// adminArray 成功响应, data 为对象数组引用。
func adminArray(desc, ref string) gin.H {
	return adminEnvelope(desc, gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/" + ref}})
}

// adminItems 成功响应, data = { items: [...] }, 可选 total。
func adminItems(desc, ref string, withTotal bool) gin.H {
	props := gin.H{"items": gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/" + ref}}}
	if withTotal {
		props["total"] = gin.H{"type": "integer", "description": "符合过滤条件的总条数"}
	}
	return adminEnvelope(desc, gin.H{"type": "object", "properties": props})
}

// adminScalar 成功响应, data 是基础类型(字符串/数字/布尔)。
func adminScalar(desc, typ string) gin.H {
	return adminEnvelope(desc, gin.H{"type": typ})
}

// itemsArray 成功响应, data = { items: [...] }: 后端把一批结果包在 items 下,
// 以便与带 total 的分页形态保持同一个外壳形状。
func itemsArray(desc, ref string) gin.H {
	return adminEnvelope(desc, gin.H{"type": "object", "properties": gin.H{
		"items": gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/" + ref}},
	}})
}

// noContent 204 空响应体: 中止在途请求、清理日志这类只表示执行过的接口不套外壳。
func noContent() gin.H {
	return gin.H{
		"204": gin.H{"description": "已执行, 无响应体"},
		"400": gin.H{"description": "路径参数不合法", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
		"401": gin.H{"description": "未登录或会话失效", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
		"403": gin.H{"description": "已登录但角色不符(不在该路由的角色白名单内)", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
	}
}

// adminEnvelope 把 data 装进统一外壳 { code, message, data }。
// 这里只声明 200 与通用失败码; 角色白名单带来的 403 由 adminPath 按 roleGuard 追加。
func adminEnvelope(desc string, data gin.H) gin.H {
	props := gin.H{
		"code":    gin.H{"type": "integer", "example": 200},
		"message": gin.H{"type": "string", "example": "success"},
	}
	if data == nil {
		props["data"] = gin.H{"nullable": true, "description": "本接口无业务返回体, 该字段省略"}
	} else {
		props["data"] = data
	}
	responses := gin.H{
		"200": gin.H{"description": desc, "content": jsonContent(gin.H{"type": "object", "properties": props})},
		"400": gin.H{"description": "请求参数不合法, message 给出具体原因", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
		"401": gin.H{"description": "未登录或会话失效", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
		"500": gin.H{"description": "服务端错误", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
	}
	return responses
}

// pathID 路径上的主键参数。
func pathID(desc string) gin.H {
	return gin.H{"name": "id", "in": "path", "required": true, "description": desc, "schema": gin.H{"type": "integer"}}
}

// query 查询参数: name/description/schema 三件套, extra 可选补 schema 键(default、enum 等)。
func query(name, desc, typ string, required bool, extra gin.H) gin.H {
	schema := gin.H{"type": typ}
	if typ == "int" {
		schema = gin.H{"type": "integer"}
	}
	for k, v := range extra {
		schema[k] = v
	}
	return gin.H{"name": name, "in": "query", "required": required, "description": desc, "schema": schema}
}

// intQuery 整型查询参数(带默认值说明)。
func intQuery(name, desc string, def int) gin.H {
	return query(name, desc, "int", false, gin.H{"default": def})
}

// sseResponses 流式接口: text/event-stream, 不套统一外壳。
func sseResponses(desc string) gin.H {
	return gin.H{
		"200": gin.H{"description": desc, "content": gin.H{"text/event-stream": gin.H{"schema": gin.H{"type": "string"}}}},
		"401": gin.H{"description": "未登录或会话失效"},
	}
}
