# 未实施功能清单(持久化待办)

> 用途: 记录**有设计稿但未实施**、**已交付但未启用**、**已知遗留债**三类事项, 防止跨会话丢失。
> 建立于 2026-10-21, 依据是对 `docs/` 全部设计稿与代码事实的逐项核对(不以文档自述为准)。
> 与 `docs/roadmap.md` 分工: roadmap 记已完成, 本文记**还没做的**。

## 0.3 2026-11-21 复核（P0-P1-ROADMAP 逐条核对后的真实未做项）

本节来源: 对 `docs/P0-P1-ROADMAP.md` 做了一次**逐条代码核对**(该文已同步改为如实的三态标注: 已完成 / 部分交付 / 未做),
这里只收其中**「部分交付」条目真正缺的那一半**, 共 12 条。roadmap 记「已有什么」, 本文记「还缺什么」, 两边不重复。

方法同 §0.2: **只认 grep 结果, 不认任何文档的自述, 包括本文自己**。每条「缺什么」都必须给出「找不到」的证据 —— 零命中计数、唯一命中处、或原文里主动声明不做的注释。
档位是我给的排期建议: **P2** = 有真实用户价值且后端多半已就绪; **P3** = 增补型/需要独立工程。

> **2026-11-22 更新: B4 / B10 / B2 / B5 已全部交付(v1.10.22, 纯前端); B8 / B9 已交付(v1.12.0); B3 / B11 已交付(v1.14.0)**。
> 下表对应行的「缺什么」列已就地标注 ✅ 并保留修前证据(它记录的是「当时确实缺」, 删掉会让下一个人以为
> 这些条目从来就没人提过)。
> 剩余未做: B1 / B6 / B7 / B12 —— 其中 **B1 自动充值有硬前置**(免密扣款授权模型不存在, 不得直接动工)。

