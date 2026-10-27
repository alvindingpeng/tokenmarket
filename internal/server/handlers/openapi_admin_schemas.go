package handlers

import (
	"github.com/gin-gonic/gin"
)

// openAPIAdminSchemas 管理面的数据结构定义。
//
// 每个结构的字段名与 json tag 逐一对齐 Go 类型, 顺序也保持一致, 便于对照源码核对。
// 字段说明取自源码注释的要点(单位、口径、0 值的含义), 这些正是文档比读源码更值钱的地方。
func openAPIAdminSchemas() gin.H {
	return gin.H{
		// ---------- 通用 ----------
		"AdminEnvelopeError": object(gin.H{
			"code":    fieldNumber("integer", "与 HTTP 状态码同值的业务码"),
			"message": fieldString("可读的失败原因, 控制台直接展示"),
			"data":    gin.H{"nullable": true, "description": "失败时通常省略"},
		}, "code", "message"),

		// ---------- 账号 ----------
		"LoginRequest": object(gin.H{
			"username": fieldString("账号名"),
			"password": fieldString("密码明文, 仅在传输与落库摘要之间短暂存在"),
			"expire":   fieldNumber("integer", "会话 Cookie 有效期(秒); 0 或负数取默认值"),
		}, "username", "password"),

		"RegisterRequest": object(gin.H{
			"username": fieldString("账号名, 全局唯一"),
			"password": fieldString("密码明文"),
			"reason":   fieldString("渠道商注册的申请说明, 供管理员审批时参考; 普通用户注册忽略"),
		}, "username", "password"),

		"StepUpRequest": object(gin.H{
			"password": fieldString("当前登录账号的密码明文, 只用于这一次二次验证; 服务端不落日志、不进审计"),
		}, "password"),

		"StepUpCredential": object(gin.H{
			"token":      fieldString("短期 step-up 凭证(JWT), 高危请求放进 X-Step-Up-Token 头提交"),
			"expires_in": fieldNumber("integer", "有效期秒数, 当前固定 300(5 分钟)"),
		}, "token", "expires_in"),

		"ChangePasswordRequest": object(gin.H{
			"old_password": fieldString("当前密码, 必须匹配"),
			"new_password": fieldString("新密码"),
		}, "old_password", "new_password"),

		"ChangeUsernameRequest": object(gin.H{
			"new_username": fieldString("新用户名; 全局唯一, 冲突 400"),
		}, "new_username"),

		"UserManageRequest": object(gin.H{
			"id":       fieldNumber("integer", "目标账号 ID; 创建时省略或 0"),
			"username": fieldString("用户名; 仅在改名时提交, 留空即不改"),
			"role":     gin.H{"type": "string", "enum": []string{"user", "reseller", "admin"}, "description": "账号角色"},
			"status":   gin.H{"type": "string", "enum": []string{"active", "pending", "disabled"}, "description": "账号状态; disabled 与 pending 登录即 401"},
			"balance":  fieldNumber("number", "可用余额; 指针字段, 不提交即不改动"),
			"password": fieldString("重置密码时使用"),
		}),

		"UserView": object(gin.H{
			"id":            fieldNumber("integer", ""),
			"username":      fieldString(""),
			"role":          gin.H{"type": "string", "enum": []string{"user", "reseller", "admin"}},
			"status":        gin.H{"type": "string", "enum": []string{"active", "pending", "disabled"}},
			"balance":       fieldNumber("number", "可用余额"),
			"frozen":        fieldNumber("number", "在途请求的预扣冻结额"),
			"total_spent":   fieldNumber("number", "累计支出"),
			"total_revenue": fieldNumber("number", "渠道商累计营收"),
			"created_at":    datetime(""),
		}, "id", "username"),

		// ---------- 渠道 ----------
		"ChannelConfig": object(channelConfigProperties(), "name"),
		"CustomHeader": object(gin.H{
			"header_key":   fieldString("Header 名"),
			"header_value": fieldString("Header 值"),
		}),

		"ChannelKeyConfig": object(gin.H{
			"name":           fieldString("凭据名称, 在渠道内唯一, 也是整体替换时的匹配依据"),
			"key":            fieldString("上游访问凭据"),
			"app_id":         fieldString("讯飞 App ID"),
			"api_secret":     fieldString("讯飞 API Secret"),
			"domain":         fieldString("讯飞 Domain"),
			"enabled":        fieldBool("禁用后不参与选路但保留统计"),
			"priority":       fieldNumber("integer", "优先级, 数值越小越优先"),
			"error_count":    fieldNumber("integer", "累计错误次数"),
			"last_error_at":  gin.H{"type": "string", "format": "date-time", "nullable": true, "description": "最后错误时间; 从未出错为 null"},
			"disabled_until": gin.H{"type": "string", "format": "date-time", "nullable": true, "description": "故障熔断的自动禁用截止时间; 未熔断为 null"},
		}),

		"ChannelEgress": object(gin.H{
			"id":      fieldNumber("integer", "出口主键; 提交时忽略, 后端按名称匹配已有出口"),
			"name":    fieldString("出口名称, 在渠道内唯一, 也是整体替换时的匹配依据"),
			"kind":    gin.H{"type": "string", "enum": []string{"direct", "http", "socks5"}, "description": "接入方式; direct 时 url 恒为空"},
			"url":     fieldString("代理地址; kind=http 时 http/https, kind=socks5 时 socks5/socks5h, kind=direct 时留空"),
			"region":  fieldString("出口所在地区码(如 HK), 用于区域亲和; 留空表示不参与匹配"),
			"weight":  fieldNumber("integer", "同分时的优先权重, 数值越大越先被选中; 也是冷出口的采样顺序"),
			"enabled": fieldBool("停用后不参与选路但保留配置"),
		}, "name", "kind"),

		"ChannelEgressHealth": object(gin.H{
			"id":                    fieldNumber("integer", "出口主键"),
			"name":                  fieldString("出口名称"),
			"kind":                  fieldString("出口类型"),
			"region":                fieldString("出口地区码"),
			"weight":                fieldNumber("integer", "同分时优先权重"),
			"enabled":               fieldBool("是否参与选路"),
			"samples":               fieldNumber("integer", "已观测样本数; 0 表示进程重启后尚无样本"),
			"has_data":              fieldBool("是否已有运行时样本"),
			"success_rate":          fieldNumber("number", "成功率 EMA, 0 到 1; 无样本时为 0"),
			"avg_wait_ms":           fieldNumber("number", "首响应耗时 EMA, 毫秒; 无样本时为 0"),
			"cooldown_remaining_ms": fieldNumber("integer", "按服务端时钟计算的剩余冷却毫秒数"),
			"cooldown_until":        gin.H{"type": "string", "format": "date-time", "nullable": true},
			"cooling":               fieldBool("当前是否冷却"),
			"updated_at":            gin.H{"type": "string", "format": "date-time", "nullable": true},
		}),

		"ChannelGrantConfig": object(gin.H{
			"model_name": fieldString("渠道模型名称, 按名称引用"),
			"key_name":   fieldString("渠道凭据名称, 按名称引用"),
			"protocols":  protocolBitmask("该组合支持的协议位掩码"),
		}),

		// ChannelDetail 匿名嵌入 ChannelConfig, JSON 里是展平的: name、base_url 等字段直接落在这一层,
		// 不是嵌在一个 config 对象下, 与 ChannelDetail 的读写同构语义一致。
		"ChannelDetail": object(withChannelConfig(gin.H{
			"id":       fieldNumber("integer", "渠道主键; 创建时提交 0 由数据库分配"),
			"keys":     gin.H{"type": "array", "items": ref("ChannelKeyConfig"), "description": "渠道下的上游凭据, 恒为数组"},
			"egresses": gin.H{"type": "array", "items": ref("ChannelEgress"), "description": "渠道的出口池, 恒为数组; 留空则沿用渠道自身的代理配置"},
			"models":   gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "渠道提供的上游模型名称"},
			"grants":   gin.H{"type": "array", "items": ref("ChannelGrantConfig"), "description": "渠道下的授权"},
		})),

		"ChannelFetchModelRequest": object(gin.H{
			"channel":    ref("ChannelConfig"),
			"key":        fieldString("拉取使用的上游凭据; 必填"),
			"app_id":     fieldString("讯飞 App ID; Spark 渠道使用"),
			"api_secret": fieldString("讯飞 API Secret; Spark 渠道使用"),
			"domain":     fieldString("讯飞 Domain; Spark 渠道使用"),
		}, "key"),

		"ChannelFetchModel": object(gin.H{
			"name":      fieldString("上游模型名称"),
			"protocols": protocolBitmask("由 OpenAI 与 Anthropic 两侧探测结果合并出的协议位掩码"),
		}),

		"ChannelPublishRequest": object(gin.H{
			"id":     fieldNumber("integer", "渠道 ID"),
			"shared": fieldBool("发布到用户侧还是收回"),
		}, "id", "shared"),

		"ChannelEnableRequest": object(gin.H{
			"id":      fieldNumber("integer", "渠道 ID"),
			"enabled": fieldBool(""),
		}, "id", "enabled"),

		"ChannelModelListing": object(gin.H{
			"name":         fieldString("渠道模型名称"),
			"listed":       fieldBool("是否上架: 已发布渠道中只有上架的模型对用户可见"),
			"supply_price": ref("LLMPrice"),
			"media_supply": ref("MediaPrice"),
			"kind":         gin.H{"type": "string", "enum": []string{"", "image", "video"}, "description": "模型媒体形态; 文本为空, 只读回显, 由授权协议位推导"},
		}),

		"ChannelModelUpdateRequest": object(gin.H{
			"id":       fieldNumber("integer", "渠道 ID"),
			"listings": gin.H{"type": "array", "items": ref("ChannelModelListing"), "description": "整批提交的上架配置"},
		}, "id", "listings"),

		"ListingChange": object(gin.H{
			"name":   fieldString("发生变更的模型名"),
			"detail": fieldString("变更内容摘要, 同时写入审计"),
		}),

		"ChannelRepriceRequest": object(gin.H{
			"id":         fieldNumber("integer", "渠道 ID"),
			"multiplier": fieldNumber("number", "统一倍率: 目标供货价 = 官方价 × 倍率; 取值 (0,100]"),
			"names":      gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "只改这些模型; 省略或留空表示改全部可改的行"},
		}, "id", "multiplier"),

		"ChannelRepriceRow": object(gin.H{
			"name":     fieldString("渠道模型名称"),
			"kind":     gin.H{"type": "string", "enum": []string{"", "image", "video"}, "description": "媒体形态; 非文本模型不参与 token 口径改价"},
			"listed":   fieldBool("是否已上架; 改价不改上架状态"),
			"current":  refWithDesc("LLMPrice", "当前供货价"),
			"official": refWithDesc("LLMPrice", "官方价(取自本地价格库 llm_infos)"),
			"next":     refWithDesc("LLMPrice", "官方价 × 统一倍率"),
			"changed":  fieldBool("新价与当前价是否有差异"),
			"skipped":  gin.H{"type": "string", "enum": []string{"", "no_official_price", "media_model", "over_cap"}, "description": "跳过原因; 空串表示该行已被计算"},
		}),

		"ChannelRepriceResult": object(gin.H{
			"channel_id": fieldNumber("integer", "渠道 ID"),
			"multiplier": fieldNumber("number", "本次使用的统一倍率"),
			"rows":       gin.H{"type": "array", "items": ref("ChannelRepriceRow"), "description": "逐个模型的计算结果, 按名称定序"},
			"changed":    fieldNumber("integer", "价格发生变化的行数"),
			"skipped":    fieldNumber("integer", "被跳过的行数"),
			"updated":    fieldNumber("integer", "实际写入的行数; 预览接口恒为 0"),
		}),

		"ChannelBreakerResetRequest": object(gin.H{
			"id":          fieldNumber("integer", "渠道 ID"),
			"key_names":   gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "要解除熔断的凭据名称; 与 model_names 至少给一个"},
			"model_names": gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "要解除熔断的渠道模型名称; 与 key_names 至少给一个"},
		}, "id"),

		"ModelAutoAddRule": object(gin.H{
			"name":       fieldString("模型名, 统一小写; 与价格表 llm_infos 同域"),
			"mode":       gin.H{"type": "string", "enum": []string{"", "on", "off"}, "description": "并入开关: on 强制并入(跳过其余条件), off 永不并入, 空表示跟随全局与渠道开关"},
			"price_min":  fieldString("参考价下限(每百万 token, blended 价); 空串表示回落全局"),
			"price_max":  fieldString("参考价上限; 空串表示回落全局"),
			"markup_min": fieldString("倍率下限(模型参考价 ÷ 渠道现有价位中位数); 空串表示回落全局"),
			"markup_max": fieldString("倍率上限; 空串表示回落全局"),
			"exclude":    fieldString("排除正则, 命中的模型不并入; 空串表示沿用全局正则"),
			"include":    fieldString("名称白名单正则, 非空时只有命中的模型才并入; 空串表示沿用全局正则"),
			"fill_price": gin.H{"type": "string", "enum": []string{"", "on", "off"}, "description": "并入时是否按参考价预填供货价; 空表示回落全局"},
		}),

		"ModelCapability": object(gin.H{
			"name":       fieldString("模型名, 统一小写; 可能带厂商前缀(如 openai/gpt-image-1)"),
			"input":      fieldString("逗号分隔的输入模态: text,image,audio,video,pdf"),
			"output":     fieldString("逗号分隔的输出模态: text,image,video,audio,pdf"),
			"context":    fieldNumber("integer", "上下文窗口(token); 0 表示官方未登记"),
			"max_output": fieldNumber("integer", "单次最大输出(token); 0 表示官方未登记"),
			"family":     fieldString("官方系列名, 仅供排错与展示"),
			"source":     gin.H{"type": "string", "enum": []string{"models.dev", "manual"}, "description": "来源; manual 为人工补录, 同步时不覆盖也不删除"},
			"updated_at": datetime(""),
		}),

		"ChannelStats": withStatsFlatten(gin.H{
			"channel_id":   fieldNumber("integer", "渠道主键"),
			"user_id":      fieldNumber("integer", "归属用户"),
			"channel_name": fieldString(""),
			"shared":       fieldBool("是否已发布到用户侧"),
			"share_code":   fieldString("发布唯一编码; 未发布为空"),
			"enabled":      fieldBool(""),
			"models":       gin.H{"type": "array", "items": ref("ChannelModelStats"), "description": "该渠道各模型的独立统计, 恒为数组; 长度即模型个数"},
		}),

		"ChannelModelStats": withStatsFlatten(gin.H{
			"model_id":       fieldNumber("integer", "渠道模型主键"),
			"model_name":     fieldString("上游模型名称"),
			"error_count":    fieldNumber("integer", "累计错误次数(渠道模型级熔断)"),
			"last_error_at":  gin.H{"type": "string", "format": "date-time", "nullable": true, "description": "最后错误时间; 从未出错为 null"},
			"disabled_until": gin.H{"type": "string", "format": "date-time", "nullable": true, "description": "渠道模型级熔断截止时间; 未熔断为 null"},
		}),

		"ChannelGrantCandidate": withStatsFlatten(gin.H{
			"id":           fieldNumber("integer", "授权主键, 分组成员按它引用"),
			"channel_id":   fieldNumber("integer", ""),
			"channel_name": fieldString("展示名: 已发布渠道一律显示发布编码, 未发布渠道显示真名"),
			"model_name":   fieldString("上游模型名称"),
			"key_name":     fieldString("凭据名称; 非归属者视角为空(脱敏)"),
			"protocols":    protocolBitmask(""),
			"available":    fieldBool("渠道与凭据均启用且模型与凭据都存在; 为假表示当前无法转发"),
			"listed":       fieldBool("模型是否上架; 非归属者只看到上架模型"),
			"supply_price": ref("LLMPrice"),
			"user_price":   ref("LLMPrice"),
		}),

		"ChannelDailyStats": object(gin.H{
			"channel": gin.H{"type": "array", "items": ref("StatsChannelDaily"), "description": "渠道本身的按日曲线"},
			"models":  gin.H{"type": "array", "items": ref("ChannelModelDaily"), "description": "渠道下逐个模型的按日曲线; 按模型分组, 便于前端按模型分别画线"},
		}),
		"ChannelModelDaily": object(gin.H{
			"channel_model_id": fieldNumber("integer", ""),
			"model_name":       fieldString("上游模型名称"),
			"days":             gin.H{"type": "array", "items": ref("StatsChannelModelDaily"), "description": "该模型的逐日曲线, 按日期升序"},
		}),
		// ---------- 分组 ----------
		"GroupResponse": object(gin.H{
			"id":             fieldNumber("integer", "分组主键"),
			"user_id":        fieldNumber("integer", "归属用户; 分组名在归属内唯一, 跨用户可重名"),
			"name":           fieldString("客户端请求使用的模型名称"),
			"mode":           gin.H{"type": "string", "enum": []string{"manual", "failover", "price", "latency", "success", "score", "random"}, "description": "成员选择模式"},
			"active_item_id": fieldNumber("integer", "手动模式指定的成员; 恒为 0 以遮蔽写入侧陈旧值, 当前成员一律读 runtime.current_item_id"),
			"relay_config":   ref("GroupRelayConfig"),
			"items":          gin.H{"type": "array", "items": ref("GroupItem"), "description": "可手动选择或故障转移的成员; 恒为数组, 空集合也给出"},
			"runtime":        ref("RouteState"),
		}),

		"GroupItem": object(gin.H{
			"id":               fieldNumber("integer", "成员主键"),
			"group_id":         fieldNumber("integer", ""),
			"channel_grant_id": fieldNumber("integer", "引用的渠道授权 ID"),
			"priority":         fieldNumber("integer", "展示顺序与故障转移的切换顺序, 越小越先"),
			"channel_id":       fieldNumber("integer", ""),
			"channel_key_id":   fieldNumber("integer", "授权引用的凭据 ID, 供上游限流定位"),
			"channel_model_id": fieldNumber("integer", "授权引用的渠道模型 ID, 供上游限流定位"),
			"channel_name":     fieldString("展示名: 已发布渠道显示发布编码, 未发布显示真名"),
			"model_name":       fieldString(""),
			"key_name":         fieldString("凭据名称; 非归属者视角为空"),
			"protocols":        protocolBitmask(""),
			"available":        fieldBool("为假表示该成员当前无法转发, 但仍列出以便移除"),
			"supply_price":     ref("LLMPrice"),
			"user_price":       ref("LLMPrice"),
			"media_supply":     ref("MediaPrice"),
			"user_media":       ref("MediaPrice"),
			"kind":             gin.H{"type": "string", "enum": []string{"", "image", "video"}, "description": "模型媒体形态, 文本留空"},
		}),

		"GroupCreateRequest": object(gin.H{
			"name":         fieldString("分组名, 必填"),
			"mode":         gin.H{"type": "string", "enum": []string{"manual", "failover", "price", "latency", "success", "score", "random"}, "description": "留空按手动"},
			"relay_config": ref("GroupRelayConfig"),
			"items":        gin.H{"type": "array", "items": ref("GroupItemInput")},
		}, "name"),

		"GroupUpdateRequest": object(gin.H{
			"name":           fieldString("仅在名称变更时发送"),
			"mode":           gin.H{"type": "string", "enum": []string{"manual", "failover", "price", "latency", "success", "score", "random"}},
			"relay_config":   ref("GroupRelayConfig"),
			"items":          gin.H{"type": "array", "items": ref("GroupItemInput"), "description": "新的成员集合, 整体替换; 提交顺序即优先级顺序"},
			"active_item_id": fieldNumber("integer", "手动模式指定的当前成员, 0 表示取消选择; 用指针以便与未提交该字段区分"),
		}),

		"GroupItemInput": object(gin.H{
			"channel_grant_id": fieldNumber("integer", "待引用的渠道授权 ID, 取自 /api/v1/channel/grants"),
		}, "channel_grant_id"),

		"GroupRelayConfig": object(gin.H{
			"member_stream_idle_timeout_seconds":         fieldNumber("integer", "流式响应两帧之间的最大空闲秒数"),
			"request_timeout_seconds":                    fieldNumber("integer", "整请求最大耗时秒数, 含重试与流式传输"),
			"max_wait_seconds":                           fieldNumber("integer", "无可用成员时最大等待秒数, 0 立即返回"),
			"max_waiting_requests":                       fieldNumber("integer", "同一分组同时排队的请求上限, 0 不排队"),
			"max_total_attempts":                         fieldNumber("integer", "单请求跨成员最大尝试轮次"),
			"member_max_attempts":                        fieldNumber("integer", "单个成员含首次的总尝试次数, 仅动态选路生效"),
			"member_retry_interval_seconds":              fieldNumber("integer", "同一成员相邻两次尝试的等待秒数"),
			"member_non_stream_response_timeout_seconds": fieldNumber("integer", "单个成员返回完整非流式响应的超时秒数"),
			"member_stream_first_event_timeout_seconds":  fieldNumber("integer", "单个成员返回首个有效流事件的超时秒数"),
			"member_cooldown_seconds":                    fieldNumber("integer", "成员耗尽尝试后被跳过的秒数, 仅动态选路生效"),
			"member_affinity_seconds":                    fieldNumber("integer", "动态选路成功后保持当前成员的秒数; 0 表示不保持"),
			"price_metric":                               gin.H{"type": "string", "enum": []string{"blended", "input", "output"}, "description": "低价优先与综合评分的价格口径"},
			"metric_window_size":                         fieldNumber("integer", "延迟与成功率指标的样本窗口规模, 5-200"),
			"score_price_weight":                         fieldNumber("integer", "综合评分中价格的相对权重 0-100; 0 表示跟随系统默认"),
			"score_latency_weight":                       fieldNumber("integer", "综合评分中延迟的相对权重 0-100; 0 表示跟随系统默认"),
			"score_success_weight":                       fieldNumber("integer", "综合评分中成功率的相对权重 0-100; 0 表示跟随系统默认"),
			"reliability_floor":                          fieldNumber("integer", "可靠性下限 0-100, 低于该成功率的成员不参与动态选路"),
		}),

		"RouteState": object(gin.H{
			"group_id":        fieldNumber("integer", "状态所属分组"),
			"current_item_id": fieldNumber("integer", "当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定"),
			"probe_item_id":   fieldNumber("integer", "正在被恢复探测的成员 ID; 同分组同时只允许一个, 手动模式恒为 0"),
			"affinity_until":  fieldNumber("integer", "当前路由亲和截止的 Unix 毫秒时间, 0 表示无亲和"),
			"cooldowns":       gin.H{"type": "object", "additionalProperties": gin.H{"type": "integer"}, "description": "失败成员 ID 到冷却截止 Unix 毫秒时间的映射; 已到期条目由前端按当前时间忽略"},
		}),

		"GroupMemberMetric": object(gin.H{
			"item_id": fieldNumber("integer", "分组成员 ID"),
			"wait_ms": fieldNumber("number", "首响应耗时指数滑动平均, 毫秒"),
			"success": fieldNumber("number", "成功率指数滑动平均, 0 到 1"),
			"samples": fieldNumber("integer", "已累计样本数"),
		}),

		"ModelScore": object(gin.H{
			"name":     fieldString("模型名"),
			"wait_ms":  fieldNumber("number", "跨渠道加权后的首响应耗时"),
			"success":  fieldNumber("number", "跨渠道加权后的成功率"),
			"samples":  fieldNumber("integer", ""),
			"channels": gin.H{"type": "array", "items": ref("ModelScoreChannel"), "description": "该模型在各渠道上的分项得分"},
		}),

		"ModelScoreChannel": object(gin.H{
			"channel": fieldString("渠道展示名"),
			"wait_ms": fieldNumber("number", ""),
			"success": fieldNumber("number", ""),
			"samples": fieldNumber("integer", ""),
		}),

		// ---------- 价格 ----------
		// ShareModel 是公开分享页下发的模型条目: 名称、媒体形态与对客单价(供货价上浮后, 与计费同口径)。
		"ShareModel": object(gin.H{
			"name":       fieldString("模型名称"),
			"kind":       fieldString("媒体形态: 空串为文本模型; image 图片, video 视频"),
			"user_price": ref("LLMPrice"),
			"user_media": refWithDesc("MediaPrice", "媒体定价; 文本模型省略"),
		}, "name", "user_price"),

		"LLMPrice": object(gin.H{
			"input":       fieldNumber("number", "每百万输入 token 单价"),
			"output":      fieldNumber("number", "每百万输出 token 单价"),
			"cache_read":  fieldNumber("number", "每百万缓存命中读取 token 单价"),
			"cache_write": fieldNumber("number", "每百万缓存写入 token 单价"),
		}),

		"MediaPrice": object(gin.H{
			"per_image":  fieldNumber("number", "按张单价"),
			"per_second": fieldNumber("number", "按秒单价"),
			"resolution": gin.H{"type": "object", "additionalProperties": gin.H{"type": "number"}, "description": "可选的分档定价, 键为档位名(如 480p / 720p / 1080p)"},
		}),

		"LLMInfo": object(gin.H{
			"name": fieldString("模型名, 主键; 写入侧自动转小写去空格"),
		}),

		"ModelNameRequest": object(gin.H{
			"name": fieldString("要删除的模型名"),
		}, "name"),

		// ---------- API Key ----------
		"APIKey": object(gin.H{
			"id":      fieldNumber("integer", "主键"),
			"user_id": fieldNumber("integer", "归属用户, 计费与数据隔离的依据; 服务端强制覆盖为当前登录账号"),
			"name":    fieldString("Key 名称"),
			"api_key": fieldString("转发用的明文密钥。创建请求必须省略或留空，由后端生成高熵随机串；非空自选密钥被拒绝；" +
				"响应侧只在创建响应里出现这一次, 列表/详情/更新/统计响应一律不返回。" +
				"更新请求携带本字段一律 400 —— Key 不可替换, 只能新建; 指纹 key_hash 永不出现在任何 API。" +
				"存量 Key(旧库迁移/旧备份导入)不受影响, 不强制轮换; 前端创建表单本就不提交 api_key, 只有外部脚本需要删除该字段。"),
			"key_prefix": gin.H{"type": "string", "readOnly": true, "description": "掩码前缀(响应只读, 创建/更新请求忽略)。明文签发后不可再取, " +
				"列表与详情只给 key_prefix + key_last4 两段, 展示形如 ${prefix}********${last4}"},
			"key_last4":           gin.H{"type": "string", "readOnly": true, "description": "掩码尾段 4 位(响应只读, 创建/更新请求忽略); 与 key_prefix 组成掩码识别这把 Key"},
			"enabled":             fieldBool(""),
			"expire_at":           fieldNumber("integer", "过期 Unix 秒; 0 表示永不过期"),
			"max_cost":            fieldNumber("number", "累计消费上限; 0 表示不限"),
			"supported_models":    gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "允许访问的分组名白名单, 空表示不限制"},
			"routing_preference":  gin.H{"type": "string", "enum": []string{"inherit", "balanced", "price", "latency", "success"}, "description": "API Key 选路偏好; inherit 跟随分组策略"},
			"quota_requests":      fieldNumber("integer", "每日请求数配额, 0 不限"),
			"quota_input_tokens":  fieldNumber("integer", "每日输入 token 配额, 0 不限"),
			"quota_output_tokens": fieldNumber("integer", "每日输出 token 配额, 0 不限"),
			"quota_reset_hour":    fieldNumber("integer", "配额重置时刻 0-23, 默认 0 点"),
		}),

		// ---------- 设置与备份 ----------
		"Setting": object(gin.H{
			"key":   fieldString("设置键, 主键; 命名形如 alert_webhook_url、register_user_enabled"),
			"value": fieldString("设置值; 一律是字符串, 数值与布尔由后端按 key 校验并解释"),
		}, "key", "value"),

		"DBDump": object(gin.H{
			"version":        fieldNumber("integer", "转储格式版本; 非当前版本导入直接拒绝"),
			"exported_at":    datetime("导出时刻"),
			"channels":       gin.H{"type": "array", "items": ref("ChannelDump"), "description": "渠道数据"},
			"channel_keys":   gin.H{"type": "array", "items": ref("ChannelKeyDump")},
			"channel_models": gin.H{"type": "array", "items": ref("ChannelModelDump")},
			"channel_grants": gin.H{"type": "array", "items": ref("ChannelGrantDump")},
			"groups":         gin.H{"type": "array", "items": ref("GroupDump")},
			"group_items":    gin.H{"type": "array", "items": ref("GroupItemDump")},
			"llm_infos":      gin.H{"type": "array", "items": ref("LLMInfo")},
			"api_keys":       gin.H{"type": "array", "items": ref("APIKey"), "description": "备份行带 key_hash 指纹与掩码片段, 不含明文; 旧版明文 api_key 字段仅导入兼容, 导入即换算指纹丢弃明文"},
			"settings":       gin.H{"type": "array", "items": ref("Setting")},
			"stats_total":    gin.H{"type": "array", "items": ref("StatsTotal")},
			"stats_daily":    gin.H{"type": "array", "items": ref("StatsDaily")},
			"stats_hourly":   gin.H{"type": "array", "items": ref("StatsHourly")},
		}),

		"DBImportResult": object(gin.H{
			"rows_affected": gin.H{"type": "object", "additionalProperties": gin.H{"type": "integer"}, "description": "每张表受影响的行数(按表为 insert 或 upsert)"},
		}),

		// ---------- 统计 ----------
		"StatsMetrics": object(gin.H{
			"input_token":     fieldNumber("integer", "累计输入 token"),
			"output_token":    fieldNumber("integer", "累计输出 token"),
			"input_cost":      fieldNumber("number", "累计输入成本"),
			"output_cost":     fieldNumber("number", "累计输出成本"),
			"wait_time":       fieldNumber("integer", "累计等待耗时, 用于算平均首响应"),
			"request_success": fieldNumber("integer", "成功请求数"),
			"request_failed":  fieldNumber("integer", "失败请求数"),
		}),

		"StatsTotal": withStatsFlatten(gin.H{}),

		"StatsHourly": withStatsFlatten(gin.H{
			"hour": fieldNumber("integer", "0-23 的小时刻度"),
			"date": fieldString("记录最后更新日期, 形如 20060102"),
		}),

		"StatsDaily": withStatsFlatten(gin.H{
			"date": fieldString("日期, 形如 20060102"),
		}),

		"StatsAPIKey": withStatsFlatten(gin.H{
			"api_key_id": fieldNumber("integer", "API Key 主键"),
		}),

		"StatsChannelDaily": withStatsFlatten(gin.H{
			"channel_id": fieldNumber("integer", ""),
			"date":       fieldString("日期, 形如 20060102"),
		}),

		"StatsChannelModelDaily": withStatsFlatten(gin.H{
			"channel_model_id": fieldNumber("integer", ""),
			"date":             fieldString("日期, 形如 20060102"),
		}),

		"BillingAggregate": object(gin.H{
			"date":              fieldString("日期"),
			"input_token":       fieldNumber("integer", ""),
			"output_token":      fieldNumber("integer", ""),
			"cache_read_token":  fieldNumber("integer", ""),
			"cache_write_token": fieldNumber("integer", ""),
			"request_count":     fieldNumber("integer", "请求笔数"),
			"user_cost":         fieldNumber("number", "用户支出(上浮后)"),
			"owner_revenue":     fieldNumber("number", "渠道发布者收入(按供货价)"),
			"platform_revenue":  fieldNumber("number", "平台收入(上浮部分)"),
		}),

		"BillingRecord": object(gin.H{
			"id":                fieldNumber("integer", "明细主键"),
			"request_id":        fieldNumber("integer", "对应的转发日志 ID"),
			"user_id":           fieldNumber("integer", "付费方: API Key 归属用户"),
			"api_key_id":        fieldNumber("integer", ""),
			"group_id":          fieldNumber("integer", ""),
			"group_model":       fieldString("客户端模型名, 即分组名"),
			"channel_id":        fieldNumber("integer", ""),
			"owner_id":          fieldNumber("integer", "发布者: 渠道归属用户, 收入按供货价计入"),
			"share_code":        fieldString(""),
			"model_name":        fieldString("上游模型名"),
			"supply_price":      ref("LLMPrice"),
			"user_price":        ref("LLMPrice"),
			"markup_ratio":      fieldNumber("number", "结算时的上浮比例快照; 改价不影响历史解释"),
			"price_source":      fieldString("价格口径, 如 channel_model 表示渠道模型发布价"),
			"input_token":       fieldNumber("integer", ""),
			"output_token":      fieldNumber("integer", ""),
			"cache_read_token":  fieldNumber("integer", ""),
			"cache_write_token": fieldNumber("integer", ""),
			"media_supply":      ref("MediaPrice"),
			"media_user":        ref("MediaPrice"),
			"media_units":       ref("MediaUnits"),
			"user_cost":         fieldNumber("number", ""),
			"owner_revenue":     fieldNumber("number", ""),
			"platform_revenue":  fieldNumber("number", ""),
			"status":            gin.H{"type": "string", "enum": []string{"settled", "refunded"}},
			"created_at":        datetime(""),
		}),

		"MediaUnits": object(gin.H{
			"images":     fieldNumber("integer", "张数"),
			"seconds":    fieldNumber("number", "秒数"),
			"resolution": fieldString("实际使用的分档"),
		}),

		// BalanceFlow 是单次请求的余额变动流水(op.BalanceFlow): 预扣、结算、释放三者逐笔对应,
		// 对账时按 request_id 就能复原这笔请求在余额上的完整轨迹。
		"BalanceFlow": object(gin.H{
			"request_id":       fieldNumber("integer", "关联的转发日志 ID"),
			"user_id":          fieldNumber("integer", "余额所属用户 ID"),
			"kind":             fieldString("reserve 预扣 | settle 结算 | release 释放"),
			"amount":           fieldNumber("number", "本笔金额"),
			"settled":          fieldBool("该笔是否已完成结算"),
			"created_at":       datetime("发生时间"),
			"user_cost":        fieldNumber("number", "用户侧实付"),
			"owner_revenue":    fieldNumber("number", "渠道归属方分成"),
			"platform_revenue": fieldNumber("number", "平台留存"),
		}),

		"BillingLedger": object(gin.H{
			"id":            fieldNumber("integer", ""),
			"user_id":       fieldNumber("integer", ""),
			"kind":          fieldString("流水类型: 充值、扣费、冻结、解冻、退款等"),
			"amount":        fieldNumber("number", "本笔变动金额"),
			"frozen":        fieldNumber("number", "变动后的冻结额"),
			"cost":          fieldNumber("number", "累计消费"),
			"balance_after": fieldNumber("number", "变动后的可用余额; 历史回填行为 0 表示未知"),
			"request_id":    fieldNumber("integer", "关联的转发日志 ID"),
			"model_name":    fieldString(""),
			"actor_id":      fieldNumber("integer", "操作者账号 ID; 系统自动记账为 0"),
			"actor_name":    fieldString(""),
			"note":          fieldString(""),
			"created_at":    datetime(""),
		}),

		// ---------- 转发日志 ----------
		"RelayRequest": object(gin.H{
			"id":                      fieldNumber("integer", "请求 ID"),
			"user_id":                 fieldNumber("integer", ""),
			"api_key_id":              fieldNumber("integer", ""),
			"api_key_name":            fieldString(""),
			"group_id":                fieldNumber("integer", ""),
			"model":                   fieldString("客户端请求的模型名, 即分组名"),
			"reasoning_effort":        fieldString("思考等级; 未指定为空"),
			"protocol":                fieldNumber("integer", "入站协议位, 单个位而非掩码"),
			"status":                  gin.H{"type": "string", "enum": []string{"success", "upstream_error", "client_error", "interrupted", "in_progress"}},
			"client_ip":               fieldString("调用方地址, 供风控与排查"),
			"started_at":              datetime(""),
			"finished_at":             datetime(""),
			"duration_ns":             fieldNumber("integer", "总耗时纳秒, 未结束为 0"),
			"first_token_duration_ns": fieldNumber("integer", "首字节耗时纳秒; 非流式为 0"),
			"stream_duration_ns":      fieldNumber("integer", "流式传输耗时纳秒"),
			"response_duration_ns":    fieldNumber("integer", "非流式响应耗时纳秒"),
			"round":                   fieldNumber("integer", "最后一轮的序号"),
			"prompt_tokens":           fieldNumber("integer", ""),
			"completion_tokens":       fieldNumber("integer", ""),
			"cached_tokens":           fieldNumber("integer", ""),
			"cache_write_tokens":      fieldNumber("integer", ""),
			"cost":                    fieldNumber("number", ""),
			"output_chars":            fieldNumber("integer", "流式过程估算的输出字符数, 仅供展示, 不参与结算"),
			"target_channel_key":      fieldString("最后一轮选中的渠道名与凭据名, 空格分隔"),
			"target_channel_code":     fieldString("最后一轮落地渠道的发布编码; 未发布为空"),
			"target_model":            fieldString("最后一轮实际请求上游的模型名"),
			"target_protocol":         fieldNumber("integer", "与 protocol 不同即本轮做了跨协议转换; 0 表示尚未选出"),
			"error":                   fieldString("最后一轮失败原因; 请求结束后即最终错误"),
			"request_body":            fieldString("归档的请求体(列表里可能被截断, 完整原文走 request-body/{id})"),
			"route_events":            gin.H{"type": "array", "items": ref("RouteState"), "description": "选路过程轨迹; 未发生选路时省略"},
			"response_body":           fieldString("归档的响应体"),
		}),

		// RequestState 是 relay.RequestState 的线上形状: 转发日志列表与状态流 SSE 都用它。
		// 耗时类字段是 time.Duration, JSON 里是纳秒整数; 客户端自行换算成毫秒展示。
		// LLMUsage 是上游用量回包(llm.Usage)的线上形状; 网关自己算的用量与费用写在 RequestState 顶层。
		// details 字段来自上游, 不同厂商给的子字段不一样, 缺的直接不出现在 JSON 里。
		"LLMUsage": object(gin.H{
			"prompt_tokens":                     fieldNumber("integer", "输入 token 数(含缓存命中)"),
			"completion_tokens":                 fieldNumber("integer", "输出 token 数"),
			"total_tokens":                      fieldNumber("integer", "合计 token 数"),
			"prompt_tokens_details":             refWithDesc("LLMUsagePromptDetails", "输入侧细分; 上游未给时为 null"),
			"completion_tokens_details":         refWithDesc("LLMUsageCompletionDetails", "输出侧细分; 上游未给时为 null"),
			"prompt_modality_token_details":     gin.H{"type": "array", "items": gin.H{"type": "object", "properties": gin.H{"modality": "string", "token_count": "integer"}}, "description": "Gemini 专用: 按模态拆分的输入 token 数组, 元素为 { modality, token_count }; 上游未给时省略"},
			"completion_modality_token_details": gin.H{"type": "array", "items": gin.H{"type": "object", "properties": gin.H{"modality": "string", "token_count": "integer"}}, "description": "Gemini 专用: 按模态拆分的输出 token 数组, 元素为 { modality, token_count }; 上游未给时省略"},
			"cost":                              fieldNumber("number", "上游侧算出的费用; 未配置价格时省略"),
		}),

		"LLMUsagePromptDetails": object(gin.H{
			"audio_tokens":              fieldNumber("integer", "音频输入 token"),
			"cached_tokens":             fieldNumber("integer", "缓存命中 token"),
			"write_cached_tokens":       fieldNumber("integer", "缓存写入 token(Anthropic); 未给时省略"),
			"write_cached_5min_tokens":  fieldNumber("integer", "5 分钟 TTL 缓存写入 token; 未给时省略"),
			"write_cached_1hour_tokens": fieldNumber("integer", "1 小时 TTL 缓存写入 token; 未给时省略"),
			"image_tokens":              fieldNumber("integer", "图像输入 token(图片生成); 未给时省略"),
			"text_tokens":               fieldNumber("integer", "文本输入 token(图片生成); 未给时省略"),
		}),

		"LLMUsageCompletionDetails": object(gin.H{
			"audio_tokens":               fieldNumber("integer", "音频输出 token"),
			"reasoning_tokens":           fieldNumber("integer", "思考 token"),
			"accepted_prediction_tokens": fieldNumber("integer", "被采纳的预测输出 token"),
			"rejected_prediction_tokens": fieldNumber("integer", "被丢弃的预测输出 token"),
		}),

		"RequestState": object(gin.H{
			"id":                   fieldNumber("integer", "请求 ID, 跨进程重启唯一"),
			"status":               fieldString("running / committed / success / failed / canceled"),
			"started_at":           datetime("请求到达时间"),
			"duration":             fieldNumber("integer", "总耗时(纳秒); 未结束为 0"),
			"first_token_duration": fieldNumber("integer", "流式首字节耗时(纳秒); 非流式为 0"),
			"stream_duration":      fieldNumber("integer", "流式传输耗时(纳秒); 非流式为 0"),
			"response_duration":    fieldNumber("integer", "非流式完整响应耗时(纳秒); 流式为 0"),
			"client_ip":            fieldString("客户端 IP"),
			"user_id":              fieldNumber("integer", "API Key 归属用户 ID"),
			"model":                fieldString("客户端请求的模型名(分组名)"),
			"reasoning_effort":     fieldString("客户端指定的思考等级; 未指定为空"),
			"protocol":             fieldString("入站协议"),
			"group_id":             fieldNumber("integer", "承载分组 ID"),
			"api_key_name":         fieldString("发起请求的 Key 名称"),
			"usage":                ref("LLMUsage"),
			"cost":                 fieldNumber("number", "累计费用"),
			"output_chars":         fieldNumber("integer", "流式实时累计的输出字符数; 仅展示, 不参与结算"),
			"round":                fieldNumber("integer", "最新一轮循环序号, 人工中止按此匹配"),
			"round_started_at":     datetime("最新一轮上游请求开始时间"),
			"target_channel_key":   fieldString("本轮选中渠道与 Key 名称, 空格分隔"),
			"target_channel_code":  fieldString("本轮落地渠道发布编码; 未发布为空"),
			"target_model":         fieldString("本轮实际请求上游的模型名"),
			"target_protocol":      fieldString("本轮实际协议; 与 protocol 不同即为跨协议转换"),
			"sending":              fieldBool("本轮是否仍在等待上游响应"),
			"error":                fieldString("本轮失败原因; 请求结束后即最终错误"),
			"route_events":         gin.H{"type": "array", "items": ref("RouteState"), "description": "选路过程轨迹; 未发生选路时省略"},
		}),

		"RelayRequestAttempt": object(gin.H{
			"id":              fieldNumber("integer", ""),
			"request_id":      fieldNumber("integer", ""),
			"round":           fieldNumber("integer", "轮次序号, 从 1 开始"),
			"group_item_id":   fieldNumber("integer", ""),
			"channel_id":      fieldNumber("integer", ""),
			"channel_code":    fieldString(""),
			"target_model":    fieldString(""),
			"target_protocol": fieldNumber("integer", ""),
			"started_at":      datetime(""),
			"first_token_ms":  fieldNumber("integer", ""),
			"duration_ms":     fieldNumber("integer", ""),
			"status":          gin.H{"type": "string", "enum": []string{"success", "failed", "limited", "canceled", "timeout", "client_error", "policy_blocked", "auth_error", "quota", "overloaded", "model_missing"}},
			"error":           fieldString(""),
			"decision":        fieldString("本轮路由决策说明(JSON 文本)"),
		}),

		"ReplayRequest": object(gin.H{
			"request_body": fieldString("改写后的请求体 JSON; 留空则原样重放归档的请求体"),
			"api_key_id":   fieldNumber("integer", "改用其他 API Key 重放; 0 表示沿用原 Key"),
		}),

		"ReplayResult": object(gin.H{
			"status_code":   fieldNumber("integer", "上游返回的状态码"),
			"duration_ms":   fieldNumber("integer", "本次重放耗时"),
			"model":         fieldString("原记录的模型名"),
			"request_body":  fieldString("实际发出的请求体"),
			"response_body": fieldString("上游响应体原文, 读取上限 8 MB"),
			"content_type":  fieldString("上游响应的 Content-Type"),
		}),

		// ---------- 运维 ----------
		"OpsOverview": object(gin.H{
			"requests_24h":      fieldNumber("integer", "近 24 小时请求数"),
			"success_24h":       fieldNumber("integer", ""),
			"failed_24h":        fieldNumber("integer", ""),
			"input_tokens_24h":  fieldNumber("integer", ""),
			"output_tokens_24h": fieldNumber("integer", ""),
			"cost_24h":          fieldNumber("number", ""),
			"active_requests":   fieldNumber("integer", "当前在途请求数"),
			"channels_total":    fieldNumber("integer", ""),
			"channels_enabled":  fieldNumber("integer", ""),
			"alerts_24h":        fieldNumber("integer", "近 24 小时告警条数"),
		}),

		"BillingReconcile": object(gin.H{
			"id":               fieldNumber("integer", ""),
			"day":              fieldString("自然日 YYYYMMDD; 对账固定从昨天开始回溯, 当天仍在写入会得出漏结算的假阳性"),
			"records":          fieldNumber("integer", "计费明细笔数"),
			"user_cost":        fieldNumber("number", ""),
			"owner_revenue":    fieldNumber("number", ""),
			"platform_revenue": fieldNumber("number", ""),
			"duplicated":       fieldNumber("integer", "重复结算(同一请求多条明细)"),
			"missing":          fieldNumber("integer", "有用量但缺少计费明细的请求"),
			"stale_hold":       fieldNumber("integer", "超时未结算且已回滚的预扣"),
			"unsettled":        fieldNumber("integer", "仍未结算的预扣"),
			"status":           fieldString("对账结论: balanced 对平 / mismatch 发现差异 / review 存在待核查项 / explained 差异已查清并留存说明"),
			"detail":           fieldString("差异明细文本"),
			"note":             fieldString("人工处置说明: 查清「为什么差」但数字不可改时留下的结论; 只有该字段可由人工改写"),
			"noted_at":         datetime("说明写入时刻"),
			"noted_by":         fieldNumber("integer", "写说明的管理员用户 ID"),
			"created_at":       datetime(""),
			"updated_at":       datetime(""),
		}),

		"ReconcileNoteRequest": object(gin.H{
			"day":  fieldString("账期 YYYYMMDD"),
			"note": fieldString("处置说明; 传空串表示撤销, 状态交回按差异重算"),
		}, "day"),

		"ArchiveResult": object(gin.H{
			"cutoff":           datetime("本次清理的时间界"),
			"archived":         fieldNumber("integer", "归档条数"),
			"file":             fieldString("归档文件名; 未开启归档时为空"),
			"requests_deleted": fieldNumber("integer", ""),
			"attempts_deleted": fieldNumber("integer", ""),
			"body_cleared":     fieldNumber("integer", "清空请求体/响应体的记录数"),
			"archive_error":    fieldString("归档失败原因, 成功时省略"),
			"archive_enabled":  fieldBool("归档开关当前状态"),
		}),

		"ArchiveFile": object(gin.H{
			"name":     fieldString("文件名(.ndjson)"),
			"size":     fieldNumber("integer", "字节数"),
			"mod_time": datetime(""),
		}),

		"AutoBackupResult": object(gin.H{
			"file":      fieldString("备份文件名"),
			"size":      fieldNumber("integer", "字节数"),
			"verified":  fieldBool("备份后校验是否通过"),
			"pruned":    fieldNumber("integer", "本次清理掉的旧备份数"),
			"encrypted": fieldBool("是否加密备份"),
			"checksum":  fieldString("校验和"),
			"remote":    gin.H{"type": "string", "enum": []string{"ok", "skipped", "failed"}, "description": "远端上传结果"},
		}),

		"BackupFile": object(gin.H{
			"name":      fieldString(""),
			"size":      fieldNumber("integer", ""),
			"mod_time":  datetime(""),
			"encrypted": fieldBool("文件名以 .enc 结尾"),
			"checksum":  fieldBool("同目录是否存在配套校验文件"),
		}),

		"ChannelHealthRecord": object(gin.H{
			"id":           fieldNumber("integer", ""),
			"channel_id":   fieldNumber("integer", ""),
			"channel_code": fieldString(""),
			"latency_ms":   fieldNumber("integer", ""),
			"status_code":  fieldNumber("integer", "探测请求的 HTTP 状态码"),
			"error":        fieldString(""),
			"healthy":      fieldBool(""),
			"checked_at":   datetime(""),
		}),

		"ChannelAlert": object(gin.H{
			"id": fieldNumber("integer", ""),
			// 主体是"谁出事了": 真实渠道名 / 用户名 / 任务名 / 系统; 由产生告警的一方填好。
			"channel_id": fieldNumber("integer", "0 表示系统级告警"),
			"subject":    fieldString("告警主体: 渠道真名、用户名、任务名或系统"),
			// 分享码只用于对外分享的渠道, 不能当渠道名用, 故保留这一列供老数据回退。
			"channel_code": fieldString("渠道分享码(可能为空)"),
			"kind": gin.H{"type": "string", "description": "告警类型", "enum": []string{
				"down", "recovered", "degraded", "key_disabled", "model_disabled",
				"mismatch", "billing_failed", "backup_failed", "archive_failed",
				"low_balance", "quota_exceeded", "task_failed", "task_recovered",
				"update_available", "test",
			}},
			"reason": fieldString("触发原因(用于去重, 不含时间戳等易变值)"),
			// 明细与外发通知取自同一批结构化字段: 延迟、错误、建议等逐行文本。
			"detail":     fieldString("结构化明细, 每行 \"字段: 值\""),
			"notified":   fieldBool("是否已推送到告警渠道"),
			"created_at": datetime(""),
		}),

		"AlertRule": object(gin.H{
			"key":         fieldString("对应的设置键; 改值走 /api/v1/setting/set"),
			"value":       fieldNumber("integer", "当前生效值"),
			"default":     fieldNumber("integer", "系统默认值"),
			"unit":        fieldString("单位说明"),
			"description": fieldString("这条判据的含义"),
		}),

		// ---------- 限流 ----------
		"RateLimitPolicy": object(gin.H{
			"id":         fieldNumber("integer", "主键; 保存时带则为更新"),
			"scope_type": gin.H{"type": "string", "enum": []string{"system", "user", "apikey", "group"}, "description": "限流口径"},
			"scope_id":   fieldNumber("integer", "口径对应的对象 ID; system 恒为 0"),
			"model_name": fieldString("限定的模型名; 空表示该 scope 的全部模型"),
			"rpm":        fieldNumber("integer", "每分钟请求数上限, 0 不限"),
			"tpm":        fieldNumber("integer", "每分钟 token 上限, 0 不限"),
			"concurrent": fieldNumber("integer", "并发上限, 0 不限"),
			"enabled":    fieldBool(""),
			"created_at": datetime(""),
			"updated_at": datetime(""),
		}),

		"PolicyIDRequest": object(gin.H{
			"id": fieldNumber("integer", "待删除的策略 ID, 必须大于 0"),
		}, "id"),

		"MetricsResetRequest": object(gin.H{
			"channel_model_id": fieldNumber("integer", "渠道模型 ID; 与 group_id+item_id 二选一, 清该渠道模型下全部成员观测"),
			"group_id":         fieldNumber("integer", "分组 ID; 与 item_id 成对使用, 只清单个成员观测"),
			"item_id":          fieldNumber("integer", "分组成员 ID; 与 group_id 成对使用"),
		}),

		"RateLimitInspect": object(gin.H{
			"budgets":    gin.H{"type": "array", "items": ref("RateLimitBudget"), "description": "该 scope 覆盖的每个模型的独立预算; 未指定 model 时逐项给出"},
			"scope_type": fieldString(""),
			"scope_id":   fieldNumber("integer", ""),
			"model_name": fieldString("请求限定的模型名; 空表示全部"),
			"rpm":        fieldNumber("integer", ""),
			"tpm":        fieldNumber("integer", ""),
			"concurrent": fieldNumber("integer", ""),
			"active":     fieldNumber("integer", "当前并发占用"),
			"requests":   fieldNumber("integer", "当前窗口已用请求数"),
			"tokens":     fieldNumber("integer", "当前窗口已用 token"),
			"reset_at":   fieldNumber("integer", "窗口重置时刻(Unix 秒)"),
			"blocked":    fieldBool("是否处于封禁期"),
		}),

		"RateLimitBudget": object(gin.H{
			"model_name": fieldString(""),
			"rpm":        fieldNumber("integer", ""),
			"tpm":        fieldNumber("integer", ""),
			"concurrent": fieldNumber("integer", ""),
			"requests":   fieldNumber("integer", ""),
			"tokens":     fieldNumber("integer", ""),
			"active":     fieldNumber("integer", ""),
			"reset_at":   fieldNumber("integer", ""),
		}),

		"RateLimitUsageHourly": object(gin.H{
			"id":            fieldNumber("integer", ""),
			"scope_type":    fieldString(""),
			"scope_id":      fieldNumber("integer", ""),
			"hour":          datetime("小时桶起点"),
			"requests":      fieldNumber("integer", ""),
			"input_tokens":  fieldNumber("integer", ""),
			"output_tokens": fieldNumber("integer", ""),
			"rejected":      fieldNumber("integer", "被限流拒绝的请求数"),
		}),

		"BillingSettlementTask": object(gin.H{
			"id":              fieldNumber("integer", "任务ID"),
			"reservation_id":  fieldNumber("integer", "原预扣ID"),
			"request_id":      fieldString("调用ID；字符串避免JavaScript精度损失"),
			"user_id":         fieldNumber("integer", "付费用户"),
			"status":          fieldString("pending | retry | manual | completed"),
			"attempts":        fieldNumber("integer", "尝试次数"),
			"next_attempt_at": datetime("下一次尝试时间"),
			"last_error":      fieldString("最近错误，不含计费快照"),
			"created_at":      datetime(""), "updated_at": datetime(""),
		}),

		// ---------- 审计 ----------
		"AuditLog": object(gin.H{
			"id":         fieldNumber("integer", ""),
			"user_id":    fieldNumber("integer", "操作者账号 ID"),
			"username":   fieldString("操作者用户名快照; 账号改名后历史仍可读"),
			"action":     fieldString("动作标识, 如 user.update、setting.set、channel.model-price"),
			"target":     fieldString("目标描述, 如 username#id、设置键、渠道号"),
			"detail":     fieldString("变更明细文本"),
			"created_at": datetime(""),
		}),

		// ---------- 升级 ----------
		"UpdateStatus": object(gin.H{
			"current_version":     fieldString("构建时写入的运行版本"),
			"paused":              fieldBool("管理员是否暂停了升级"),
			"check_enabled":       fieldBool("是否启用自动检查"),
			"check_interval_min":  fieldNumber("integer", ""),
			"last_check_at":       fieldNumber("integer", "上次检查的 Unix 秒"),
			"update_available":    fieldBool(""),
			"up_to_date":          fieldBool(""),
			"check_failed":        fieldBool(""),
			"check_error":         fieldString("检查失败原因"),
			"platform":            fieldString("目标平台, 如 linux-amd64"),
			"expected_asset":      fieldString("期望的资产文件名"),
			"asset_missing":       fieldBool("最新 release 里没有本平台资产"),
			"latest_version":      fieldString(""),
			"latest_published_at": fieldString(""),
			"latest_body":         fieldString("release 说明正文"),
			"release_url":         fieldString(""),
			"asset_name":          fieldString(""),
			"asset_size":          fieldNumber("integer", ""),
			"asset_sha256":        fieldString("资产校验和"),
			"download_url":        fieldString(""),
		}),

		"UpdateResult": object(gin.H{
			"from":        fieldString("升级前版本"),
			"to":          fieldString("升级后版本"),
			"asset":       fieldString("使用的资产文件"),
			"exec_path":   fieldString("被替换的可执行文件路径"),
			"backup_path": fieldString("旧二进制的备份路径"),
		}),

		// ---------- 转发面引用的最小价格视图(转储用) ----------
		"ChannelDump":      refWithDesc("ChannelDetail", "库内渠道记录: ChannelDetail 的可编辑字段加上主键、归属、发布状态与累计统计。"),
		"ChannelKeyDump":   refWithDesc("ChannelKeyConfig", "库内凭据记录: 配置字段加主键、渠道 ID 与累计统计。"),
		"ChannelModelDump": refWithDesc("ChannelModelListing", "库内渠道模型记录: 上架与价格字段加主键、渠道 ID 与累计统计。"),
		"ChannelGrantDump": refWithDesc("ChannelGrantConfig", "库内授权记录: 模型、凭据与协议位掩码, 另含各自主键与统计。"),
		"GroupDump":        refWithDesc("GroupResponse", "库内分组记录: 配置与成员, 不含运行状态。"),
		"GroupItemDump":    refWithDesc("GroupItem", "库内分组成员记录。"),
	}
}

