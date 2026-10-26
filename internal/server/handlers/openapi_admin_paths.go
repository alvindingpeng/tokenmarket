package handlers

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// openAPIAdminPaths 管理面路径表, 逐条对照各 GroupRouter 的 init() 注册写出。
// 拆成两半只为让单文件保持可读: A 是账号到 API Key, B 是设置到站点配置。
//
// 鉴权口径必须与注册表一致, 否则文档会误导调用方:
//   - 公开:   该 GroupRouter 没挂 middleware.Auth()(登录、注册、站点配置)。
//   - 登录即可: 挂了 Auth() 但路由没有 Allow(...) —— 普通用户也能调, 后端按数据归属(scope)
//     过滤, 各人只看到自己名下的资源。
//   - 角色白名单: 路由显式 Allow(...), 已登录但角色不符返回 403, 由 adminPath 的 roleGuard
//     统一补上 403 响应。
func openAPIAdminPaths() gin.H {
	paths := openAPIAdminPathsAuth()
	for path, item := range openAPIAdminPathsOps() {
		paths[path] = item
	}
	applyStepUpContract(paths)
	return paths
}

// stepUpProtectedRoutes 受 step-up 保护的高危写/敏感读路由清单(文档侧的单一事实源):
// 与各 handler 注册处的 middleware.RequireStepUp() 一一对应。value 是"为什么高危",
// 会追加进该接口的说明里。TestStepUpOpenAPIContractMatchesEnforcement 拿它逐条对照
// 真实路由(不带凭证必须 428)与文档条目(必须标了 X-Step-Up-Token 与 428), 三方不一致即测试失败。
//
// 说明: 清单后处理作用在合并后的整张路径表上, 本文件不改 schemas、也不改
// openapi_admin_paths_ops.go —— 与其它 agent 的同文件改动天然错开。
var stepUpProtectedRoutes = map[string]string{
	"POST /api/v1/user/manage/create":          "建号可指定角色与初始余额",
	"POST /api/v1/user/manage/update":          "余额调整与账号状态变更",
	"POST /api/v1/user/manage/reset-password":  "重置他人密码",
	"DELETE /api/v1/user/manage/delete/{id}":   "删号",
	"POST /api/v1/setting/set":                 "系统设置写入, 改全局行为",
	"POST /api/v1/setting/import":              "备份导入, 整库覆盖",
	"GET /api/v1/setting/export":               "整库导出, 含凭据",
	"GET /api/v1/audit/export":                 "审计导出",
	"GET /api/v1/log/export":                   "日志导出, 可还原使用行为",
	"GET /api/v1/stats/billing/records/export": "计费明细导出, 逐笔资金",
	"POST /api/v1/settlement/generate":         "结算生成, 决定打款口径",
	"POST /api/v1/dispute/review":              "争议裁决",
	"POST /api/v1/withdrawal/review":           "提现审核",
	"POST /api/v1/withdrawal/mark-paid":        "提现打款确认",
	"POST /api/v1/withdrawal/rollback":         "提现回滚",
	"POST /api/v1/redemption/batch/create":     "兑换码批量生成, 等价发钱",
	"POST /api/v1/redemption/code/disable":     "兑换码作废",
	"POST /api/v1/ops/billing-recovery/run":    "计费恢复, 资金补偿执行",
	"POST /api/v1/ops/backup/run":              "备份生成",
	"POST /api/v1/ops/log-lifecycle/run":       "日志归档/清理执行",
	"POST /api/v1/ops/auth-secret/rotate":      "登录签名密钥轮换, 全员下线",
	"GET /api/v1/ops/metrics/token":            "指标令牌读取, 敏感凭据",
	"POST /api/v1/ops/metrics/token/rotate":    "指标令牌轮换",
	"POST /api/v1/update":                      "系统更新",
}

