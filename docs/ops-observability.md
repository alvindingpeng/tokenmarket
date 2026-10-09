# 可观测性: 日志检索/导出 · /healthz · /metrics · 运维概览

## 日志检索增强

历史日志(管理后台 > 调用日志)新增三个独立筛选维度, 与关键字/终态/时间范围叠加生效, 全部模糊匹配:

| 筛选 | 匹配列 |
|---|---|
| 客户端 IP | `client_ip` |
| 模型 | `model` / `target_model` |
| 渠道 | `target_channel_key` / `target_channel_code` |

CSV 导出: 「导出 CSV」按钮走 `GET /api/v1/log/export`, 与列表同一套筛选条件与可见性(管理员全量, 普通用户仅自有), 单次上限 5000 条, 超出时文件尾以 `# truncated` 注释行提示。 导出行为进审计日志(`log.export`)。

## /healthz 存活探针

无需登录, 返回进程与数据库状态; 数据库异常时以 503 + `status=degraded` 表达降级:

```json
{ "status": "ok", "uptime_seconds": 123, "version": "dev", "db": "ok", "active_requests": 0 }
```

## /metrics Prometheus 指标

无需登录, 文本格式(`text/plain; version=0.0.4`), 只暴露聚合数字:

| 指标 | 含义 |
|---|---|
| `octopus_up` | 恒为 1, 进程存活 |
| `octopus_uptime_seconds` | 运行时长 |
| `octopus_build_info{version,commit}` | 构建信息 |
| `octopus_relay_requests{status}` | 各终态累计调用 |
| `octopus_relay_tokens_total{direction}` | 输入/输出 token 累计 |
| `octopus_billing_cost_total` | 累计计费 |
| `octopus_users` / `octopus_user_balance_total` | 用户数与余额合计 |
| `octopus_channels{state}` | 渠道总数/启用数 |
| `octopus_alerts_raised_24h` | 近 24 小时告警数 |
| `octopus_active_requests` | 进行中请求 |
| `octopus_goroutines` / `octopus_memory_bytes{kind}` | 运行时资源 |

注意: `/metrics` 与 `/healthz` 无鉴权, 便于探针直采; 需要收敛时在反代层(Nginx/Caddy)对 `/metrics` 加来源限制或 Basic Auth。

## 运维概览(仪表盘)

管理后台 > 运维中心 顶部新增概览卡片(30s 自动刷新): 24h 请求/成功率/Token/费用 + 进行中请求/渠道 启用-总数/24h 告警。
数据来自 `GET /api/v1/ops/overview`(登录+管理员), 与 `/metrics` 共用同一套聚合实现(`internal/op/metrics.go`)。

## 限流实时用量修复

此前「限流管理 > 实时用量」的系统默认/渠道/渠道模型无数据: 用量只在用户与 API Key 两个维度落表。 现已全量落六维度(系统默认/用户/API Key/渠道/渠道凭据/渠道模型), 用户侧触顶与上游触顶分别计入对应维度的 `rejected`。
