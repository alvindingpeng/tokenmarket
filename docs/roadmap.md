# 功能路线图(待办)

> 本文档记录**已完成**的版本流水与历史决策, 防止跨会话丢失。
> **还没做的**事项统一记录在 `docs/backlog-unimplemented.md`(2026-10-21 建立), 两文分工: 本文记已做, 那篇记未做。

## 第二批后续(2026-10-08 审计产出的五件事)

| 优先级 | 事项 | 状态 |
|---|---|---|
| P0 | 前端接线: 第二批后端 API + 10 个新设置项上界面(运维中心页/设置面板/三语) | ✅ 已完成 |
| P1 | 告警闭环: 对账差异 + 健康指标(降级阈值/连续失败防抖/恢复补发)经 Webhook 外发 | ✅ 已完成 |
| P1 | 备份加固: 校验和 + AES-256 加密 + WebDAV 异地推送 + CLI 解密恢复 | ✅ 已完成 |
| P2 | 日志检索增强: IP/模型/渠道/状态筛选 + CSV 导出 | ✅ 已完成 |
| P2 | 运维仪表盘 + /healthz + Prometheus /metrics | ✅ 已完成 |

## 计费修复(2026-10-08)

用户余额未按调用费用扣除已修复: 根因是 admin 免计费豁免 + 旧构建预扣释放; 现已全量计费, 结算失败进告警闭环。 见 docs/billing-audit.md。

## 限流实时用量修复(2026-10-08)

系统默认/渠道/渠道模型无数据已修复: 用量现已全量落六维度。 见 docs/ops-observability.md。

## 后续候选(未排期)

| 事项 | 备注 |
|---|---|
| ~~计费明细页与余额流水打通~~ | ✅ 已完成(2026-10-08): 明细筛选/分页/CSV 导出 + 余额流水(预扣/结算/释放/调账), 见 docs/billing-detail.md |
| ~~/metrics 鉴权选项~~ | ✅ 已完成(2026-10-08): `metrics_auth=off|bearer` + 令牌自动生成/轮换, /healthz 恒不鉴权, 见 docs/probes-and-alert-rules.md |
| ~~告警规则可配置~~ | ✅ 已完成(2026-10-08): 去重窗口/抖动抑制/延迟阈值进设置并经 /ops/alerts/rules 回显, 见 docs/probes-and-alert-rules.md |
| ~~渠道模型改价~~ | ✅ 已完成(2026-10-08): 用户价预览/搜索筛选/零价上架拦截 + 改价审计, 见 docs/channel-pricing.md |
| ~~审计日志 CSV 导出~~ | ✅ 已完成(2026-10-09): `/api/v1/audit/export` 与列表共享同一套筛选(上限 5000), 超限写截断说明, 导出本身也留审计 `audit.export`, 审计页加导出按钮(与当前筛选同源) |
| ~~低余额告警~~ | ✅ 已完成(2026-10-09): `low_balance` 告警 kind, 两个触发点 — 结算后按 `min_balance + estimate` 预判提前预警、预扣被拒时兜底补报; 两处 reason 相同由去重合并, 运维页加橙色徽标 |

## 生图/生视频支持(2026-10-09)

用户确认: 生图预扣张数默认 4 且参数化(后台可改), 历史计费快照保持兼容(Q2 保留), edits 支持多图。

| 优先级 | 事项 | 状态 |
|---|---|---|
| P1 | 生图同步转发: /v1/images/generations + /v1/images/edits(多图), 协议位 1<<4, 授权矩阵/出站/多图 multipart 透传 | ✅ 已完成 |
| P1 | 按张计费: ChannelModel.MediaSupply(每张/每秒/分档) + BillingRecord media_units, 旧账单行兼容; 改价工作台按张编辑 | ✅ 已完成 |
| P1 | 预扣参数 balance_reserve_images(默认 4, 后台可改, 1-64 校验) | ✅ 已完成 |
| P2 | 生视频: /v1/videos 异步任务, 按次计价, 协议位 1<<5 | ✅ 已完成(v0.20.0): 见 docs/video-generation.md |
| P2 | 生图流式(partial_images 事件流)与上游用量对账 | ✅ 已完成: stream:true 透传 image_generation.* SSE 事件, 流末按 completed 事件数张计费(partial 不计), 见 docs/image-generation.md |
| P3 | 模型能力探测: 用 /models 元数据或试算替代名称关键词归类 | ✅ 已完成(v0.19.0): `op.CapabilityGet` 改走能力登记(models.dev 官方收录 + 人工补录), 名称关键词退为兜底, 见 `internal/op/channel_probe.go` |