// stepUpHeaderParam X-Step-Up-Token 请求头的参数定义: 短期验证凭证的完整语义写在这里,
// 各受保护接口引用同一段说明。
func stepUpHeaderParam() gin.H {
	return gin.H{
		"name":     "X-Step-Up-Token",
		"in":       "header",
		"required": true,
		"description": "step-up 短期凭证(JWT, 默认 5 分钟): 由 POST /api/v1/user/step-up 用登录密码换取。" +
			"凭证绑定 用户 + token_version + 当前登录会话: 改密、禁用、登出或换会话任一发生即失效, 期满也失效。" +
			"缺失或失效返回 428(error_code=step_up_required)且请求不会执行 —— 客户端应弹密码确认后把原请求原样重发一次。",
		"schema": gin.H{"type": "string"},
	}
}

// stepUp428 缺 step-up 凭证的响应: 不套 AdminEnvelopeError 的 $ref, 免得本条说明依赖
// schemas 文件里的条目(那个文件由其它 agent 维护)。
func stepUp428() gin.H {
	return gin.H{
		"description": "缺少、过期或不属于当前登录会话的 X-Step-Up-Token: 请求不会执行(零副作用)。" +
			"响应带 error_code=step_up_required, 调用方(含前端)据此弹密码确认后原样重发一次。",
		"content": jsonContent(gin.H{
			"type": "object",
			"properties": gin.H{
				"code":       gin.H{"type": "integer", "example": 428},
				"message":    gin.H{"type": "string", "example": "Step-up verification required"},
				"error_code": gin.H{"type": "string", "example": "step_up_required"},
			},
		}),
	}
}

// applyStepUpContract 给受保护路由补上 X-Step-Up-Token 头参数、428 响应与一句高危说明。
// 只标记已进文档的条目, 不硬造路径(提现/结算/兑换码等尚未进文档的路由保持原状)。
func applyStepUpContract(paths gin.H) {
	for key, reason := range stepUpProtectedRoutes {
		method, path, _ := strings.Cut(key, " ")
		route, ok := paths[path].(gin.H)
		if !ok {
			continue
		}
		operation, ok := route[strings.ToLower(method)].(gin.H)
		if !ok {
			continue
		}
		switch params := operation["parameters"].(type) {
		case []gin.H:
			operation["parameters"] = append(params, stepUpHeaderParam())
		default:
			operation["parameters"] = []gin.H{stepUpHeaderParam()}
		}
		if description, ok := operation["description"].(string); ok && description != "" {
			operation["description"] = description + adminNewLine + adminNewLine +
				"高危操作(" + reason + "): 除登录态外还要求 X-Step-Up-Token 短期凭证, 语义见 POST /api/v1/user/step-up。"
		}
		if responses, ok := operation["responses"].(gin.H); ok {
			responses["428"] = stepUp428()
		}
	}
}

// stepUpResponses 二次验证端点的响应: 200 换凭证, 401 口令不对, 429 触发防爆破。
func stepUpResponses() gin.H {
	responses := adminItem("验证通过, data 为短期 step-up 凭证", "StepUpCredential")
	responses["401"] = gin.H{"description": "密码不正确", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})}
	responses["429"] = gin.H{"description": "密码验证被限流(与登录同一套防爆破闸门), 按 Retry-After 等待后再试", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})}
	return responses
}

