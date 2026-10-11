package handlers

import (
	"github.com/gin-gonic/gin"
)

// openAPIAdminPathsOps 设置、日志、统计、运维、限流、审计、升级与站点配置。
func openAPIAdminPathsOps() gin.H {
	return gin.H{
		// ---------------- 系统设置 ----------------
		"/api/v1/setting/list": gin.H{
			"get": adminPath("setting", "设置列表",
				"全部键值对: 站点文案、注册开关、告警渠道与 Telegram/SMTP 凭据、健康探测阈值、日志保留天数都在这里, 控制台按 key 分组渲染。", "",
				adminArray("设置项", "Setting"), true),
		},
		"/api/v1/setting/set": gin.H{
			"post": adminPath("setting", "写入设置",
				"一次一个键; 值统一为字符串, 数值与布尔由后端按 key 校验范围, 非法 400。"+adminNewLine+adminNewLine+
					"两个联动特例: 把指标鉴权切成 bearer 时自动确保指标令牌存在并在响应里带回; 修改自动升级间隔时即时重排定时器。",
				"Setting", adminItem("写入后的设置项", "Setting"), true),
		},
		"/api/v1/setting/export": gin.H{
			"get": adminPath("setting", "导出整库",
				"把渠道、分组、模型价、Key、设置与统计打包为 JSON 下载(Content-Disposition 附件), 响应不套统一外壳。", "",
				downloadJSON("库转储 JSON 文件", "DBDump"), true),
		},
		"/api/v1/setting/import": gin.H{
			"post": adminPath("setting", "导入整库",
				"两种提交方式二选一: multipart/form-data 带 file 字段(SQLite 二进制或 JSON 转储自动识别), 或直接 POST JSON 转储。"+adminNewLine+adminNewLine+
					"增量导入: 同主键覆盖, 不删除请求体里没出现的既有数据; 完成后重建全部缓存。该操作能改写任意配置, 只应在受控环境使用。", "",
				adminEnvelope("导入结果", gin.H{"$ref": "#/components/schemas/DBImportResult"}), true),
		},
		// ---------------- 转发日志 ----------------
		"/api/v1/log/list": gin.H{
			"get": adminPath("log", "转发日志列表",
				"按过滤条件分页返回请求记录与总条数; limit 超出 1-100 一律按 20。普通用户只看自己 Key 产生的记录, 管理员看全站。", "",
				adminItems("日志列表", "RequestState", true), false, logFilterParams(20)...),
		},
		"/api/v1/log/overview/stream": gin.H{
			"get": adminPath("log", "实时请求流",
				"SSE: 先推一份当前在途请求快照, 之后每个开始与结束事件实时推送; 空快照会先写一行 SSE 注释以便代理立即转发。日志页的实时看板用它。", "",
				sseResponses("在途请求事件流"), false),
		},
		"/api/v1/log/request-body/{id}": gin.H{
			"get": adminPath("log", "取归档请求体",
				"返回保存的原始请求体字符串(媒体类请求体可能是摘要)。仅归属者与管理员可读, 越权与不存在都返回 404。", "",
				adminEnvelope("请求体原文", gin.H{"type": "string"}), false, pathID("请求记录 ID")),
		},
		"/api/v1/log/response-body/{id}": gin.H{
			"get": adminPath("log", "取归档响应体",
				"返回上游响应体字符串; 读取权限口径同 request-body。", "",
				adminEnvelope("响应体原文", gin.H{"type": "string"}), false, pathID("请求记录 ID")),
		},
		"/api/v1/log/attempts/{id}": gin.H{
			"get": adminPath("log", "取上游尝试明细",
				"一条客户端请求可能因故障转移打到多个渠道与凭据, 这里按轮次给出渠道、模型、状态与耗时。", "",
				itemsArray("尝试明细", "RelayRequestAttempt"), false,
				pathID("请求记录 ID")),
		},
		"/api/v1/log/stop/{id}": gin.H{
			"post": adminPath("log", "中断在途请求",
				"取消整条请求的后续尝试并收尾记账; 只对尚未结束的请求有意义。", "",
				noContent(), false, pathID("请求记录 ID")),
		},
		"/api/v1/log/stop/{id}/{round}": gin.H{
			"post": adminPath("log", "中断指定一轮",
				"只打断第 round 轮尝试(从 1 开始, 非法 400), 故障转移仍可继续下一轮。", "",
				adminOk(), false, pathID("请求记录 ID"),
				gin.H{"name": "round", "in": "path", "required": true, "description": "轮次序号, 从 1 开始", "schema": gin.H{"type": "integer"}}),
		},
		"/api/v1/log/export": gin.H{
			"get": adminPath("log", "导出日志 CSV",
				"与列表同一套过滤参数, 一次性导出为附件; limit 上限 5000, 超出按 5000, 需要更多请缩小时间窗分段导出。", "",
				downloadCSV("日志 CSV 文件"), false, logFilterParams(5000)...),
		},
		"/api/v1/log/clear": gin.H{
			"delete": adminPath("log", "清理日志",
				"按保留策略立即执行一次归档清理, 等价于手动触发一次日志生命周期任务。", "",
				noContent(), true),
		},
		"/api/v1/log/replay/{id}": gin.H{
			"post": adminPath("log", "重放请求",
				"取该条记录的原始请求体(可用 request_body 现场改写)经完整转发链路重放: 选路、鉴权、计费照常发生, 因此真实消耗上游额度并产生新日志。"+adminNewLine+adminNewLine+
					"只有归档了可解析请求体的记录可重放, 否则 400; api_key_id 可改用其他 Key, 缺省用原 Key(已失效则 401)。",
				"ReplayRequest", adminEnvelope("重放结果", gin.H{"$ref": "#/components/schemas/ReplayResult"}), false,
				pathID("请求记录 ID")),
		},
		// ---------------- 统计与账单 ----------------
		"/api/v1/stats/daily": gin.H{
			"get": adminPath("stats", "按日用量",
				"管理员与渠道商返回全站长期曲线, 普通用户返回自己名下 Key 的聚合。", "",
				adminEnvelope("按日统计", gin.H{"type": "object", "properties": gin.H{
					"max_request_count": fieldNumber("integer", "最近 54 周每日请求量的最大值, 供前端定出图表纵轴上界"),
					"items":             gin.H{"type": "array", "items": ref("StatsDaily"), "description": "每日原始统计, 按日期升序"},
				}}), false),
		},
		"/api/v1/stats/hourly": gin.H{
			"get": adminPath("stats", "按小时用量",
				"口径同 daily, 粒度为小时。", "",
				adminArray("按小时统计", "StatsHourly"), false),
		},
		"/api/v1/stats/total": gin.H{
			"get": adminPath("stats", "累计用量",
				"请求数、token 数与成本的累计值; 管理员为全站, 其余为本人。", "",
				adminItem("累计统计", "StatsTotal"), false),
		},
		"/api/v1/stats/apikey": gin.H{
			"get": adminPath("stats", "按 API Key 用量",
				"管理员拿全量, 普通用户自动过滤到自己名下的 Key。", "",
				adminArray("Key 用量", "StatsAPIKey"), false),
		},
		"/api/v1/stats/billing": gin.H{
			"get": adminPath("stats", "本人账单曲线",
				"固定按当前登录账号统计近一个月逐日账单, 不接受 user_id 参数。", "",
				adminArray("账单曲线", "BillingAggregate"), false),
		},
		"/api/v1/stats/billing/revenue": gin.H{
			"get": adminPath("stats", "渠道商营收曲线",
				"按渠道归属(owner)聚合近一个月逐日营收; owner_id 只有管理员能指定, 其余身份固定查自己, 解析结果为 0 视为非法 400。", "",
				adminArray("营收曲线", "BillingAggregate"), false,
				query("owner_id", "指定查看他人的渠道商 ID(仅管理员生效)", "int", false, nil)),
		},
		"/api/v1/stats/billing/records": gin.H{
			"get": adminPath("stats", "逐条账单明细",
				"分页返回计费记录与总条数, 可按模型、渠道、结算状态、价格来源与时间窗过滤。", "",
				adminItems("账单明细", "BillingRecord", true), false, billingParams()...),
		},
		"/api/v1/stats/billing/records/export": gin.H{
			"get": adminPath("stats", "导出账单明细 CSV",
				"与 records 同一套过滤参数, 输出为附件。", "",
				downloadCSV("账单明细 CSV 文件"), false, billingParams()...),
		},
		"/api/v1/stats/balance/ledger": gin.H{
			"get": adminPath("stats", "余额流水",
				"充值、扣费、冻结、退款逐笔台账; 管理员可用 user_id 查他人, 其余身份固定查自己。", "",
				adminItems("余额流水", "BillingLedger", true), false,
				query("kind", "按流水类型过滤", "string", false, nil),
				query("user_id", "指定用户(仅管理员生效)", "int", false, nil),
				query("limit", "每页条数", "int", false, nil),
				query("offset", "偏移量", "int", false, nil)),
		},
		// ---------------- 运维 ----------------
		"/api/v1/ops/overview": gin.H{
			"get": adminPath("ops", "运维概览",
				"近 24 小时的请求、成功、失败、token 与成本, 加上当前在途请求数、渠道总数与启用数、24 小时内告警数。控制台首页轮询这一条。", "",
				adminItem("概览", "OpsOverview"), false),
		},
		"/api/v1/ops/balance-flow": gin.H{
			"get": adminPath("ops", "余额变动时间线",
				"对账用的余额流水视图; 管理员可用 user_id 查他人, 其余固定查自己。", "",
				itemsArray("余额流水", "BalanceFlow"), false,
				query("user_id", "指定用户(仅管理员生效)", "int", false, nil),
				intQuery("limit", "条数", 100)),
		},
		"/api/v1/ops/reconcile/run": gin.H{
			"post": adminPath("ops", "执行对账",
				"逐账号核对余额与消费、收入、冻结之间的勾稽关系并纠正漂移, 返回每个账号的差异明细。", "",
				itemsArray("对账结果", "BillingReconcile"), true,
				intQuery("days", "回溯天数", 1)),
		},
		"/api/v1/ops/reconcile/list": gin.H{
			"get": adminPath("ops", "对账历史",
				"最近若干次对账任务的执行记录。", "",
				itemsArray("对账历史", "BillingReconcile"), true,
				intQuery("limit", "条数", 30)),
		},
		"/api/v1/ops/log-lifecycle/run": gin.H{
			"post": adminPath("ops", "立即执行日志生命周期",
				"按保留策略归档并清理转发日志, 返回本次归档条数等明细; 失败会触发 system 类告警。", "",
				adminItem("归档结果", "ArchiveResult"), true),
		},
		"/api/v1/ops/log-lifecycle/list": gin.H{
			"get": adminPath("ops", "归档文件列表",
				"已归档的日志文件与当前生效的保留天数。", "",
				adminEnvelope("归档清单", gin.H{
					"type": "object",
					"properties": gin.H{
						"items":          gin.H{"type": "array", "items": gin.H{"$ref": "#/components/schemas/ArchiveFile"}},
						"retention_days": gin.H{"type": "integer"},
					},
				}), true),
		},
		"/api/v1/ops/backup/run": gin.H{
			"post": adminPath("ops", "立即备份",
				"对 SQLite 做一次在线快照, 返回文件名与大小; 失败触发 system 类告警。", "",
				adminItem("备份结果", "AutoBackupResult"), true),
		},
		"/api/v1/ops/backup/list": gin.H{
			"get": adminPath("ops", "备份文件列表",
				"备份目录内的历史备份。", "",
				itemsArray("备份清单", "BackupFile"), true),
		},
		"/api/v1/ops/health/list": gin.H{
			"get": adminPath("ops", "健康探测记录",
				"各渠道最近若干次探测的结果(延迟、状态码、是否健康); channel_id 可只看单个渠道。", "",
				itemsArray("探测记录", "ChannelHealthRecord"), true,
				query("channel_id", "只看某个渠道", "int", false, nil),
				intQuery("limit", "条数", 50)),
		},
		"/api/v1/ops/health/run": gin.H{
			"post": adminPath("ops", "立即健康探测",
				"对所有启用渠道并发跑一次探测, 按失败判据触发 down 与 recovered 告警; 明细到 health/list 查看。", "",
				adminEnvelope("已执行", gin.H{"type": "object", "properties": gin.H{"ok": gin.H{"type": "boolean"}}}), true),
		},
		"/api/v1/ops/alerts/list": gin.H{
			"get": adminPath("ops", "告警记录",
				"渠道宕机、恢复等告警的历史记录; limit 超出 1-500 一律按 50。", "",
				itemsArray("告警记录", "ChannelAlert"), true,
				intQuery("limit", "条数", 50)),
		},
		"/api/v1/ops/alerts/rules": gin.H{
			"get": adminPath("ops", "告警判据与当前值",
				"抖动抑制、去重窗口、延迟阈值等告警参数: 每项给出当前值、默认值、单位与说明。"+adminNewLine+adminNewLine+
					"这一条就是配置告警参数的入口; 改值本身走 /api/v1/setting/set 传对应的 key。", "",
				itemsArray("告警规则", "AlertRule"), true),
		},
		"/api/v1/ops/alerts/test-webhook": gin.H{
			"post": adminPath("ops", "测试 Webhook 告警",
				"用已配置的 Webhook 地址发一条 kind=test 的样例告警; 未配置 400, 上游非 2xx 或不可达 502。", "",
				adminEnvelope("发送成功", gin.H{"type": "object", "properties": gin.H{"ok": gin.H{"type": "boolean"}}}), true),
		},
		"/api/v1/ops/alerts/test/{channel}": gin.H{
			"post": adminPath("ops", "测试指定告警渠道",
				"只向 telegram 或 email 单渠道发一条测试告警, 用来验证 Bot Token、Chat ID 或 SMTP 凭据是否可用; 未知渠道名 400。", "",
				adminEnvelope("发送成功", gin.H{"type": "object", "properties": gin.H{"ok": gin.H{"type": "boolean"}}}), true,
				gin.H{"name": "channel", "in": "path", "required": true, "description": "渠道名",
					"schema": gin.H{"type": "string", "enum": []string{"telegram", "email"}}}),
		},
		"/api/v1/ops/metrics/token": gin.H{
			"get": adminPath("ops", "查看指标鉴权",
				"返回 /metrics 的鉴权模式与 Bearer 令牌(令牌不存在时自动创建)。", "",
				adminEnvelope("指标令牌", metricsTokenSchema()), true),
		},
		"/api/v1/ops/metrics/token/rotate": gin.H{
			"post": adminPath("ops", "轮换指标令牌",
				"作废旧令牌并签发新的; 抓取端需同步更新 Authorization 头。", "",
				adminEnvelope("新令牌", metricsTokenSchema()), true),
		},
		"/api/v1/ops/auth-secret/rotate": gin.H{
			"post": adminPath("ops", "轮换会话密钥",
				"更换 JWT 签名密钥: 所有已登录会话立即失效, 全部用户需重新登录。新密钥本身不回显。", "",
				adminEnvelope("已轮换", gin.H{"type": "object", "properties": gin.H{"message": gin.H{"type": "string"}}}), true),
		},
		// ---------------- 限流 ----------------
		"/api/v1/ratelimit/policy": gin.H{
			"get": adminPath("ratelimit", "限流策略列表",
				"全部策略; 生效键是 scope_type + scope_id + model_name 三元组。", "",
				itemsArray("策略列表", "RateLimitPolicy"), true),
		},
		"/api/v1/ratelimit/policy/save": gin.H{
			"post": adminPath("ratelimit", "保存限流策略",
				"带 id 为更新, 不带为新建。scope_type=system 时 scope_id 强制归零(全站口径), 其余 scope 必须给正的 scope_id。"+adminNewLine+adminNewLine+
					"rpm、tpm、concurrent 任一为负即 400, 0 表示该项不限制。",
				"RateLimitPolicy", adminItem("保存后的策略", "RateLimitPolicy"), true),
		},
		"/api/v1/ratelimit/policy/delete": gin.H{
			"post": adminPath("ratelimit", "删除限流策略",
				"请求体只需 id; 成功返回 204 空响应体, 不套统一外壳。",
				"PolicyIDRequest", gin.H{
					"204": gin.H{"description": "已删除, 无响应体"},
					"400": gin.H{"description": "id 缺失或非法", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
					"401": gin.H{"description": "未登录或会话失效", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
					"403": gin.H{"description": "已登录但角色不符(不在该路由的角色白名单内)", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})},
				}, true),
		},
		"/api/v1/ratelimit/inspect": gin.H{
			"get": adminPath("ratelimit", "查看实时限流预算",
				"指定 scope 的当前窗口已用量(requests 与 tokens)、并发数、重置时间与是否处于封禁; 该 scope 覆盖多个模型时逐模型给出 budgets。scope_type 非法 400。", "",
				adminItem("限流快照", "RateLimitInspect"), true,
				query("scope_type", "口径(留空按 system)", "string", false, gin.H{"enum": []string{"system", "user", "apikey", "group"}}),
				query("scope_id", "口径对应的对象 ID(system 忽略)", "int", false, nil),
				query("model", "限定模型名(留空覆盖该 scope 全部模型)", "string", false, nil)),
		},
		"/api/v1/ratelimit/usage": gin.H{
			"get": adminPath("ratelimit", "限流用量历史",
				"按小时聚合的用量序列, 用于观察触顶情况。", "",
				itemsArray("用量序列", "RateLimitUsageHourly"), true,
				query("scope_type", "口径", "string", false, nil),
				query("scope_id", "对象 ID", "int", false, nil),
				query("hours", "回溯小时数", "int", false, nil)),
		},
		// ---------------- 审计 ----------------
		"/api/v1/audit/list": gin.H{
			"get": adminPath("audit", "操作审计列表",
				"谁在什么时候对什么做了什么; limit 超出 1-200 一律按 50。", "",
				adminItems("审计记录", "AuditLog", true), true, auditFilterParams(50)...),
		},
		"/api/v1/audit/actions": gin.H{
			"get": adminPath("audit", "审计动作清单",
				"去重后的全部 action 值, 供列表页做筛选下拉。", "",
				adminEnvelope("动作清单", gin.H{"type": "array", "items": gin.H{"type": "string"}}), true),
		},
		"/api/v1/audit/export": gin.H{
			"get": adminPath("audit", "导出审计 CSV",
				"与 list 同一套过滤参数, 输出为附件。", "",
				downloadCSV("审计 CSV 文件"), true, auditFilterParams(0)...),
		},
		// ---------------- 在线升级 ----------------
		"/api/v1/update": gin.H{
			"get": adminPath("update", "检查升级",
				"返回升级状态: 当前版本、最新版本、是否有可用更新与暂停标记。force=1 或 true 跳过缓存强制查远端; 升级被管理员暂停时 503。", "",
				adminItem("升级状态", "UpdateStatus"), true, forceParam()),
			"post": adminPath("update", "执行升级",
				"下载并替换二进制, 随后自动重启进程。"+adminNewLine+adminNewLine+
					"响应写完后进程即退出, 浏览器可能表现为网络中断 —— 这是预期行为, 请以重启后拿到的版本号为准。已是最新 409, 升级被暂停 503。", "",
				adminItem("升级结果(此后进程重启)", "UpdateResult"), true, forceParam()),
		},
		"/api/v1/update/check": gin.H{
			"post": adminPath("update", "立即检查升级",
				"等价于带 force 的 GET /api/v1/update, 语义上更适合由按钮触发的主动检查。", "",
				adminItem("升级状态", "UpdateStatus"), true),
		},
		"/api/v1/update/now-version": gin.H{
			"get": adminPath("update", "当前运行版本",
				"返回构建时写入的版本号字符串。", "",
				adminEnvelope("当前版本", gin.H{"type": "string"}), true),
		},
		// ---------------- 公开分享 ----------------
		"/api/v1/share/{code}": gin.H{
			"get": adminPathPublic("site", "分享编码下的模型清单",
				"公开接口, 无需登录: 分享页与下游对接方据此查询某个发布编码下已上架的模型与对客价。", "",
				shareEnvelope()),
			"parameters": []gin.H{
				shareCodeParam(),
			},
		},
		// ---------------- 站点公开配置 ----------------
		"/api/v1/site/config": gin.H{
			"get": adminPathPublic("site", "站点配置",
				"登录页与标题栏使用: 站点名称、描述、联系方式、公告(开关关闭或无正文时为空串)、维护模式与提示文案。", "",
				adminEnvelope("站点配置", gin.H{
					"type": "object",
					"properties": gin.H{
						"site_name":          gin.H{"type": "string"},
						"site_description":   gin.H{"type": "string"},
						"site_contact":       gin.H{"type": "string"},
						"announcement":       gin.H{"type": "string", "description": "公告正文; 为空则前端不显示横幅"},
						"maintenance_mode":   gin.H{"type": "boolean"},
						"maintenance_notice": gin.H{"type": "string", "description": "维护提示, 未配置时给默认文案"},
					},
				})),
		},
	}
}

