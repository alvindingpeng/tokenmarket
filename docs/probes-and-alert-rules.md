# 探针鉴权与告警规则配置

## 一、`/metrics` 鉴权选项

默认要求 bearer 令牌(只暴露聚合数字, 不含业务明细); 需要明确开放抓取时把设置 `metrics_auth` 切为 `off`。

| 设置键 | 取值 | 默认 | 说明 |
|---|---|---|---|
| `metrics_auth` | `bearer` / `off` | `bearer` | 抓取 `/metrics` 是否需要令牌; 已有显式 `off` 保持不变 |
| `metrics_token` | 48 位十六进制 | 自动生成 | 抓取凭据, **内部键**: 不进 `/setting/list`, 不进备份 |

鉴权模式下的两种携带方式(任一即可):

```
curl -H "Authorization: Bearer <token>" http://host:8088/metrics
curl "http://host:8088/metrics?token=<token>"
```

未通过校验返回 `401` 并带 `WWW-Authenticate: Bearer realm="metrics"`。

### 设计取舍

- **`/healthz` 恒不鉴权**: 它是编排系统的存活探针, 只返回进程/数据库状态与活跃请求数; 加鉴权会让探活失效, 而漏掉的收益很小。
- **令牌自动生成**: 切到 `bearer` 时若尚无令牌, 立即生成并在该次响应里回显一次, 避免"打开鉴权的同时把自己关在门外"。
- **令牌不进备份**: 凭据不应随数据库快照流转到异地副本。恢复后 `metrics_auth` 仍为 `bearer` 而令牌为空, 此时接口**失败关闭**(401), 管理员在运维面板点「重新生成」即可。
- **可轮换**: `POST /api/v1/ops/metrics/token/rotate` 立即作废旧令牌, 用于凭据泄漏处置; 轮换进审计(`ops.metrics-token-rotate`)。
- **配置入口唯一**: 模式改走已有的 `/api/v1/setting/set`, 令牌查看/轮换走 `/api/v1/ops/metrics/token[/rotate]`, 不新开第二套配置界面。

## 二、告警规则可配置

原先硬编码的三条规则参数已全部搬进设置, 并在 `GET /api/v1/ops/alerts/rules` 回显**当前值 + 默认值**(管理员专用), 让"界面上看到的数字"与"引擎真正使用的数字"永不脱节。

| 规则 | 设置键 | 默认 | 作用 |
|---|---|---|---|
| 去重窗口 | `alert_dedup_minutes` | 10 | 同渠道同类型同原因在窗口内只告警一次; **0 表示不去重**(排查阶段用) |
| 抖动抑制 | `alert_fail_streak` | 5 | 连续失败多少次才判定 `down`, 抑制单次抖动 |
| 延迟阈值 | `health_latency_ms` | 1000 | 健康但延迟超阈值发 `degraded`; 0 关闭该规则 |

`alert_dedup_minutes` 的边界是 0-1440 分钟, `alert_fail_streak` 是 1-100, 越界由 `Setting.Validate()` 以 400 拒绝。两者与 `health_latency_ms` 一起在运维面板「运营可靠性」中编辑。

## 三、验证

`switchyard-e2e-billing.py` 94/94, 其中本项新增 20 条: 规则回显与默认值、非管理员 403、改设置后生效值跟随、越界 400、`/metrics` 默认开放 → 切 `bearer` 后无令牌/错令牌 401 → 查询串与 Header 两种携带方式均 200、`/healthz` 全程 200、令牌不出现在 `/setting/list`、轮换后旧令牌 401 新令牌 200、恢复 `off` 后重新开放。

## 告警送达与补发

外发失败不是终点。每条告警都会记录送达状态，失败后由后台任务重试：

| 字段 | 含义 |
| --- | --- |
| `notified` | 是否至少有一个渠道送达成功。 |
| `notified_at` | 送达时刻；只在 `notified` 为真时有意义。 |
| `notify_attempts` | 已尝试的外发轮数（含首发）。 |
| `next_notify_at` | 下一次补发时间；为空表示不排期（已送达 / 已放弃 / 从未尝试）。 |
| `last_notify_error` | 最近一次失败原因；送达成功后清空。 |

**补发节奏**：首发失败后按指数退避排期，2 分钟起，单次上限 2 小时，累计 8 轮为上限。
送达即停止并清空失败信息；达到上限则放弃，但记录与原因保留可查。
后台任务 `alert_redeliver` 每 5 分钟执行一轮，单轮最多 50 条、有界 2 分钟。

**Markdown 转义与降级**：Telegram 走 `parse_mode=Markdown` 时，标题、正文、时间三处都必须转义——
错误文本里的下划线、星号、反引号、方括号都是实体起始字符。
漏转义的后果是整条消息被 Telegram 以 400 `can't parse entities` 拒绝，而告警就此消失。
因此除转义外还有一层兜底：识别到实体解析错误时，去掉 Markdown 以纯文本重发一次。
只有实体解析错误才降级；token 未配置、chat_id 无效这类参数错误重发同样失败，直接记录原因。

**可观测性**：运维页告警区展示待补发与已放弃条数、24 小时成败计数、最近失败原因，
并可展开未送达列表查看逐条的尝试次数与排期。接口：

- `GET /api/v1/ops/alerts/delivery` — 送达概览
- `GET /api/v1/ops/alerts/undelivered` — 未送达列表
- `POST /api/v1/ops/alerts/redeliver` — 立即补发一次（后台任务会周期执行，此为手动触发）

排查建议：若「已放弃」持续增长，先看 `last_notify_error`。
`telegram bot token not configured` / `chat not found` 属于配置错误，补发不会自愈，需在设置页修正；
`connection reset` / 超时属于外部抖动，补发通常能自行恢复。