// openAPIAdminPathsAuth 账号、渠道、分组、模型价格库与 API Key。
func openAPIAdminPathsAuth() gin.H {
	return gin.H{
		// ---------------- 账号与登录态 ----------------
		"/api/v1/user/login": gin.H{
			"post": adminPathPublic("auth", "登录",
				"用户名密码登录, 成功后用 Set-Cookie 下发 HttpOnly 会话 Cookie(auth), 后续管理面请求由浏览器自动携带。"+adminNewLine+adminNewLine+
					"防爆破: 按 IP 与账号两个维度独立限流, 任一超限返回 429; 开启维护模式时非管理员返回 503。",
				"LoginRequest", adminItem("登录成功, data 为账号信息", "UserView")),
		},
		"/api/v1/user/step-up": gin.H{
			"post": adminPath("auth", "高危操作二次验证(step-up)",
				"用当前账号的密码重新确认身份, 换一枚短期 step-up 凭证。凭证绑定 用户 + token_version + 当前登录会话: 改密、禁用、登出或换会话任一发生即失效, 5 分钟后过期。"+
					adminNewLine+adminNewLine+
					"受保护的高危写操作(余额调整与用户管理、提现审核/打款/回滚、备份导入、整库/审计/日志/计费明细导出、结算生成、计费恢复、系统设置)要求把凭证放进 X-Step-Up-Token 头提交; 缺失或失效返回 428 且响应带 error_code=step_up_required, 请求不会执行 —— 客户端应弹密码确认后把原请求原样重发一次。读列表类接口不需要。"+
					adminNewLine+adminNewLine+
					"防爆破: 密码验证与登录共用同一套限流闸门(计数独立), 超限 429 + Retry-After。审计只记结果, 不含密码与凭证。",
				"StepUpRequest", stepUpResponses(), false),
		},
		"/api/v1/user/register": gin.H{
			"post": adminPathPublic("auth", "注册用户",
				"自助注册普通用户, 受注册开关与注册审批设置控制, 开关关闭时 403。角色固定为 user, 请求体不含角色字段以防自提权。",
				"RegisterRequest", adminEnvelope("注册成功", gin.H{
					"type": "object",
					"properties": gin.H{
						"id":       gin.H{"type": "integer"},
						"username": gin.H{"type": "string"},
						"status":   gin.H{"type": "string", "enum": []string{"active", "pending"}, "description": "开启注册审批时为 pending, 需管理员激活"},
					},
				})),
		},
		"/api/v1/user/register-reseller": gin.H{
			"post": adminPathPublic("auth", "注册渠道商",
				"自助注册渠道商(reseller), 开关为渠道商注册设置; 其余口径同 /register。",
				"RegisterRequest", adminEnvelope("注册成功", gin.H{
					"type": "object",
					"properties": gin.H{
						"id":       gin.H{"type": "integer"},
						"username": gin.H{"type": "string"},
						"status":   gin.H{"type": "string", "enum": []string{"active", "pending"}},
					},
				})),
		},
		"/api/v1/user/register-config": gin.H{
			"get": adminPathPublic("auth", "注册开关配置",
				"登录页据此决定要不要显示注册与渠道商注册入口, 无需登录。", "",
				adminEnvelope("注册开关", gin.H{
					"type": "object",
					"properties": gin.H{
						"user_enabled":      gin.H{"type": "boolean", "description": "是否允许注册普通用户"},
						"reseller_enabled":  gin.H{"type": "boolean", "description": "是否允许注册渠道商"},
						"approval_required": gin.H{"type": "boolean", "description": "为真时注册成功后提示等待审批而非直接登录"},
					},
				})),
		},
		"/api/v1/user/status": gin.H{
			"get": adminPath("auth", "当前账号",
				"返回当前会话账号。令牌版本落后(改过密码或登出过)或账号被停用一律 401。", "",
				adminItem("当前账号", "UserView"), false),
		},
		"/api/v1/user/logout": gin.H{
			"post": adminPath("auth", "登出",
				"服务端登出: 自增本账号的令牌版本, 使该账号所有端的既有 Cookie 立即失效。只想退本端的话客户端自行清除 Cookie 即可。", "",
				adminEnvelope("已登出", gin.H{"type": "string", "example": "logged out"}), false),
		},
		"/api/v1/user/change-password": gin.H{
			"post": adminPath("auth", "修改密码",
				"校验旧密码后修改; 成功后令牌版本自增, 当前会话需重新登录。",
				"ChangePasswordRequest", adminEnvelope("修改成功", gin.H{"type": "string", "example": "password changed successfully"}), false),
		},
		"/api/v1/user/change-username": gin.H{
			"post": adminPath("auth", "修改用户名",
				"改名同样会自增令牌版本, 需要重新登录。",
				"ChangeUsernameRequest", adminEnvelope("修改成功", gin.H{"type": "string", "example": "username changed successfully"}), false),
		},
		"/api/v1/user/manage/list": gin.H{
			"get": adminPath("auth", "账号列表",
				"全站账号(不含密码与令牌版本), 按 ID 升序。仅管理员。", "",
				adminArray("账号列表", "UserView"), true),
		},
		"/api/v1/user/manage/create": gin.H{
			"post": adminPath("auth", "创建账号",
				"管理员直接建号, 角色与初始状态由请求体决定。",
				"UserManageRequest", adminItem("新建账号", "UserView"), true),
		},
		"/api/v1/user/manage/update": gin.H{
			"post": adminPath("auth", "更新账号",
				"按 id 更新角色、状态与余额; balance 为指针字段, 未提交即不改动。"+adminNewLine+adminNewLine+
					"改角色后该账号的旧 Cookie 立刻按新角色受限 —— 每次请求都重新读取库内角色, 不信任令牌里的角色。",
				"UserManageRequest", adminItem("更新后的账号", "UserView"), true),
		},
		"/api/v1/user/manage/reset-password": gin.H{
			"post": adminPath("auth", "重置密码",
				"管理员重置指定账号密码, 同时自增令牌版本踢掉其所有登录态。",
				"UserManageRequest", adminEnvelope("重置成功", gin.H{"type": "string", "example": "password reset successfully"}), true),
		},
		"/api/v1/user/manage/delete/{id}": gin.H{
			"delete": adminPath("auth", "删除账号",
				"按路径 id 删除账号; 不允许删除自己(400)。", "",
				adminOk(), true, pathID("账号 ID")),
		},

		// ---------------- 渠道 ----------------
		"/api/v1/channel/stats": gin.H{
			"get": adminPath("channel", "渠道列表(含统计)",
				"渠道概览: 名称、启停、发布状态、逐模型统计与累计用量。控制台渠道页只消费这一条查询, 完整配置在点开编辑时按 detail/{id} 单独取。"+adminNewLine+adminNewLine+
					"渠道商与管理员各自只看得到自有渠道, 管理员看到全站。没有单独的 list 接口 —— 这一条就是列表。", "",
				adminArray("渠道统计列表", "ChannelStats"), true),
		},
		"/api/v1/channel/detail/{id}": gin.H{
			"get": adminPath("channel", "渠道详情",
				"整份渠道配置: 上游地址与各协议路径、凭据(含密钥)、模型名、授权组合、方言与轮换策略。凭据属敏感数据, 仅归属者与管理员可读, 越权 404。", "",
				adminItem("渠道配置", "ChannelDetail"), true, pathID("渠道 ID")),
		},
		"/api/v1/channel/egress-health/{id}": gin.H{
			"get": adminPath("channel", "渠道出口健康",
				"返回渠道出口池的实时成功率、平均首响应耗时与冷却状态; 不返回代理 URL 或凭据。无样本时明确表示为冷出口。", "",
				adminItem("出口健康", "ChannelEgressHealth"), true, pathID("渠道 ID")),
		},
		"/api/v1/channel/stats/daily/{id}": gin.H{
			"get": adminPath("channel", "渠道按日统计",
				"渠道本身与其各模型的按日用量曲线。", "",
				adminItem("按日统计", "ChannelDailyStats"), true,
				pathID("渠道 ID"), intQuery("days", "回看天数, 超出 1-90 一律按 14 处理", 14)),
		},
		"/api/v1/channel/grants": gin.H{
			"get": adminPath("channel", "可授权模型候选",
				"分组页挑选成员用: 按当前身份可见范围列出 (渠道, 模型, 凭据) 组合候选, 附带供货价与用户价。非归属者视角下凭据名脱敏、只含已上架模型。", "",
				adminArray("授权候选", "ChannelGrantCandidate"), false),
		},
		"/api/v1/channel/create": gin.H{
			"post": adminPath("channel", "创建渠道",
				"一次提交整份配置(渠道 + 凭据 + 模型名 + 授权)。模型价按价格库自动补齐供货价; model_auto_add 为真时保存后立即试拉上游并增量并入模型与授权, 只增不删。",
				"ChannelDetail", adminItem("创建后的渠道", "ChannelDetail"), true),
		},
		"/api/v1/channel/update": gin.H{
			"post": adminPath("channel", "更新渠道",
				"请求体带 id。凭据、模型与授权按名称匹配做增删改, 未提交的凭据视为删除。保存后按 model_auto_add 决定是否再次自动并入。",
				"ChannelDetail", adminItem("更新后的渠道", "ChannelDetail"), true),
		},
		"/api/v1/channel/enable": gin.H{
			"post": adminPath("channel", "启用或停用渠道",
				"停用后该渠道不参与选路, 但配置与历史统计保留。",
				"ChannelEnableRequest", adminOk(), true),
		},
		"/api/v1/channel/delete/{id}": gin.H{
			"delete": adminPath("channel", "删除渠道",
				"级联删除其凭据、渠道模型与授权, 引用这些授权的分组成员随之移除。", "",
				adminOk(), true, pathID("渠道 ID")),
		},
		"/api/v1/channel/fetch-model": gin.H{
			"post": adminPath("channel", "试拉上游模型",
				"用请求携带的渠道配置与凭据同时探测 OpenAI 与 Anthropic 两侧, 返回模型名及各自支持的协议。"+adminNewLine+adminNewLine+
					"渠道尚未保存时也要能试拉, 故请求体带整份配置而不是 id; 缺 base_url 或过滤正则非法为 400, 两侧都探不通为 502 并带上上游原文。",
				"ChannelFetchModelRequest", adminEnvelope("探测到的模型", gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/ChannelFetchModel"}}), true),
		},
		"/api/v1/channel/publish": gin.H{
			"post": adminPath("channel", "发布或取消发布渠道",
				"shared=true 时生成唯一 share_code, 用户侧(模型列表与分享页)据此看到该渠道已上架的模型; false 收回。",
				"ChannelPublishRequest", adminEnvelope("发布状态", gin.H{
					"type": "object",
					"properties": gin.H{
						"id":         gin.H{"type": "integer"},
						"shared":     gin.H{"type": "boolean"},
						"share_code": gin.H{"type": "string", "description": "未发布时为空串"},
					},
				}), true),
		},
		"/api/v1/channel/models/{id}": gin.H{
			"get": adminPath("channel", "渠道模型上架配置",
				"发布管理页用: 逐个模型的上架标记、供货价与媒体价。", "",
				adminEnvelope("上架配置", gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/ChannelModelListing"}}), true,
				pathID("渠道 ID")),
		},
		"/api/v1/channel/models/update": gin.H{
			"post": adminPath("channel", "更新渠道模型上架与供货价",
				"整体提交上架配置, 返回实际变更清单并逐项写入审计。"+adminNewLine+adminNewLine+
					"改这里会同步影响引用这些模型的分组的用户价 —— 用户价是供货价按上浮比例算出来的, 不在分组上单独存价。",
				"ChannelModelUpdateRequest", adminEnvelope("变更清单", gin.H{
					"type": "object",
					"properties": gin.H{
						"changed": gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/ListingChange"}, "description": "空数组表示没有实际变化"},
					},
				}), true),
		},

		"/api/v1/channel/models/reprice": gin.H{
			"post": adminPath("channel", "一键改价预览",
				"按统一倍率算出渠道内每个模型的目标供货价(官方价 × 倍率), 不写库。"+adminNewLine+adminNewLine+
					"只处理文本模型: 官方价格表是每百万 token 的口径, 生图/生视频模型按张与秒计价, 用 token 价覆盖媒体价等于改错收入口径, 故一律跳过。"+adminNewLine+
					"跳过的行在 skipped 里给出原因: no_official_price(价格库无此模型) / media_model(媒体形态模型) / over_cap(超出供货价上限)。",
				"ChannelRepriceRequest", adminEnvelope("改价预览", ref("ChannelRepriceResult")), true),
		},
		"/api/v1/channel/models/reprice/apply": gin.H{
			"post": adminPath("channel", "一键改价执行",
				"按预览结果写库, 只改供货价, 不动上架状态。"+adminNewLine+adminNewLine+
					"names 省略或留空表示改全部可改的行; 指定时只改这些模型。实际写入条数在 updated 中给出。"+adminNewLine+
					"改价会同步重建分组快照(用户价 = 供货价 × 上浮比例), 并写入 channel.model-reprice 审计。",
				"ChannelRepriceRequest", adminEnvelope("改价结果", ref("ChannelRepriceResult")), true),
		},
		"/api/v1/channel/breaker/reset": gin.H{
			"post": adminPath("channel", "解除渠道熔断",
				"按名称手动清零渠道凭据或渠道模型的错误计数并清掉熔断截止时间。"+adminNewLine+adminNewLine+
					"自动熔断按时间自愈, 上游修好(换 Key, 补额度)后用它立刻恢复, 不必干等熔断到期。"+adminNewLine+
					"key_names 与 model_names 至少要给一个, 两者都为空为 400; 返回实际解锁的条数。"+adminNewLine+
					"熔断的参数(阈值与时长)在系统设置的运维参数页配置。",
				"ChannelBreakerResetRequest", adminEnvelope("解锁条数", gin.H{"type": "object", "properties": gin.H{"reset": gin.H{"type": "integer"}}}), true),
		},
		// ---------------- 分组(客户端可见模型) ----------------
		"/api/v1/group/list": gin.H{
			"get": adminPath("group", "分组列表",
				"每个分组附带实时路由状态(runtime)。普通用户只看自己的分组, 管理员看全站。", "",
				adminArray("分组列表", "GroupResponse"), false),
		},
		"/api/v1/group/get/{id}": gin.H{
			"get": adminPath("group", "分组详情",
				"按 id 取单个分组; 不在当前身份可见范围内返回 404。", "",
				adminItem("分组详情", "GroupResponse"), false, pathID("分组 ID")),
		},
		"/api/v1/group/events": gin.H{
			"get": adminPath("group", "分组变更实时流",
				"SSE: 分组的创建、更新与删除事件推给控制台, 用于免轮询刷新列表与运行状态; 事件体即 groupResponse。", "",
				sseResponses("分组变更事件流"), false),
		},
		"/api/v1/group/metrics/{id}": gin.H{
			"get": adminPath("group", "分组成员实时选路指标",
				"每个成员的滑动平均首响应耗时与成功率, 与选路打分同源; 控制台约 20 秒轮询一次。", "",
				adminEnvelope("成员指标", gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/GroupMemberMetric"}}), false,
				pathID("分组 ID")),
		},
		"/api/v1/group/create": gin.H{
			"post": adminPath("group", "创建分组",
				"分组名即客户端提交给网关的 model 参数。成员按渠道授权主键引用, 提交顺序即优先级顺序; relay_config 留零值由后端补默认。",
				"GroupCreateRequest", adminItem("新建分组", "GroupResponse"), false),
		},
		"/api/v1/group/update/{id}": gin.H{
			"post": adminPath("group", "更新分组",
				"字段全部是指针语义: 只提交要变更的项, 未提交的保持原状。items 一旦提交即整体替换; active_item_id=0 表示取消手动指定成员。",
				"GroupUpdateRequest", adminItem("更新后的分组", "GroupResponse"), false, pathID("分组 ID")),
		},
		"/api/v1/group/delete/{id}": gin.H{
			"delete": adminPath("group", "删除分组",
				"删除后客户端再用该模型名请求即 404。", "",
				adminOk(), false, pathID("分组 ID")),
		},

		// ---------------- 模型价格库 ----------------
		"/api/v1/model/list": gin.H{
			"get": adminPath("model", "模型价格库列表",
				"全站模型供货价目录(每百万 token 的四类单价), 供渠道模型定价参照。", "",
				adminArray("价格库", "LLMInfo"), false),
		},
		"/api/v1/model/create": gin.H{
			"post": adminPath("model", "新增模型价",
				"名称自动转小写去空格; 空名或重名 400。",
				"LLMInfo", adminItem("新建模型价", "LLMInfo"), true),
		},
		"/api/v1/model/update": gin.H{
			"post": adminPath("model", "更新模型价",
				"按 name 更新四类单价。",
				"LLMInfo", adminItem("更新后的模型价", "LLMInfo"), true),
		},
		"/api/v1/model/delete": gin.H{
			"post": adminPath("model", "删除模型价",
				"请求体只需 name。",
				"ModelNameRequest", adminOk(), true),
		},
		"/api/v1/model/update-price": gin.H{
			"post": adminPath("model", "同步价格源",
				"从价格源拉取并更新已知模型的单价; 耗时取决于上游, 建议错峰执行。", "",
				adminOk(), true),
		},
		"/api/v1/model/rebuild-price": gin.H{
			"post": adminPath("model", "重建价格库",
				"先清理幽灵模型(有价无引用), 再按价格源全量重写单价, 返回处理条数。"+adminNewLine+adminNewLine+
					"渠道上手工设置过的价格会被覆盖, 慎用。", "",
				adminEnvelope("重建条数", gin.H{"type": "object", "properties": gin.H{"count": gin.H{"type": "integer"}}}), true),
		},
		"/api/v1/model/auto-add-rule/list": gin.H{
			"get": adminPath("model", "模型级自动添加条件",
				"返回当前登录用户为各模型单独设置的自动添加条件。"+adminNewLine+adminNewLine+
					"条件按 (用户, 模型) 存放: 每个人设的各人生效, 互不覆盖 —— 同一个模型在不同人眼里值得不值得收,"+adminNewLine+
					"可以各按各的账算。字段全为空串表示该项未单独设置, 回落系统设置里的全局默认。",
				"", adminArray("自动添加条件", "ModelAutoAddRule"), false),
		},
		"/api/v1/model/auto-add-rule/save": gin.H{
			"post": adminPath("model", "保存模型级自动添加条件",
				"逐字段覆盖: 留空的项继续跟随全局, 填了的项才覆盖该模型。"+adminNewLine+adminNewLine+
					"mode 为 on 表示该模型强制并入(跳过其余全部条件), off 表示永不并入。"+adminNewLine+
					"非法数字, 非法正则或非法开关值一律拒绝, 不静默回落 —— 条件填错了等于没设, 必须让调用方知道。"+adminNewLine+
					"全部字段留空等价于删除这条条件; 请求体里的 user_id 会被忽略, 只写登录者自己那一份。",
				"ModelAutoAddRule", adminItem("保存后的条件", "ModelAutoAddRule"), true),
		},
		"/api/v1/model/auto-add-rule/delete": gin.H{
			"post": adminPath("model", "删除模型级自动添加条件",
				"请求体只需 name; 删除后该模型回到全局默认。幂等: 条件本就不存在时同样返回成功。",
				"ModelNameRequest", adminOk(), true),
		},
		"/api/v1/model/capability": gin.H{
			"get": adminPath("model", "模型能力登记",
				"官方登记的模型输入输出模态与规模上限(models.dev 自动同步 + 人工补录)。"+adminNewLine+adminNewLine+
					"它决定探测渠道模型时预勾选哪些协议位: 只输出图像的判为生图, 只输出视频的判为生视频, 图文混合的既不判生图也不预勾选 —— "+adminNewLine+
					"混合模型一旦被判生图就会按张计费, 聊天流量会被算成图片。"+adminNewLine+
					"媒体单价官方不提供, 仍由管理员按每张/每秒填写, 与本登记无关。",
				"", adminArray("能力登记", "ModelCapability"), false),
		},
		"/api/v1/model/capability/save": gin.H{
			"post": adminPath("model", "人工补录模型能力",
				"覆盖或新建一条能力登记, 来源固定记为 manual。"+adminNewLine+adminNewLine+
					"官方未收录的小众模型靠它兜底, 后续同步不会覆盖也不会删除人工条目。"+adminNewLine+
					"模态用逗号分隔, 例如 output=image 表示只出图。",
				"ModelCapability", adminItem("保存后的能力登记", "ModelCapability"), true),
		},
		"/api/v1/model/capability/delete": gin.H{
			"post": adminPath("model", "删除模型能力登记",
				"请求体只需 name; 同步会重新写回官方来源的条目, 人工条目删除后不再恢复。",
				"ModelNameRequest", adminOk(), true),
		},
		"/api/v1/model/last-update-time": gin.H{
			"get": adminPath("model", "价格库最后同步时间",
				"RFC3339 时间戳; 从未同步过时为零值时间。", "",
				adminEnvelope("同步时间", gin.H{"type": "string", "format": "date-time"}), false),
		},
		"/api/v1/model/scores": gin.H{
			"get": adminPath("model", "模型综合评分",
				"按可见范围给出各模型跨渠道的低价、延迟、成功率合成得分, 与分组 score 模式的选路口径一致。", "",
				adminArray("模型评分", "ModelScore"), false),
		},

		// ---------------- API Key ----------------
		"/api/v1/apikey/list": gin.H{
			"get": adminPath("apikey", "API Key 列表",
				"当前身份可见范围内的 Key(管理员为全站), 归属取自登录身份。"+
					"列表项只含掩码片段 key_prefix/key_last4 与档案字段: 不含明文 api_key, 也不含指纹 key_hash。", "",
				adminArray("Key 列表", "APIKey"), false),
		},
		"/api/v1/apikey/create": gin.H{
			"post": adminPath("apikey", "创建 API Key",
				"创建请求必须省略或留空 api_key，非空自选密钥返回 400；后端生成高熵随机串，明文只在这一次响应里返回, 此后任何接口都拿不到, 库里只存指纹与掩码。"+
					"响应里的 key_prefix/key_last4 是今后识别这把 Key 的唯一展示(形如 prefix********last4), 请当场复制保存 api_key。"+adminNewLine+adminNewLine+
					"归属强制为当前登录账号, 不信任请求体里的 user_id。",
				"APIKey", adminItem("新建 Key", "APIKey"), false),
		},
		"/api/v1/apikey/update": gin.H{
			"post": adminPath("apikey", "更新 API Key",
				"更新名称、启停、过期时间、限额、可用模型白名单与配额; 未提交的字段保留原值。"+
					"Key 本身不可替换: 请求带 api_key 一律 400, 旧 Key 原样可用, 权限与额度不受影响。"+
					"响应不含明文, 与列表一致只给 key_prefix/key_last4 掩码。",
				"APIKey", adminItem("更新后的 Key; 无明文, 只给 key_prefix/key_last4 掩码", "APIKey"), false),
		},
		"/api/v1/apikey/delete/{id}": gin.H{
			"delete": adminPath("apikey", "删除 API Key",
				"删除后使用该 Key 的转发请求立刻 401。", "",
				adminOk(), false, pathID("Key ID")),
		},
		"/api/v1/apikey/login": gin.H{
			"get": adminPath("apikey", "API Key 鉴权自检",
				"由转发端点用的鉴权中间件校验 Bearer Key, 通过即 200 且无 data。"+adminNewLine+adminNewLine+
					"本表里少数不吃会话 Cookie 的接口之一: 它服务于转发侧, 不需要登录态。", "",
				bearerProbe("鉴权通过"), false),
		},
		"/api/v1/apikey/stats": gin.H{
			"get": adminPath("apikey", "按当前 Key 统计",
				"该 Key 的累计用量与档案。"+adminNewLine+adminNewLine+
					"本表里少数不吃会话 Cookie 的接口之一: 由转发端的 Bearer 鉴权中间件守护, 与 /apikey/login 同口径。", "",
				gin.H{
					"200": gin.H{"description": "Key 统计", "content": jsonContent(gin.H{
						"type": "object",
						"properties": gin.H{
							"stats": ref("StatsAPIKey"),
							"info":  refWithDesc("APIKey", "Key 档案; supported_models 已按当前可用分组收敛; 无明文/指纹, 只给 key_prefix/key_last4 掩码"),
						},
					})},
					"401": bearerFail(),
				}, false),
		},
	}
}