// bearerProbe Bearer 鉴权探针的成功响应(不套统一 data)。
func bearerProbe(desc string) gin.H {
	return gin.H{
		"200": gin.H{"description": desc, "content": jsonContent(gin.H{
			"type": "object",
			"properties": gin.H{
				"code":    gin.H{"type": "integer", "example": 200},
				"message": gin.H{"type": "string", "example": "success"},
			},
		})},
		"401": bearerFail(),
	}
}
func bearerFail() gin.H {
	return gin.H{"description": "API Key 缺失、无效或被停用", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})}
}

// downloadCSV 文件下载响应: 成功是附件而不是统一外壳, 因此不复用 adminEnvelope。
func downloadCSV(desc string) gin.H {
	return gin.H{
		"200": gin.H{"description": desc, "content": gin.H{"text/csv": gin.H{"schema": gin.H{"type": "string"}}}},
		"401": gin.H{"description": "未登录或会话失效"},
	}
}

// downloadJSON 以附件形式返回的 JSON 文档。
func downloadJSON(desc, ref string) gin.H {
	return gin.H{
		"200": gin.H{"description": desc, "content": jsonContent(gin.H{"$ref": "#/components/schemas/" + ref})},
		"401": gin.H{"description": "未登录或会话失效"},
	}
}

