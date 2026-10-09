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
`octopus-e2e-ab.py` 全链路验证:

1. 慢上游(20ms) + 阈值 1ms → `degraded` 告警 + Webhook 送达(notified=true)
2. 阈值抬高后探测 → `recovered` 补发
3. 死渠道前 4 次失败无告警, 第 5 次 → `down`(防抖生效)
4. test-webhook 端点 200, mock 告警送达
5. 造一条漏结算记录 → 对账 `mismatch` → 告警 + Webhook 送达
6. Webhook 共收 5 条(degraded/recovered/down/test/mismatch)

## 部署

- 构建: `pnpm exec tsc --noEmit && pnpm run build` + `go build` + `go test ./...` 全绿。
- 二进制 SHA256: `ef8452df84bc497d1db5acb5040a42dee8e062402733f5e7d86b330969f70d22`
- 生产: `/opt/octopus-multiuser/octopus`, 备份 `backups/alert-loop-20261008-162658`, 服务 active, 启动日志干净(自动备份 verified=true)。
- 新键 `health_latency_ms` 启动时自动补齐默认值。

## 注意事项

- 健康探测默认关闭(可能产生上游费用), 开启后才会有健康/告警记录。
- Webhook 建议配合测试按钮先行验证, 避免真实故障时才发现配置错误。
- `down` 告警去重窗口 10 分钟: 持续宕机每 10 分钟最多一条, 恢复必发一条 `recovered`。