| 编号 | 事项 | 现状(已有什么) | 缺什么 | 归属模块 | 依据(grep / 代码落点) | 档位 |
|---|---|---|---|---|---|---|
| B1 | **自动充值**（余额低时自动下单充值） | 余额预警配置与通知已齐（`model/balance_alert.go`、`op/balance_alert.go:84` 触发 + `:119` 发通知）、充值下单链路已齐（`handlers/recharge.go:21` 的 `/api/v1/recharge`） | 没有任何「余额跌破阈值 → 自动建充值单」的联动；也没有免密扣款/委托代扣的授权模型（自动花钱必须有显式授权与上限，这部分设计不存在） | op/balance_alert.go + handlers/recharge.go | `grep -rn "auto_recharge\|AutoRecharge\|autoRecharge\|自动充值\|自动扣款" --include=*.go --include=*.ts --include=*.tsx internal web/src` → **0 命中** | P2（真需求但涉及扣款授权，风险最高的那条） |
| B2 | 余额不足顶部红色 Banner | 站内通知 + 邮件/告警渠道已有（`op/notification_helper.go:68` `NotifyBalanceLow`）；钱包页有预警设置卡 `wallet/BalanceAlertSettings.tsx` | ✅ **已交付 (v1.10.22)** —— `web/src/components/balance-low-banner.tsx` + `balance-low.ts`(判定/格式化纯逻辑), 挂在 `app-shell.tsx` 的横幅槽; 只在用户自设阈值且可用余额(账面−冻结)≤ 阈值时出现, 无阈值一律不提示(不替用户发明门槛) | web/src/components/modules/wallet/ + app-shell | `grep -rn "Banner" --include=*.tsx web/src/components/modules/wallet/` → **0 命中**；`app-shell.tsx` 全文无余额相关渲染 | P3（纯前端小改，成本最低的一条） |
| B3 | 最近 10 次预警历史 | 每次触发原先只写 `last_triggered_at`（`model/balance_alert.go:25`），响应视图仅回上次触发时间 | ✅ **已交付 (v1.14.0)** —— 新表 `balance_alert_histories` (AutoMigrate, 无迁移号), 仅存 user_id/triggered_at/source; 去重时间与历史行同事务; GET API 按当前用户返回最新 10 条; 钱包历史 UI 有 loading/error+retry/empty/data 四态 |
| B4 | 统计页自定义日期范围 | 三档预设（今天/本周/本月，`analytics/dates.ts:5,23`）+ 粒度四档（hour/day/week/month，`analytics/index.tsx:87,146`，后端 `model/user_stats.go:7` 校验）已齐；后端 `start_date/end_date` 本来就接受任意区间 | ✅ **已交付 (v1.10.22)** —— `analytics/dates.ts` 的 `PRESETS` 重新纳入 `custom` + `isValidDate`/`isValidDateRange`/`rangeSpanDays`/`effectiveGranularity`; `analytics/index.tsx` 两个原生 date 输入(选中 custom 才出现), 非法区间不发查询只提示, 起止写反不自动交换 | web/src/components/modules/analytics/ | `analytics/dates.ts:3` 原文自述「custom 因为页面上没有日期输入控件, 选了只会退化成『不限时间』的假范围」；`grep -rn "type=\"date\"\|DatePicker" --include=*.tsx web/src/components/modules/analytics/` → **0 命中**。**这一条是本表里唯一「后端已就绪、纯缺前端」的** | P2（后端零改动，性价比最高） |
| B5 | 独立成功率柱状图（按日/周/月） | 成功率已在趋势图里以**折线**（右轴虚线）呈现（`analytics/index.tsx:276-286` 的 `Line dataKey="success_rate"`），并且有无样本时断线的正确语义（`success.ts` 的 `buildTrendChartData`） | ✅ **已交付 (v1.10.22)** —— `analytics/index.tsx` 新增 `data-testid="success-bar-chart"` 的独立柱状图, 与趋势折线同一份 `buildTrendChartData` 数据; 无日志样本的桶是 `null`(不画柱, 绝不画 0%), Y 轴锁死 0-100 | web/src/components/modules/analytics/ | `grep -n "BarChart\|Bar " web/src/components/modules/analytics/index.tsx` → 只有 `:378` 的模型 BarChart；`dataKey="success_rate"` 仅出现在 `Line`（`:276-286`） | P3（信息已能读到，纯视觉增补） |
| B6 | 工单附件上传 | 工单文本全链路已交付（`handlers/ticket.go`、`modules/tickets/`） | 附件功能**只有数据库字段、没有任何读写方**：无上传接口、无存储后端、前端明确不做入口 —— 字段本身在注释里就写着「JSON 数组」，但没有代码写过它 | model/ticket.go + handlers/ticket.go + tickets 前端 | `grep -rn "Attachments" --include=*.go internal` → **仅 1 处命中**（`model/ticket.go:59` 的定义行，无任何 `.Attachments` 读写）；前端 `api/ticket.ts:56` 原注释：「本期只支持文本消息: attachments 有底座之前不读不传, 界面也不做附件入口」 | P3（要配套存储/鉴权/病毒扫描，是独立工程） |
| B7 | 工单操作历史视图 | 状态流转本身有完整状态机与 409 冲突校验（`docs/TICKETS.md`），每次操作也落审计 | 界面上没有「谁在何时把状态从 A 改到 B」的专门视图；用户只能看当前状态与对话 | tickets 前端 + handlers/ticket.go | `grep -rn "TicketHistory\|ticket_history\|操作历史\|OperationHistory" --include=*.go --include=*.tsx internal web/src` → **0 命中** | P3（审计里有数据、缺一个视图） |
| B8 | 机器人专有报文模板（钉钉 Markdown / 飞书卡片 / Slack） | 通用签名 JSON WebHook 已全套交付（`handlers/webhook.go:27`、`op/webhook_dispatch.go`、投递重放、投递日志）；**运维告警**侧另有 dingtalk/feishu/wecom 推送（`internal/alert/alert.go:101-105,144-148`） | ✅ **已交付 (v1.12.0)** —— `webhooks.template` 列(AutoMigrate 建列, 不占迁移号) + 四种 body 各自 `json.Marshal` 构造(generic 字节不变, 由写死字符串断言锁住); 平台自身加签(钉钉 sign / 飞书 signature)**明确不做**并写在配置下拉旁; 改模板不改写已入队投递, 重放按当前模板重建。见 `docs/WEBHOOKS.md`「报文模板」 | internal/op/webhook_dispatch.go + model/webhook.go | `grep -rn "dingtalk\|feishu\|lark\|slack" --include=*.go internal/op/webhook*.go internal/model/webhook.go` → **0 命中**；`grep -rn "template" 同上` → **0 命中**；载荷结构 `model/webhook.go:225-239` 只有通用 `id/event/timestamp/user_id/data` | P2（集成类需求高频，但要做格式分支与测试） |
| B9 | 2FA 信任设备（记住本机 N 天）+ 恢复码自助重生成 | TOTP 全链路已交付：setup/enable/disable（`handlers/user_totp.go:33`）、step-up 闸门（`middleware/stepup.go:15`）、10 个恢复码（`op/user_totp.go:146`）、重放防护（`TOTPLastStep`） | ✅ **已交付 (v1.12.0)** —— 新建 `trusted_devices` 表(AutoMigrate) + 登录免码旁路(令牌只存 sha256、30 天固定不滑动、HttpOnly、改密/重绑 2FA 时全量清除、设置页可逐个/全部吊销) + `POST /2fa/recovery-codes/regenerate`(step-up + 一个有效码, 整体替换旧码, 不吊销会话)。见 `docs/two-factor-trusted-devices.md` | internal/op/user_totp.go + internal/server/middleware/stepup.go | `grep -rn "trusted_device\|TrustedDevice\|remember_me\|rememberDevice" --include=*.go --include=*.tsx internal web/src` → **0 命中**；`grep -rn "RecoveryRegenerate\|regenerate.*recovery" --include=*.go internal` → **0 命中**；`user_totp.go` 的恢复码生成只在 `UserTOTPEnable`(:146) 内 | P3（体验优化；恢复码重生成是安全运维的实际痛点，可提到 P2） |
| B10 | API Key 逐日明细趋势图 | 报表接口已把逐日明细整份下发：`model/apikey_usage.go:49` 的 `Days []APIKeyUsageDay`（升序、无数据时非 nil 空切片），前端类型也有（`api/apikey.ts:127`） | ✅ **已交付 (v1.10.22)** —— `setting/apiKeyUsageChart.ts`(纯逻辑: 标签校验/加总/判据/30 天窗口) + `APIKey.tsx` 内私有 `APIKeyUsageDaysChart`; recharts BarChart 只画 `request_count`, 窗口收到 `recent_from`, 全零走空态不画零线 | web/src/components/modules/setting/APIKey.tsx | `grep -rn "recharts\|LineChart\|BarChart" web/src/components/modules/setting/APIKey.tsx` → **0 命中**；`grep -rn "days" 同文件` → 仅 `:502` 的 `retention_days` 文案，无 `usage.days` 遍历 | P3（数据零成本，纯前端） |
| B11 | 等级 → 模型白名单（tier 决定可访问模型） | tier 列、EffectiveTier 与兜底限流已交付 | ✅ **已交付 (v1.14.0)** —— `user_tier_policies` 增加 `allowed_models`, 客户端 Group 名精确匹配; 缺失/null/空列表保持无限制; 与 API Key `supported_models` 取交集; Relay 在限流/余额预留前拒绝 |
| B12 | 审计归档推对象存储（S3/OSS）+ JSON 导出格式 | CSV 导出（`handlers/audit.go:102`，上限 5000，超限自述截断）；NDJSON 本地归档 + 先归档后删 + 失败零删除（`op/audit_archive.go`）；备份侧**已有**远端推送通道可复用（`op/backupremote.go:18,32` `BackupRemoteConfigured`/`PushBackupRemote`） | ① 审计归档只落本地 `data/archives/audit/`，不推 S3/OSS（运维要异地留存只能手动拷）；② 导出只有 CSV，`format` 参数根本没实现（不存在 JSON 导出） | internal/op/audit_archive.go + handlers/audit.go | `grep -rn "s3\|oss" --include=*.go internal/op/audit_archive.go` → **0 命中**；`grep -rn "format" --include=*.go internal/server/handlers/audit.go` → **0 命中**（`auditFilterFromQuery` 只读 keyword/action/operator/from/to/limit）；目录见 `op/audit_archive.go:43-44` | P2（合规相关；S3 可复用既有远端通道，JSON 导出是小活） |