见 docs/image-generation.md 与 docs/video-generation.md。

## 渠道模型自动添加(2026-10-09)

第一版自动添加规则(A2 + B1/B2/B3 + C2/C4): 渠道保存后按开关自动探测上游 /models 并入渠道。

| 规则 | 实现 |
|---|---|
| A2 保存后自动同步 | 渠道配置新增 `model_auto_add`(默认关); ChannelCreate/Update 成功后由 handler 触发 `op.AutoAddChannelModels`, 复用与手动刷新同一份 `op.ProbeChannelModels`(相同配置、相同过滤) |
| B1 trim + 去重 | 名称两侧去空白, 按名称去重合并协议位(大小写敏感) |
| B2 只增不删 | 上游未返回的本地模型/授权原样保留, 自动添加永不删除 |
| B3 授权只并入不覆盖 | 探测协议位按位 OR 进该凭据授权; 管理员手设位不被清掉, 其他凭据授权不动 |
| C2 单次上限 | 每次保存最多并入 200 个新模型, 超出截断并记入审计 detail |
| C4 审计 | 新增模型落 `channel.model-added` 审计(target=channel#id, detail=added 数+名称; 截断时注明) |

依赖: 探测客户端按渠道代理配置构建(直连/应用代理/渠道专用), 与手动刷新一致; 失败为 best-effort 只记日志, 不影响已成功的保存。生图协议位仍按名称关键词默认勾选(dall-e/flux 等), kind 随授权重算。

B4(自动定价)与自动添加条件已于 **v0.41.0** 落地, B5(自动上架)仍不做:

| 规则 | 实现 |
|---|---|
| B4 条件过滤 | `op.AutoAddFilter`: 排除正则 + 参考价区间(每百万 token) + 倍率区间(模型参考价 ÷ 渠道现有价位中位数)。全部留空/置 0 表示不限制, 与升级前行为一致 |
| B4 预填供货价 | 开关 `channel_auto_add_fill_price`(默认关); 开启后按 models.dev 参考价填四价, **只填四价全 0 的模型**, 不覆盖人工定价 |
| B5 自动上架 | ❌ 仍不做: 上架等于对客可见, 必须人工确认 |
| A3 定时同步 | ❌ 不建议: 上游模型列表变动不频繁, 保存时同步已足够 |

被条件挡下的模型单独记 `channel.model-skipped` 审计(前 20 个 + 计数), 让管理员能看出条件是否过紧。

## 渠道观察期与质量展示(2026-10-21, v0.41.0)

设计见 `docs/design-channel-review-observation-quality.md`, P2/P3 均已上线:

| 能力 | 实现 |
|---|---|
| P2 观察期 | 审核通过即进入观察期(`channel_trust_eval` 日任务评估): 达标 → trusted; 首次不达标 → probation(延长一轮); 再不合格 → revoked(收回发布)。门槛由 `channel_observation_days`(7) / `channel_observation_min_samples`(100) / `channel_observation_success_floor`(0.98) 控制 |
| P2 评分折减 | `relay/observationPenalty`: observing ×0.85 / probation ×0.70 / revoked ×0.50, 只作用于 score 选路与评分展示; failover/price/latency 等硬排序模式不受影响 |
| P2 前端 | 卡片头部信任徽章 + 详情页观察期双进度条(样本 + 成功率), 与审核徽章并列展示 |
| P3 质量快照 | `channel_quality_snapshots` 复合主键(channel_id+date); `channel_quality_snapshot` 日任务固化昨日数据, 保留 180 天 |
| P3 评级 | `model.QualityGradeOf`: 0.40×成功率 + 0.35×延迟 + 0.25×可用率, A≥90 且成功率≥99.5%, B≥75, C≥60; 样本 <30 不评级(显示 --)。探测未开启时权重归一, 不会因没开探测就得 D |
| P3 API | `GET /api/v1/channel/quality/:id`(概览+趋势) 与 `/snapshot/:id`(纯趋势), 归属者与管理员可读 |
| P3 对客脱敏 | 分享页只出等级 + 区间(99.9%+ / <2s / 99.5%+), 永不出精确值; 快照超过 7 天不展示(过期承诺) |

## 告警外发接线(2026-10-12)

`internal/op/alert.go` 的 `sendMultiChannelAlert` 长期是空占位函数: `RaiseAlert` 把事件/标题/正文算好后
交给一个什么都不做的函数, 而 `billing.go` / `quota.go` 里两处 `alert.Send` 也一直停在注释状态。

后果是**静默失效**: 站内告警照常落库(复核时表里 243 条全部 `notified=0`), 但设置里配好的 Telegram / Email
一条都收不到, 而设置页的连通性测试却是通的 —— 故障只在「该收到告警时收不到」时才暴露。

根因是导入环: `internal/alert` 读告警设置要调 `op.SettingGetString`, 所以 `op` 不能反向 import `alert`,
作者只能把调用点留成 TODO。**这是依赖方向问题, 不是漏写。**

| 优先级 | 事项 | 状态 |
|---|---|---|
| P0 | 双向解耦: `alert.SettingsProvider` 与 `op.AlertDispatcher` 两个注入点, 由 `cmd/start.go` 接线 | ✅ 已完成(v0.19.5) |
| P0 | 单一外发路径: 去掉 `RaiseAlert` 内重复的 webhook 发送, 统一走 `alert.Send`(按 `alert_channels` 分发) | ✅ 已完成(v0.19.5): 否则 `alert_channels` 含 webhook 时同一条告警会外发两次 |
| P1 | 外发脱掉请求取消信号(`context.WithoutCancel`)并按结果回填 `notified` | ✅ 已完成(v0.19.5) |
| P2 | 未接线时打印一次可见日志, 把静默失效变成可发现 | ✅ 已完成(v0.19.5) |

## 仍未实现(2026-10-12 复核)

| 优先级 | 事项 | 状态 |
|---|---|---|
| P2 | 生视频: `/v1/videos` 异步任务, 按次计价 | ✅ 已完成(v0.20.0): 创建/查询/取消三条路由 + 任务归属追踪 + 按条与按秒计费, 见 `docs/video-generation.md` |
| P2 | 生图流式(`partial_images` 事件流)与上游用量对账 | ✅ 已完成: `stream:true` 透传 `image_generation.*` SSE 事件, 流末按 completed 事件数张计费(partial 不计), 见 `docs/image-generation.md` |
| P3 | 充值功能 | ✅ 已完成: 钱包底座+兑换码(P1) → 易支付+充值订单(P2) → 提现全链路(P3) 全部上线, 见 `docs/design-recharge-redemption-withdrawal.md`(该文设计已全部落地, 仅 P4 自动打款预留)。支付渠道已扩到四条(epay/alipay/stripe/creem), 商户参数后台可配 |

## 媒体定价(2026-10-12 定稿)

生图与生视频都不走 token 计价, 三个维度各自独立按形态取用:

| 字段 | 含义 | 适用形态 |
|---|---|---|
| `per_image` | 每张单价 | 生图 |
| `per_video` | 每条固定起步价, 与时长无关 | 生视频 |
| `per_second` | 每秒时长单价 | 生视频 |
| `resolution` | 分辨率分档价, 命中时覆盖 `per_image` / `per_second` 的单价 | 两者 |

结算公式: 生图 `张数 × 单价`, 生视频 `条数 × per_video + 秒数 × 每秒单价`。
分档只覆盖「每张/每秒多少钱」, 不参与与时长无关的起步价。详见 `docs/video-generation.md`。

## 文档过期修正(2026-10-12)

复核时发现下列描述与代码不符, 已就地更正:

- 「模型能力探测未开始」: v0.19.0 已实现, 见上表。
- 「界面内自更新停用(`/api/v1/update` 回 `update paused`)」: 实测 `GET /api/v1/update` 返回 `paused=false`,
  自更新对管理员可用; 该描述是 v0.15.1 当时的临时状态, 之后已恢复。


