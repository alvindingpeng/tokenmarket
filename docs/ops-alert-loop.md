# 告警闭环与健康指标告警(A+B)

## 概述
在第二批运营可靠性的基础上补齐两条能力:

- **A 健康指标告警**: 渠道探测不再只看"通/断"。延迟超过阈值判 `degraded`; 连续 5 次失败才判 `down`(抖动抑制); 恢复后自动补发 `recovered`。
- **B 告警闭环**: 对账差异(`mismatch`)、备份失败(`backup_failed`)、归档失败(`archive_failed`)全部走统一告警出口 `op.RaiseAlert`, 自动落库 `channel_alerts` 并按配置外发 Webhook。

## 告警出口与去重
`internal/op/alert.go` 是全站唯一告警出口:

- 去重窗口 10 分钟, 按 (channel_id, kind, reason) 精确去重: 同因同果不刷屏, 不同天的对账差异互不影响。
- Webhook 地址来自设置 `alert_webhook_url`(仅允许 http/https, 校验失败不发起请求); 10 秒短超时, 外发失败不影响站内落库, `notified` 字段记录是否外发成功。
- `reason` 截断到 500 字符, 防止错误堆栈撑爆表格。

## 健康判定规则(internal/op/channelhealth.go)

| 情形 | 判定 | 告警 |
|---|---|---|
| 探测失败(网络错误/HTTP>=500)连续 < 5 次 | 异常但静默 | 无 |
| 连续 5 次失败 | down | `down` |
| 健康且延迟 >= health_latency_ms | 降级 | `degraded` |
| 健康且延迟 < 阈值, 且上一条告警是 down/degraded | 恢复 | `recovered` |

连续失败计数读自 `channel_health_records` 最近 5 条记录, 不依赖进程内存, 重启不丢状态。阈值为 0 表示关闭延迟告警。

## 新增设置

| 键 | 默认 | 说明 |
|---|---|---|
| `health_latency_ms` | 1000 | 延迟告警阈值(毫秒), 0 关闭, 上限 600000 |

其余沿用第二批: `alert_webhook_url`、`health_check_enabled`、`health_check_interval`。

## 新增接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /api/v1/ops/health/run | 立即对所有启用渠道探测一轮(仅管理员) |
| POST | /api/v1/ops/alerts/test-webhook | 一键测试 Webhook 连通性, 发送 mock 告警 |

## 前端

- 运维中心「渠道健康」页签新增「立即探测」按钮。
- 「告警记录」页签新增对账差异/备份失败/归档失败/测试四类徽章。
- 设置「运营可靠性」新增延迟阈值输入与 Webhook 测试按钮。

## 验证(E2E 21/21)
`switchyard-e2e-ab.py` 全链路验证:

1. 慢上游(20ms) + 阈值 1ms → `degraded` 告警 + Webhook 送达(notified=true)
2. 阈值抬高后探测 → `recovered` 补发
3. 死渠道前 4 次失败无告警, 第 5 次 → `down`(防抖生效)
4. test-webhook 端点 200, mock 告警送达
5. 造一条漏结算记录 → 对账 `mismatch` → 告警 + Webhook 送达
6. Webhook 共收 5 条(degraded/recovered/down/test/mismatch)

## 部署

- 构建: `pnpm exec tsc --noEmit && pnpm run build` + `go build` + `go test ./...` 全绿。
- 二进制 SHA256: `ef8452df84bc497d1db5acb5040a42dee8e062402733f5e7d86b330969f70d22`
- 生产: `/opt/switchyard-multiuser/switchyard`, 备份 `backups/alert-loop-20261008-162658`, 服务 active, 启动日志干净(自动备份 verified=true)。
- 新键 `health_latency_ms` 启动时自动补齐默认值。

## 注意事项

- 健康探测默认关闭(可能产生上游费用), 开启后才会有健康/告警记录。
- Webhook 建议配合测试按钮先行验证, 避免真实故障时才发现配置错误。
- `down` 告警去重窗口 10 分钟: 持续宕机每 10 分钟最多一条, 恢复必发一条 `recovered`。


---

# 告警内容改造(2026-10-13): 真实渠道名 · 结构化明细 · 熔断与后台任务

## 这一版解决什么

原告警在 Telegram 里只有两行: 标题带 `channel_code`, 正文一句英文 reason。管理员收到通知
根本不知道是哪家渠道出事了 —— 分享码是给外部用户看的随机串, 不是渠道名; 而 reason 里既没有
延迟数字, 也没有探测地址与错误详情。此外只有渠道故障会告警: 凭据被熔断、某个模型被熔断、
后台任务天天在失败, 全都静默写日志。

## 排版归 alert, 事实归产生方

- `internal/alert/message.go` 只管排版: `kindMetas` 给每种 kind 定级别与中文标题,
  `renderTitle`/`renderMessage` 生成"图标 + 类型: 主体"与逐行"标签: 值"。Telegram 与邮件共用同一批字段。