**两处值得先做**(都在「后端能力已备、纯缺前端或纯缺串联」)—— **已于 2026-11-22 一并交付(v1.10.22)**:
- ✅ **B4 统计自定义日期范围**: 后端 `model/user_stats.go:5-6` 的 `start_date/end_date` 本来就接受任意区间, 前端只差日期控件与请求拼装;
- ✅ **B10 API Key 逐日趋势图**: `days` 字段已经在响应体里下发(`model/apikey_usage.go:49`), 前端一个字段都没读。

同一版还顺带做掉了两条纯前端项: ✅ **B2 余额不足顶部 Banner**、✅ **B5 独立成功率柱状图**。
四条合计后端零改动、零迁移 —— 这是本表里唯一一批「不需要定授权模型、不需要新表」的活。

**B3 / B11 已交付(v1.14.0)**: ✅ **B3 最近 10 次余额预警历史**（只记实际去重获胜的提醒；时间戳/来源，不记余额金额；历史表与去重时间同事务; 数据库与 API 均只保留/返回最新 10 条）+ ✅ **B11 tier 模型白名单**（按客户端模型组名精确匹配；未配置/空列表保持不限制；与 API Key allowlist 取交集；请求计费前拒绝）。

**此前批次已交付(v1.12.0)**: ✅ **B9 2FA 信任设备 + 恢复码自助重生成**、✅ **B8 WebHook 机器人报文模板**。
这两条的共同点是「原功能都能用, 但用到最后会把人卡死」—— 每台设备每次登录都要掏手机、
最后一张恢复码用掉后账号再也回不来; WebHook 只能发通用 JSON, 钉钉/飞书端点直接拒收。
两条都不需要新迁移号(AutoMigrate 建表/建列), 也不碰资金与路由。

