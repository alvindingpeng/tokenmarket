# 第二批运营可靠性升级

已实施并部署到生产服务 octopus-multiuser (202.6.204.216:8088)。

## A+B: 计费价格快照与对账

### 价格快照
- 每笔计费记录保存 supply_price(供货价)、user_price(用户价)、markup_ratio(上浮比例)、price_source(价格口径)。
- 改价不影响历史账单解释: 旧账单可按当时价格复核。

### 每日对账
- 每日自动核对昨日账目, 只登记结论与差异, 绝不自动修改用户余额。
- 检测: 重复结算、漏结算、长期预扣、三方对平。
- 状态: balanced / mismatch / review。
- 管理端 API: POST /api/v1/ops/reconcile/run, GET /api/v1/ops/reconcile/list。
- 余额流水: GET /api/v1/ops/balance-flow。

## C+D: 调用日志 IP / 7 天留存 / 归档清理 / 脱敏

### 调用 IP
- relay_requests.client_ip 记录调用方地址, 优先取 X-Forwarded-For 首个地址。
- 日志详情展示调用 IP。

### 日志生命周期
- 默认留存 7 天, 可在设置中调整 log_retention_days(1-3650)。
- 每小时自动执行: 归档超期记录 -> 清空正文 -> 删除记录。
- 归档: NDJSON 格式, 按日期命名, 保存到 data/archives/。
- 归档失败不删除数据。
- 手动触发: POST /api/v1/ops/log-lifecycle/run。

### 正文脱敏
- 保存正文前自动脱敏敏感字段: api_key, authorization, password, secret, token, access_token, refresh_token, cookie。
- 支持自定义脱敏字段(log_mask_fields)。
- 递归处理嵌套对象与数组, 键名大小写不敏感。
- 可关闭正文保存(log_store_body=false)。

## E: 自动一致性备份
- 每天自动备份 SQLite(VACUUM INTO 独立快照)。
- 备份后自动校验关键表存在。
- 保留策略: 默认 7 份, 多余清理。
- 手动触发: POST /api/v1/ops/backup/run。
- 备份目录: data/backups/。

## F+G: 渠道健康检查与告警

### 健康检查
- 默认关闭(health_check_enabled=false), 开启后按间隔探测。
- 探测方式: GET 请求渠道 BaseURL, 记录延迟、状态码、健康状态。
- 每渠道保留最近 1000 条。

### 告警
- 渠道异常或恢复时生成告警记录。
- 去重: 同渠道同类型 5 分钟内不重复。
- Webhook 通知(可选): 设置 alert_webhook_url(仅 http/https)。
- 查看告警: GET /api/v1/ops/alerts/list。

## 配置入口

| 设置键 | 默认 | 说明 |
|---|---|---|
| log_retention_days | 7 | 日志留存天数 |
| log_archive_enabled | true | 清理前先归档 |
| log_store_body | true | 是否保存正文 |
| log_mask_fields | (空) | 额外脱敏字段 |
| auto_backup_enabled | true | 自动备份开关 |
| auto_backup_keep | 7 | 备份保留份数 |
| auto_backup_interval | 24 | 备份间隔(小时) |
| health_check_enabled | false | 渠道健康探测 |
| health_check_interval | 30 | 探测间隔(分钟) |
| alert_webhook_url | (空) | 告警 Webhook 地址 |

## 验证
- Go 全量测试、go vet、race 检查通过。
- TypeScript 检查、Vite 生产构建通过。
- E2E 21 项通过。
- 生产 GUI 200, ops API 未认证 401, 服务 active。
- 启动时自动备份 verified=true。

## 部署记录
- 二进制 SHA-256: b534600b3bcbf101ca4230c55c5b0d763789472e56ee332f3d34dd1143a0ea25
- 备份: /opt/octopus-multiuser/backups/second-batch-20261008-111538

## 注意事项
- 日志留存是自然日计算, 每小时清理一次。
- 备份是 SQLite VACUUM INTO 快照, MySQL/PostgreSQL 需 DBA 离线备份。
- 渠道健康探测会产生真实 HTTP 请求, 默认关闭。
- Webhook 通知仅在设置地址后启用。