- `internal/op` 在产生告警的一侧填好事实(`AlertFacts{ChannelID, Subject, Kind, Reason, Fields}`):
  谁产生告警谁最清楚该说什么, alert 包不做任何猜测。
- 未登记的 kind 走 `metaFor` 兜底分支, 标题显示原始 kind 而不是冒充"渠道降级" ——
  冒充正是改造前把"备份失败"报成"渠道降级"的成因。

## 主体用真实渠道名

`resolveSubject` 决定告警主体: 渠道名 `Channel.Name` 优先; 传空、传分享码、或渠道没有名字时
依次回退到 `Name` → `ShareCode` → `渠道 #ID`。站内列表新增「对象」列取 `subject`,
`channel_code` 保留为分享码供老数据回退。系统级告警(备份、任务、更新)主体是任务名或"系统"。

## 新增的告警类型

| kind | 触发点 | 级别 | 明细字段 |
|---|---|---|---|
| `key_disabled` | 渠道凭据被熔断(`RecordChannelKeyError`) | warning | 渠道/凭据/错误类型/熔断时长/触发条件/影响/建议 |
| `model_disabled` | 渠道模型被熔断(`RecordChannelModelError`) | warning | 渠道/模型/错误类型/熔断时长/触发条件/建议 |
| `task_failed` | 后台任务连续 3 次失败(含 panic) | warning | 任务/任务标识/连续失败/最近错误/影响/建议 |
| `task_recovered` | 失败任务重新跑通 | info | 任务/任务标识/此前连续失败 |
| `quota_exceeded` | API Key 日/月请求或成本配额触顶 | warning | API Key/配额类型/当前用量/影响/建议 |
| `low_balance` | 用户余额跌破起扣门槛 | warning | 用户/可用余额/账户余额/冻结额/起扣门槛/影响/建议 |

渠道健康类(down / degraded / recovered)补齐了探测地址、实测延迟、HTTP 状态、探测错误与探测时间。
备份/归档/对账/计费结算四类同样带上了文件名、大小、完整性校验结果、异地状态、明细数字与影响。

`update_available` 在 `kindMetas`、前端徽章和 OpenAPI 枚举里都登记了, 但当前没有任何地方产生它:
发现新版本只在后台「系统信息」里提示。检查周期是小时级, 真去外发就是每小时刷屏, 而它并不是故障。

熔断只发"被熔断"这一条: 熔断到期是自动恢复, 不补发 recovered, 否则管理员要为每家渠道的每个
凭据收两条通知。凭据/模型熔断的 reason 不含时间戳 —— 去重按 (channel_id, kind, reason) 精确匹配,
带易变值会让同一故障在窗口内反复外发。

## 后台任务失败进入告警

`internal/task` 的记录器现在统计连续失败次数: 第 3 次起回调 `OnTaskFailure`, 成功一次回调
`OnTaskRecovered`。`cmd/start.go` 把这两个钩子接到 `op.RaiseTaskFailureAlert` /
`RaiseTaskRecoveredAlert`。阈值取 3 而不是 1: 备份与归档依赖磁盘与网络, 单次抖动就推送会让
管理员对告警脱敏。任务体内的 panic 也被 `runBody` 捕获成一次失败, 不再击穿进程。

## 间隔设置热生效

改造前只有 `model_info_update_interval` 会真正通知调度器, 其余周期(统计落库、日志留存、
自动备份、健康探测、更新检查)改完必须重启才生效, 界面上却都写着可改。现在
`POST /api/v1/setting/set` 保存成功后调用 `task.ApplySetting(key)`, 按设置键重算它影响到的
全部任务周期; 应用失败直接返回错误而不是假装成功。GitHub 令牌改完还会 `update.Invalidate()`
立刻重新检查。

更新检查的开关与周期此前在设置页没有入口(只能进数据库改行), 现在与 GitHub 令牌并排出现在
「系统设置」里: 开关关掉即停止轮询, 间隔取 1-1440 分钟, 前端与后端各自夹住范围。

## 运维页手动动作也进告警

「立即归档」「立即备份」失败原先只在页面上弹个错, 没人会盯着看。现在同样走
`RaiseAlertFacts`, 明细带上触发方式(运维页手动执行)与操作者用户名, 便于事后追责。

## 请求体上限

转发前请求体要被读进内存做改写与留痕, 原先完全没有上限。新增 `middleware.BodyLimit`,
管理端 32 MiB、`/v1` 512 MiB(生图底图与生视频素材是正常业务), 整库导入豁免;
`server.max_body_bytes` / `server.relay_max_body_bytes` 可调, 超限回 413 并记一条 WARN 日志。

## 验证

- `go build ./internal/... ./cmd/...`、`go vet`、`go test ./internal/... ./cmd/...` 全绿,
  新增覆盖: `resolveSubject` 用真实渠道名、`errorClassLabel`、`BodyLimit` 六例(413/放行/分块截断/豁免/分档/GET 跳过)。
- `pnpm exec tsc --noEmit` 与 `pnpm exec vitest run` 全绿。