**一处不能低估**: **B1 自动充值**。它不是"少写个定时任务" —— 自动花钱必须有显式授权(授权范围/单次上限/失败回滚), 而这套授权模型目前不存在。在授权模型定下来之前不应动工。

**B8 的易混点**: WebHook 订阅侧的机器人模板是空的, 但 `internal/alert` 里的 dingtalk/feishu/wecom 是**运维告警渠道**(给管理员发告警用), 与用户订阅的 WebHook 不是同一套。搜到后者不要当成前者已做。

---

## 0.2 2026-11-20 复核（SaaS 版暂停，施工回基线）

本轮按代码事实重新核对, **更正三处过期记录**（都曾标为"未做"，实际早已实现）:

| 原记录 | 实际 | 证据 |
|---|---|---|
| §C3 告警静默窗口「全库无 silence/maintenance/mute 实现」 | **已完整实现** | `model/alert_silence.go`、`op/alert_silence.go`、3 条路由（`/ops/silence/list\|create\|:id`）、前端 `ops/index.tsx` 面板、三语 locale |
| `P0-P1-ROADMAP.md` 表「多因素认证 🔲待开发」 | **已实现** | 全库 219 处 TOTP 引用；`totp-security.md`；登录/状态响应带 `totp_enabled` |
| 同上「用户分级管理 / 审计日志归档 🔲待开发」 | **都已实现** | `SettingKeyUserTierPolicies` + 四档默认配额 + `lib/user-tier.ts`；`TaskAuditArchive` 独立留存期 |

其余条目经核**状态未变**: 多实例三障碍仍在（`stats.go` 覆盖写 / `ratelimit` 内存桶 / `task` 无租约）,
`balance_reservations` 与 `balance_ledgers` 仍无清理任务, 出口池与区域亲和仍依赖 CF 橙云,
B5 自动上架仍不做。

**同日新增一处更正（B3 部署磁盘）**: 此前记着"`swap_deploy.sh` 的 `pre-*` 整库副本无轮转"，
核对后发现**名字和归属都记错了**——`swap_deploy.sh` 只操作 `bin/`，从不产生 `pre-*` 目录；
真正的整库副本在 `data/releases/<时间戳>-{security-batch2, security-batch2-deploy, financial}`，
由 `security-batch2-backup.py` / `deploy-security-batch2.py` / `production-financial-release.py` 写入。
问题本身是真的（三者都无轮转），只是位置记错了。已由 `scripts/releases-retention.py` 修复：
只轮转 `*-security-batch2-deploy` 的完成态（保留 5 份），`*-financial` 与 `*-security-batch2`
**永不轮转**（前者被 `financial-release-handoff.md` 明文要求豁免，后者是回退到迁移前 schema 的唯一途径）。

> **教训**: 本文历史上有过多处"文档说没做、代码早已有"的偏差（§0 表已记过一次）。
> 核对**只认代码 grep 的结果**, 不认任何文档的自述, 包括本文自己。
> 本次还暴露出另一类偏差: **"问题是真的, 但位置/归属记错了"** —— 直接照文档去改会改错脚本。

