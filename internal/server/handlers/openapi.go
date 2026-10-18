package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/openapi.json").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(serveOpenAPI))
	router.NewGroupRouter("/docs").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(serveDocs))
}

// serveOpenAPI 运行时生成 OpenAPI 3.0 文档: 覆盖全部对外转发接口与探针,
// 供第三方客户端与 SDK 对接时自助查阅; 管理面 API 面向内置控制台, 不在此文档范围内。
func serveOpenAPI(c *gin.Context) {
	c.JSON(http.StatusOK, openAPISpec())
}

// openAPISpec 组装 OpenAPI 3.0.3 文档; 生成逻辑集中于此, 新增转发协议时同步补一条 path。
func openAPISpec() gin.H {
	auth := []gin.H{{"apiKey": []string{}}}
	return gin.H{
		"openapi": "3.0.3",
		"info": gin.H{
			"title":       "Switchyard LLM Gateway API",
			"description": "OpenAI / Anthropic 兼容的统一 LLM 网关转发接口。用分组名(客户端模型名)作为 model 参数, 由网关按路由策略选择上游渠道; 计费按实际用量结算。",
			"version":     conf.Version,
		},
		"servers": []gin.H{{"url": "/", "description": "当前站点"}},
		"security": auth,
		"paths": gin.H{
			"/v1/models": gin.H{
				"get": gin.H{
					"tags":        []string{"models"},
					"summary":     "列出可用模型",
					"description": "返回当前 API Key 归属者可用的客户端模型名(即分组名), 可直接作为 chat 等接口的 model 参数。",
					"security":    auth,
					"responses": gin.H{
						"200": gin.H{
							"description": "模型列表",
							"content": gin.H{
								"application/json": gin.H{
									"schema": gin.H{
										"type": "object",
										"properties": gin.H{
											"object": gin.H{"type": "string", "example": "list"},
											"data": gin.H{
												"type": "array",
												"items": gin.H{
													"type": "object",
													"properties": gin.H{
														"id":       gin.H{"type": "string", "example": "deepseek-v4-flash"},
														"object":   gin.H{"type": "string", "example": "model"},
														"owned_by": gin.H{"type": "string", "example": "switchyard"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"/v1/chat/completions": gin.H{
				"post": gin.H{
					"tags":        []string{"chat"},
					"summary":     "Chat Completions(OpenAI 兼容)",
					"description": "OpenAI Chat Completions 协议转发; stream=true 时以 SSE 返回。",
					"security":    auth,
					"requestBody": jsonBody("ChatCompletionRequest"),
					"responses":   completionResponses("ChatCompletionResponse"),
				},
			},
			"/v1/responses": gin.H{
				"post": gin.H{
					"tags":        []string{"chat"},
					"summary":     "Responses(OpenAI 兼容)",
					"description": "OpenAI Responses 协议转发。",
					"security":    auth,
					"requestBody": jsonBody("ResponsesRequest"),
					"responses":   completionResponses("ResponsesResponse"),
				},
			},
			"/v1/messages": gin.H{
				"post": gin.H{
					"tags":        []string{"chat"},
					"summary":     "Messages(Anthropic 兼容)",
					"description": "Anthropic Messages 协议转发; x-api-key 与 Authorization: Bearer 均可携带 API Key。",
					"security":    auth,
					"requestBody": jsonBody("AnthropicMessageRequest"),
					"responses":   completionResponses("AnthropicMessageResponse"),
				},
			},
			"/v1/images/generations": gin.H{
				"post": gin.H{
					"tags":        []string{"images"},
					"summary":     "生图(OpenAI 兼容)",
					"description": "OpenAI Images Generations 协议转发; 仅同步响应。",
					"security":    auth,
					"requestBody": jsonBody("ImageGenerationRequest"),
					"responses":   completionResponses("ImageGenerationResponse"),
				},
			},
			"/v1/images/edits": gin.H{
				"post": gin.H{
					"tags":        []string{"images"},
					"summary":     "改图(OpenAI 兼容)",
					"description": "OpenAI Images Edits 协议转发(multipart/form-data)。",
					"security":    auth,
					"requestBody": gin.H{
						"required": true,
						"content": gin.H{
							"multipart/form-data": gin.H{
								"schema": gin.H{"$ref": "#/components/schemas/ImageEditRequest"},
							},
						},
					},
					"responses": completionResponses("ImageGenerationResponse"),
				},
			},
			"/healthz": gin.H{
				"get": gin.H{
					"tags":        []string{"ops"},
					"summary":     "存活探针",
					"description": "无需鉴权; 返回进程运行时长、版本与数据库状态。",
					"security":    []gin.H{},
					"responses": gin.H{
						"200": gin.H{"description": "正常", "content": jsonContent(gin.H{
							"type": "object",
							"properties": gin.H{
								"status":          gin.H{"type": "string", "example": "ok"},
								"uptime_seconds":  gin.H{"type": "integer"},
								"version":         gin.H{"type": "string"},
								"db":              gin.H{"type": "string"},
								"active_requests": gin.H{"type": "integer"},
							},
						})},
						"503": gin.H{"description": "数据库降级"},
					},
				},
			},
			"/metrics": gin.H{
				"get": gin.H{
					"tags":        []string{"ops"},
					"summary":     "Prometheus 指标",
					"description": "Prometheus 文本格式指标; metrics_auth 设为 bearer 时需携带 metrics_token。",
					"security":    []gin.H{},
					"responses": gin.H{
						"200": gin.H{"description": "指标文本", "content": gin.H{"text/plain": gin.H{"schema": gin.H{"type": "string"}}}},
					},
				},
			},
		},
		"components": gin.H{
			"securitySchemes": gin.H{
				"apiKey": gin.H{
					"type":        "http",
					"scheme":      "bearer",
					"description": "控制台创建的 API Key, 以 Authorization: Bearer <key> 携带; Anthropic 协议同时接受 x-api-key 头。",
				},
			},
			"schemas": gin.H{
				"ChatCompletionRequest": gin.H{
					"type":     "object",
					"required": []string{"model", "messages"},
					"properties": gin.H{
						"model":  gin.H{"type": "string", "description": "客户端模型名(分组名)", "example": "deepseek-v4-flash"},
						"messages": gin.H{
							"type":        "array",
							"description": "对话消息列表",
							"items": gin.H{
								"type": "object",
								"required": []string{"role", "content"},
								"properties": gin.H{
									"role":    gin.H{"type": "string", "enum": []string{"system", "user", "assistant", "tool"}},
									"content": gin.H{"description": "文本或多模态内容", "oneOf": []gin.H{{"type": "string"}, {"type": "array", "items": gin.H{"type": "object"}}}},
								},
							},
						},
						"stream":           gin.H{"type": "boolean", "default": false, "description": "SSE 流式返回"},
						"temperature":      gin.H{"type": "number"},
						"top_p":            gin.H{"type": "number"},
						"max_tokens":       gin.H{"type": "integer"},
						"stop":             gin.H{"oneOf": []gin.H{{"type": "string"}, {"type": "array", "items": gin.H{"type": "string"}}}},
						"tools":            gin.H{"type": "array", "items": gin.H{"type": "object"}},
						"tool_choice":      gin.H{"description": "工具调用策略"},
						"response_format":  gin.H{"type": "object"},
						"reasoning_effort": gin.H{"type": "string", "description": "思考等级(部分上游支持)"},
					},
				},
				"ChatCompletionResponse": gin.H{
					"type": "object",
					"properties": gin.H{
						"id":      gin.H{"type": "string"},
						"object":  gin.H{"type": "string", "example": "chat.completion"},
						"created": gin.H{"type": "integer"},
						"model":   gin.H{"type": "string"},
						"choices": gin.H{"type": "array", "items": gin.H{"type": "object"}},
						"usage":   gin.H{"$ref": "#/components/schemas/Usage"},
					},
				},
				"ResponsesRequest": gin.H{
					"type":     "object",
					"required": []string{"model", "input"},
					"properties": gin.H{
						"model":  gin.H{"type": "string"},
						"input":  gin.H{"description": "输入文本或消息列表", "oneOf": []gin.H{{"type": "string"}, {"type": "array", "items": gin.H{"type": "object"}}}},
						"stream": gin.H{"type": "boolean", "default": false},
					},
				},
				"ResponsesResponse": gin.H{"type": "object", "properties": gin.H{"id": gin.H{"type": "string"}, "output": gin.H{"type": "array", "items": gin.H{"type": "object"}}, "usage": gin.H{"$ref": "#/components/schemas/Usage"}}},
				"AnthropicMessageRequest": gin.H{
					"type":     "object",
					"required": []string{"model", "messages"},
					"properties": gin.H{
						"model":    gin.H{"type": "string"},
						"messages": gin.H{"type": "array", "items": gin.H{"type": "object"}},
						"system":   gin.H{"description": "系统提示", "oneOf": []gin.H{{"type": "string"}, {"type": "array", "items": gin.H{"type": "object"}}}},
						"max_tokens": gin.H{"type": "integer", "description": "必填(上游协议要求)"},
						"stream":   gin.H{"type": "boolean", "default": false},
					},
				},
				"AnthropicMessageResponse": gin.H{"type": "object", "properties": gin.H{"id": gin.H{"type": "string"}, "content": gin.H{"type": "array", "items": gin.H{"type": "object"}}, "usage": gin.H{"$ref": "#/components/schemas/Usage"}}},
				"ImageGenerationRequest": gin.H{
					"type":     "object",
					"required": []string{"model", "prompt"},
					"properties": gin.H{
						"model":  gin.H{"type": "string"},
						"prompt": gin.H{"type": "string"},
						"n":      gin.H{"type": "integer", "default": 1},
						"size":   gin.H{"type": "string", "example": "1024x1024"},
					},
				},
				"ImageEditRequest": gin.H{
					"type":     "object",
					"required": []string{"model", "image"},
					"properties": gin.H{
						"model":  gin.H{"type": "string"},
						"image":  gin.H{"type": "string", "format": "binary"},
						"prompt": gin.H{"type": "string"},
					},
				},
				"ImageGenerationResponse": gin.H{
					"type": "object",
					"properties": gin.H{
						"created": gin.H{"type": "integer"},
						"data":    gin.H{"type": "array", "items": gin.H{"type": "object", "properties": gin.H{"url": gin.H{"type": "string"}, "b64_json": gin.H{"type": "string"}}}},
					},
				},
				"Usage": gin.H{
					"type": "object",
					"properties": gin.H{
						"prompt_tokens":     gin.H{"type": "integer"},
						"completion_tokens": gin.H{"type": "integer"},
						"total_tokens":      gin.H{"type": "integer"},
					},
				},
				"Error": gin.H{
					"type": "object",
					"properties": gin.H{
						"error": gin.H{
							"type": "object",
							"properties": gin.H{
								"message": gin.H{"type": "string"},
								"type":    gin.H{"type": "string"},
								"code":    gin.H{"type": "string"},
							},
						},
					},
				},
			},
		},
	}
}

// jsonBody 构造 application/json 请求体引用。
func jsonBody(schema string) gin.H {
	return gin.H{
		"required": true,
		"content":  jsonContent(gin.H{"$ref": "#/components/schemas/" + schema}),
	}
}

// jsonContent 把 schema 包成 application/json 内容对象。
func jsonContent(schema gin.H) gin.H {
	return gin.H{"application/json": gin.H{"schema": schema}}
}

// completionResponses 标准成功/失败响应集合。
func completionResponses(schema string) gin.H {
	return gin.H{
		"200": gin.H{"description": "成功(流式时为 SSE)", "content": jsonContent(gin.H{"$ref": "#/components/schemas/" + schema})},
		"400": gin.H{"description": "请求无效", "content": jsonContent(gin.H{"$ref": "#/components/schemas/Error"})},
		"401": gin.H{"description": "API Key 无效", "content": jsonContent(gin.H{"$ref": "#/components/schemas/Error"})},
		"402": gin.H{"description": "余额不足", "content": jsonContent(gin.H{"$ref": "#/components/schemas/Error"})},
		"429": gin.H{"description": "限流", "content": jsonContent(gin.H{"$ref": "#/components/schemas/Error"})},
		"502": gin.H{"description": "上游错误", "content": jsonContent(gin.H{"$ref": "#/components/schemas/Error"})},
	}
}