// ---------- 小工具: 少写样板, 让上面的表保持可读 ----------

// object 组装一个 JSON Schema 对象; required 列出必填字段名。
func object(props gin.H, required ...string) gin.H {
	schema := gin.H{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// withStatsFlatten 把 StatsMetrics 的七个累计字段并进去: Go 的匿名嵌入在 JSON 里是展平的,
// 文档也照展平写, 而不是让消费方去猜一个并不存在的 stats 键。
func withStatsFlatten(props gin.H) gin.H {
	metrics := statsMetricsProperties()
	for key, value := range metrics {
		if _, exists := props[key]; !exists {
			props[key] = value
		}
	}
	return object(props)
}

// withChannelConfig 把 ChannelConfig 的字段并进去, 用途同 withStatsFlatten:
// Go 的匿名嵌入在 JSON 里是展平的, 文档不该写成 allOf 或嵌套的 config 对象。
func withChannelConfig(props gin.H) gin.H {
	config := channelConfigProperties()
	for key, value := range config {
		if _, exists := props[key]; !exists {
			props[key] = value
		}
	}
	return props
}

// channelConfigProperties 与 ChannelConfig 声明共用同一份字段表, 抽出来只为避免两处各写一遍。
func channelConfigProperties() gin.H {
	return gin.H{
		"name":                         fieldString("渠道名称, 在同一归属内唯一"),
		"dialect":                      gin.H{"type": "string", "description": "上游方言, 决定出站转换器的厂商特化配置", "enum": []string{"generic", "openai", "anthropic", "gemini", "deepseek", "moonshot", "zai", "xai", "xunfei"}},
		"enabled":                      fieldBool("为假时不参与选路, 配置与统计保留"),
		"base_url":                     fieldString("上游地址, 各协议共用"),
		"openai_chat_completion_path":  fieldString("留空由后端填默认 /v1/chat/completions"),
		"openai_response_path":         fieldString("留空由后端填默认 /v1/responses"),
		"openai_image_generation_path": fieldString("留空由后端填默认 /v1/images/generations"),
		"openai_image_edit_path":       fieldString("留空由后端填默认 /v1/images/edits"),
		"anthropic_message_path":       fieldString("留空由后端填默认 /v1/messages"),
		"proxy":                        fieldBool("是否为该渠道走代理"),
		"channel_proxy":                fieldString("渠道专用代理地址; 留空表示不用"),
		"custom_header":                gin.H{"type": "array", "items": ref("CustomHeader"), "description": "追加到上游请求的 Header"},
		"param_override":               fieldString("请求参数覆盖配置(JSON 文本); 留空表示不覆盖"),
		"match_regex":                  fieldString("拉取模型列表时的过滤表达式; 留空表示不过滤"),
		"model_auto_add":               fieldBool("保存后自动探测上游模型并入渠道: 只增不删, 已有授权的协议位按 OR 合并"),
		"key_rotation_strategy":        gin.H{"type": "string", "enum": []string{"roundrobin", "random", "failover"}, "description": "同渠道多凭据时的轮换策略"},
	}
}

// statsMetricsProperties 返回嵌入结构 StatsMetrics 展平后的字段; 描述只在首个使用处给全,
// 其余靠引用同一定义即可, 这里逐处给一遍以免消费方漏读单位。
func statsMetricsProperties() gin.H {
	return gin.H{
		"input_token":     fieldNumber("integer", "累计输入 token"),
		"output_token":    fieldNumber("integer", "累计输出 token"),
		"input_cost":      fieldNumber("number", "累计输入成本"),
		"output_cost":     fieldNumber("number", "累计输出成本"),
		"wait_time":       fieldNumber("integer", "累计等待耗时纳秒, 除以成功数即平均首响应"),
		"request_success": fieldNumber("integer", "成功请求数"),
		"request_failed":  fieldNumber("integer", "失败请求数"),
	}
}

func ref(name string) gin.H {
	return gin.H{"$ref": "#/components/schemas/" + name}
}

// refWithDesc 给一个复用他处定义的字段补一句上下文说明。
func refWithDesc(name, description string) gin.H {
	schema := ref(name)
	schema["description"] = description
	return schema
}

func fieldString(desc string) gin.H {
	schema := gin.H{"type": "string"}
	if desc != "" {
		schema["description"] = desc
	}
	return schema
}

func fieldNumber(typ, desc string) gin.H {
	schema := gin.H{"type": typ}
	if desc != "" {
		schema["description"] = desc
	}
	return schema
}

func fieldBool(desc string) gin.H {
	schema := gin.H{"type": "boolean"}
	if desc != "" {
		schema["description"] = desc
	}
	return schema
}

func datetime(desc string) gin.H {
	schema := gin.H{"type": "string", "format": "date-time"}
	if desc != "" {
		schema["description"] = desc
	}
	return schema
}

// protocolBitmask 协议位掩码的统一描述: 具体位值写在描述里而不是 enum 里,
// 因为它本质是可按位或的组合, 不是枚举值集合。
func protocolBitmask(desc string) gin.H {
	bits := "协议位掩码: 2=openai_chat_completion, 4=openai_response, 8=anthropic_message, 16=openai_image, 32=openai_video(预留), 64=xunfei_spark_websocket; 多个协议按位或"
	if desc != "" {
		bits = desc + "; " + bits
	}
	return gin.H{"type": "integer", "description": bits}
}