---

## 0.1 2026-11-08 复核

本日修完统计口径(迁移 041→044)后重新核对本文: §1.1 的多实例三障碍**一条都没变**(本次改的是统计
**口径**与**主键**, 不是并发模型); §1.2 的 B1/B5/C1/C2 全部照旧; §2 的零配置项照旧。
另外补记一条本轮新发现的时序约束(已写进 §1.1 的"配套前置"): 统计口径迁移是一次性改写历史数据,
多实例滚动升级期间会先跑的实例改写、后跑的实例仍按旧口径写入 —— 这类迁移在多活下必须先停写再迁移。

## 0. 2026-10-21 状态核对结论

核对方式: 对每篇设计稿的关键字段 / 路由 / 任务 / 设置键在代码里 grep 求证。

### 已就地更正的过期描述

| 文档 | 原文 | 实际 |
|---|---|---|
| `roadmap.md` P3 充值 | 「未开始, 后端无任何实现」 | 早已全线实现: `handlers/recharge.go`(8 路由) / `wallet.go` / `redemption.go`(6 路由) / `withdrawal.go`(7 路由); `internal/pay` 四渠道 epay/alipay/stripe/creem; 前端 `wallet/` `redemption/` `withdrawals/` 三模块齐全 |
| `design-recharge-redemption-withdrawal.md` 头部 | 「待评审, 尚未实现」 | P1 钱包底座+兑换码 / P2 支付+充值 / P3 提现 全部已上线 |
| `design-channel-review-observation-quality.md` 头部 | 「设计稿, 未实施」 | P1 审核已完成; P2/P3 于 2026-10-21 开发 |
| 本文 §4 交付索引 | 引用两份不存在的文档 | **2026-10-22 已修正**: 两个文件名从未存在, 改为指向实际记载位置 |

### 2026-10-22 复核: 各项状态未变

对本文全部条目重新核对了一次(代码 grep + 生产库查询), 结论与 2026-10-21 一致, 无一项因新增版本而改变:
出口池仍 0 行、区域亲和仍为 0、多实例三障碍仍在、B5 仍未做、支付渠道商户参数仍全未配置。

前端遗留过期文案: `users.form.balanceHint` 的「充值功能后续上线」。**2026-10-22 已删除**(三语同步)。

---

## 1. 有设计稿, 未实施

### 1.1 多区域高可用 / 应用多活 — 整篇未落地
设计稿: `design-multi-region-ha.md`、`design-app-multi-active.md`

三个**阻碍多实例**的核心改造一个都没做。想动多活之前, 必须先读完下面这份"为什么不能直接上"的说明:
这三处不是"加个锁就行", 每一处都绑着一条当前架构赖以正确的假设。

#### 障碍一: 统计内存累计 + 全量快照覆盖写(`internal/op/stats.go`)

现状: `statsTotalCache` / `statsDailyCache` / `statsHourlyCache` / `channelCache.StatsMetrics` /
`channelModelCache` / `channelKeyCache` / `statsAPIKeyCache` 全部是**进程内累加器**,
由 `StatsSaveDBTask` 定时把"缓存当前值"整份盖回数据库(`persistStatsSnapshots`, 用 `Save` / `Select(...).Updates`)。
正确性依赖一条单写者假设: **内存里的值就是该统计维度的权威累计, 只有一个进程在改它**。

为什么这条假设不能靠"加锁/加事务"绕过: 覆盖写没有增量语义。两个实例各自持有半份累加值,
谁后落库就把对方的增量整段抹掉 —— 不是重复计数, 是**丢计数**, 而且丢多少无法从库里还原。
改成"增量 UPDATE ... SET x = x + ?"又会与现有的重启恢复路径冲突: 启动时 `statsRefreshCache`
把当天行装回缓存作为基线, 之后若再叠增量, 重放一次就重复累加一次。

要拆必须先选定一种模型, 二选一, 不能混:

1. **选主 + 单写者**(改动最小): 库级租约只让一个实例跑 `StatsSaveDBTask` 与全部累加刷新,
   其余实例的统计请求走只读库查询。代价: 统计热路径多一次库往返, 且选主失效时统计会短暂停更(不丢)。