// shareCodeParam 路径上的发布编码参数。
func shareCodeParam() gin.H {
	return gin.H{"name": "code", "in": "path", "required": true, "description": "发布编码", "schema": gin.H{"type": "string"}}
}

// shareEnvelope 公开分享接口的响应: 无需登录故不声明 401, 编码不存在补 404。
func shareEnvelope() gin.H {
	data := gin.H{"type": "object", "required": []string{"share_code", "models"}, "properties": gin.H{
		"share_code": fieldString("回显请求里的发布编码"),
		"models":     gin.H{"type": "array", "items": ref("ShareModel"), "description": "该编码下已上架的模型与对客价"},
	}}
	responses := adminEnvelope("编码与模型清单", data)
	delete(responses, "401")
	responses["404"] = gin.H{"description": "发布编码不存在或未发布", "content": jsonContent(gin.H{"$ref": "#/components/schemas/AdminEnvelopeError"})}
	return responses
}

func metricsTokenSchema() gin.H {
	return gin.H{
		"type": "object",
		"properties": gin.H{
			"mode":  gin.H{"type": "string", "enum": []string{"off", "bearer"}, "description": "off 匿名可抓 / bearer 需携带 token"},
			"token": gin.H{"type": "string"},
		},
	}
}
func forceParam() gin.H {
	return query("force", "跳过缓存或重复校验强制执行", "string", false, gin.H{"example": "1"})
}