2. **真正的增量写**(改动最大): 落库改成 `INSERT ... ON CONFLICT DO UPDATE SET input_token = input_token + ?`,
   同时删掉"缓存即权威"的假设, 让缓存退化为可丢的读加速层; 重启不再需要 `statsRefreshCache` 回填基线。
   还要给每个增量打上幂等键(否则重试会重复累加), 等于把每笔请求的统计写入变成一次性事件。

未做的原因: 两条路径都要动 `stats.go` 的核心不变量与重启恢复语义, 且必须同时改备份导出/导入的口径,
没有"小步安全上线"的中间态。当前单实例下这套是正确且快的, 不值得为一个还没排期的目标先拆掉。

#### 障碍二: 限流内存桶(`internal/ratelimit`)

现状: 计数桶在进程内 map 里, 各实例互不可见, 因此 N 个实例 = 实际额度 × N。
这不是"漏一点"而是一致性问题: 同一个 API Key 打两个实例, 各自都判定"还没超", 双双放行。

要拆必须把桶搬到一个共享且原子的存储: Redis(现无部署)或数据库。数据库方案需要在请求热路径上
做一次带条件的 `UPDATE ... WHERE used + ? <= limit`, 并且要有过期清理任务 —— 这本身就是一套新子系统。
未做的原因: 线上单实例下这条完全正确; 引入共享限流存储属于新的运维依赖, 需要单独评审与容量评估。

#### 障碍三: 后台任务进程内 ticker(`internal/task`)

现状: 定时任务(自动备份、归档清理、告警重投、统计落库、结算重试等)由进程内 ticker 触发,
没有库级租约, 多实例会**每个实例各跑一遍**。对幂等任务只是浪费; 对非幂等任务(自动备份、
告警推送、结算重试)就是重复动作。

要拆的最小方案是库级租约选主: 任务表加 `lease_owner` / `lease_expires_at`, 抢到租约的实例才执行,
租约到期由别的实例接管。`balance_reservations.lease_expires_at` 已是同类模式的现成先例,
可以照着做。未做的原因: 需要逐个任务确认幂等性并给出租约超时(超时短了会双跑, 长了故障后停更),
且这套机制只有在真的要多实例时才有收益 —— 现在做等于给单实例加一层无收益的复杂度。

#### 配套前置

多活整体还缺: PG + Patroni + HAProxy 主备集群未搭建(`internal/db` 支持 postgres 方言, 线上仍跑 SQLite);
以及统计口径迁移(041/042/043/044)这类"一次性改历史数据"的迁移, 在多实例滚动升级期间会先跑的实例
改写数据、后跑的实例仍按旧口径写入 —— 发版流程必须先停写再迁移。

配套 PG + Patroni + HAProxy 主备集群未搭建(`internal/db` 支持 postgres 方言, 线上仍跑 SQLite)。

### 1.2 全球加速 backlog
设计稿: `backlog-global-acceleration.md`

| 编号 | 事项 | 卡在哪 |
|---|---|---|
| B1 | Cloudflare 边缘证书未签发 → G2 边缘无法启用 | **纯外部依赖**: 需 CF 后台操作 |
| B5 | G2 验收指标未测 | 依赖 B1 |
| C1 | G3 出口池线上零配置(`channel_egresses` 0 行) | 缺真实第二线路 |
| C2 | 区域亲和默认关闭(`egress_region_bonus_ms=0`) | 依赖 B1(需 `CF-IPCountry`) |
| C5 | 出口健康看板(出口成功率/冷却未暴露接口) | 已完成 |
| D1 | G0b MaxMind mmdb + `client_asn` | 主动延后 |
| E | G4 多区域数据面 | 未开始, 前置是 §1.1 |

### 1.3 充值/提现明确不做的子项
设计稿本身写着不做, 不是遗漏:

| 事项 | 说明 |
|---|---|
| 充值订单退款流程 | `RechargeStatusRefunded` 常量已定义, 全库无任何退款动作实现 |
| 发票 | 无任何实现 |
| 提现自动打款(P4) | 注释「后续自动打款按同一字段对接」, v1 仍是人工打款 + 凭证 |
| Stripe/支付宝完整形态 | `internal/pay` 有实现, 线上未配置商户参数 |

### 1.4 渠道模型自动上架(B5)
`roadmap.md`: 「B4/B5 自动定价/自动上架(P2) 未纳入本版」。
2026-10-21 已补 **B4 自动预填供货价 + 条件过滤**(见 `roadmap.md` 的「渠道模型自动添加」一节),
2026-10-22 (v0.42.0) 已补 **A3 后台周期自动添加 + 名称白名单**, 2026-10-22 (v0.43.3) 已补**按模型单独设置条件**。
但 **B5 自动上架(listed=true) 仍不做**: 上架等于对客可见, 必须人工确认价格, 自动上架会把零价模型暴露给用户。

### 1.5 模型广场
设计稿: `design-model-marketplace.md`(P1/P2/P3 三阶段)

| 阶段 | 内容 | 状态 |
|---|---|---|
| P1 只读挂牌表 | 口径单点(`internal/op/grant_view.go`)、`MarketCatalog`、品牌解析、`GET /api/v1/market/models`、前端 `market` 页 | **v1.5.41 已发布**(细化见设计稿 §10) |
| P2 加入自有模型配置 | 展开行内勾选自有分组与渠道; 新增 `POST /api/v1/group/add-items`(只追加语义) | **v1.6.0 已发布**。原最小闭环走 `items` 整体替换, 依赖调用方的快照, 并发下会静默盖掉别的会话刚加的成员; 改为后端"读出当前集合再追加", 并发安全且幂等。详见 §4 交付索引 |
| P3 日级回落 + 质量区间 + 对比 + 成本估算 | `metrics_source` 增加 `daily`(近 7 天持久化统计回落); 渠道质量区间; 卡片勾选并排对比; 按用量估算花费 | **v1.6.1 已发布**(品牌覆盖表除外)。原写「重启后清零」是错的: `relay.RestorePersist` 会从 `route_metrics` 恢复, 重启后仍标 `live`; 回落真正补的是「从未经分组转发过」的空白。详见 §4 交付索引 |
| 阶段三/四清点 + 广场收藏 | 核实「选路解释/渠道看板/预算告警」三项已存在; 修好选路解释的文案缺陷; 新增广场收藏 | **v1.6.2 已发布**。按清单逐项实施会变成重写已有代码, 故先核查; 收藏只存模型名, 幂等靠唯一索引, 展示侧核对存在性。详见设计稿 §10.3.2 |

> P1 **v1.5.41**(门槛由 v1.5.42 修正), P2 **v1.6.0**, P3 **v1.6.1**(品牌覆盖表除外), 阶段三/四清点与补缺 **v1.6.2**。详见 §4 交付索引。

---

## 2. 已交付但未启用 / 零配置

| 能力 | 交付版本 | 为什么没启用 |
|---|---|---|
| G2 入口边缘接入 | v0.38.0(源站侧) | CF 边缘证书未签发(B1) |
| G3 出口池 + 按出口打分 | v0.37.0 | 线上 `channel_egresses` 曾 0 行; 2026-11-03 起有 3 条 direct 出口, 真实代理线路仍在配置中。字段约束与可达性要求见 [出口池配置要求](./egress-configuration.md) |
| 区域亲和(`CF-IPCountry` 选路) | v0.37.0 | 依赖 B1 |
| `channel_insights` 延迟归因出口列 | v0.35.0 | 有数据但无出口健康视图(C5) |

---

## 3. 遗留债与已知限制

### 3.1 财务历史(**2026-10-23 已处置**, 见 v0.44.0)
- **20261016 单据差异**: 已重算。成因是单据生成于 12:09:29、当天最后一批计费在 13:32:46, 之后 41 笔 / 0.998436396 落在单据之外。走正式生成流程刷成 194 笔 / 3.278277946, 单号与身份字段不动, 用户余额未触碰。
- **20261008 对账 mismatch**: 已登记处置说明(status=`explained`)。成因是全量计费修复前的 admin 免计费流量(计费明细从 10-08 20:28 才开始)。**数字一个都不改** —— 原始请求日志已过保留窗口, 无逐笔证据可核, 按「没有证据不得把已记录的漏结算改成零」保留原异常。

两笔的完整前后值与审计位置见发布目录 `/opt/switchyard/data/releases/20261023-101236-v0440-financial/financial-history-resolution.md`。
用户余额、收益池、提现一律未动; 未生成任何调整凭证。

**遗留**: 若将来要把 20261008 的 321 笔追认为资金损失或补扣, 仍需要归档证据 + 人工审批 + 调整凭证, 这一条不因登记说明而消失。