// logFilterParams 转发日志的过滤参数, defLimit 只用于文档里的默认值提示。
func logFilterParams(defLimit int) []gin.H {
	return []gin.H{
		intQuery("limit", "每页条数", defLimit),
		query("offset", "偏移量, 负数按 0", "int", false, nil),
		query("keyword", "按模型名、上游或客户端 IP 模糊匹配", "string", false, nil),
		query("status", "状态集合, 逗号分隔(success / upstream_error / client_error / interrupted / in_progress)", "string", false, nil),
		query("model", "按分组名即客户端模型名过滤", "string", false, nil),
		query("channel", "按渠道名或 share_code 过滤", "string", false, nil),
		query("client_ip", "按客户端 IP 过滤", "string", false, nil),
		query("from", "起始时间 RFC3339, 解析失败即忽略", "string", false, nil),
		query("to", "结束时间 RFC3339", "string", false, nil),
	}
}

// billingParams 账单明细的过滤参数(列表与导出共用)。
func billingParams() []gin.H {
	return []gin.H{
		query("model", "按分组名过滤", "string", false, nil),
		query("channel", "按渠道过滤", "string", false, nil),
		query("status", "按结算状态过滤", "string", false, nil),
		query("price_source", "按价格来源过滤", "string", false, nil),
		query("user_id", "指定用户(仅管理员生效)", "int", false, nil),
		query("api_key_id", "指定 API Key", "int", false, nil),
		query("from", "起始时间 RFC3339", "string", false, nil),
		query("to", "结束时间 RFC3339", "string", false, nil),
		intQuery("limit", "每页条数", 20),
		query("offset", "偏移量", "int", false, nil),
	}
}

// auditFilterParams 审计的过滤参数; export 复用同一套, 传 0 表示不声明默认条数。
func auditFilterParams(defLimit int) []gin.H {
	params := []gin.H{
		query("keyword", "按对象或详情模糊匹配", "string", false, nil),
		query("action", "按动作精确过滤", "string", false, nil),
		query("operator", "按操作者用户名过滤", "string", false, nil),
		query("from", "起始时间 RFC3339", "string", false, nil),
		query("to", "结束时间 RFC3339", "string", false, nil),
	}
	if defLimit > 0 {
		return append(params, intQuery("limit", "每页条数", defLimit), query("offset", "偏移量", "int", false, nil))
	}
	return params
}