### 3.2 验证缺口
- 登录态页面与**真实交易全流程验收**未执行(充值与提现未用真实资金测过)
- MySQL / PostgreSQL **真实服务集成验收**未做(`financial-hardening-phase2a.md:64`)
- **已补(v0.44.1)**: 备份导入非法版本/媒体单价/设置值/渠道配置拒绝测试; 分组按用户隔离与改名/全量刷新清理索引测试; 用户缓存跨数据库刷新清理测试; 路由按分组隔离、手动模式绕过运行时状态、全量重置测试; 出口状态按 ID 隔离与重置测试。测试边界不触碰生产数据。
- **已补(v0.45.0 第一批)**: SSRF 私网防护(保存时校验 + 拨号层复核防 rebinding, `allow_private_upstream` 例外开关); 非流式响应 128 MiB 读取上限(传输层, SSE 旁路); 流式聚合 128 MiB 上限(超限按本轮失败); 成功但上游不回用量 → 释放预扣并挂 `usage_missing` 告警; 默认 admin/admin 未改密前管理面强制跳转改密(登录/状态响应带 `must_change_password`)。登录失败限流此前已在(`login_max_attempts`/`login_lockout_seconds`)。

### 3.3 已知代码限制(设计上接受, 非 bug)
- gin `Flush()` 错误在 writer 层不可观测 → 死客户端会一直抽干上游直到空闲超时(客户端断开的取消信号已随请求 ctx 传播, 剩余窗口是"已提交后的空闲超时段")
- 旧 `VideoJob` 记录缺 `ChannelID` → 授权删除后无法恢复(fail-closed by design)

---

## 4. 各批次交付索引

| 事项 | 版本 | 文档 |
|---|---|---|
| 渠道观察期(P2) + 质量展示(P3) | v0.41.0 | `docs/design-channel-review-observation-quality.md` |
| 渠道模型自动添加条件(价格/倍率/形态/排除) | v0.41.0 | `docs/roadmap.md`「渠道模型自动添加」一节 |
| A3 后台周期自动添加 + 名称白名单 | v0.42.0 | `docs/roadmap.md`「A3 后台周期自动添加」 |
| 按模型单独设置自动添加条件 + 入口补到「模型配置」页 | v0.43.0 / v0.43.3 | `CHANGELOG.md` |
| 发信配置收敛成一套(删除失效的邮件服务卡片) | v0.43.4 | `CHANGELOG.md` |
| 模型广场 P1 只读挂牌表 | v1.5.41 / v1.5.42 | `docs/design-model-marketplace.md` §10 |
| 模型广场 P2 加入自有模型配置(只追加语义) | v1.6.0 | `docs/design-model-marketplace.md` §10 + `CHANGELOG.md` |
| 模型广场 P3 可信质量面板 + 对比 + 成本估算 | v1.6.1 | `docs/design-model-marketplace.md` §10.3 + `CHANGELOG.md` |
| 广场收藏 + 选路解释文案修复 | v1.6.2 | `docs/design-model-marketplace.md` §10.3.2 + `CHANGELOG.md` |

> 2026-10-22 修正: 本表原先引用的 `docs/channel-observation-quality.md` 与
> `docs/channel-auto-add-conditions.md` **从未存在过**(git 全历史无此文件), 是凭印象写下的文档名。
> 实际记载位置见上表。

---

## 5. 建议优先级

> 下表**只覆盖旧项**; 2026-11-21 新增的 12 条(来自 P0-P1-ROADMAP 的部分交付缺口)已在 **§0.3** 单列并各自带档位, 不要与本表混算。

| 优先级 | 事项 | 理由 |
|---|---|---|
| P1 | 财务历史两项核查 | 真实资金风险, 需人工审批与授权窗口 |
| P2 | 出口健康看板 C5 | 小改动, 但线上零出口配置, 收益暂时为零 |
| P3 | 应用多活 P3 | 改动最大, 需先有稳定期与 PG 集群 |
| 外部依赖 | CF 边缘证书 B1 | 只有运维能操作 |
| — | **§0.3 的 12 条新增项** | B4 / B10 / B2 / B5(v1.10.22)、B8 / B9(v1.12.0)、B3 / B11(v1.14.0) 已交付; 剩余 B1 / B6 / B7 / B12, 其中 B1 需先定授权模型(不得直接动工) |
