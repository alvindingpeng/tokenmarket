# 审计归档与用户多级配额

本批实现原先列在 `docs/backlog-unimplemented.md` 的两项："审计日志归档"与"用户分级与配额管理"。

## 1. 审计归档

### 问题
审计 CSV 导出早已有(上限 5000 行), 但**没有任何归档与清理**: `audit_logs` 只增不减。
把它塞进 `log_retention_days`(7 天)是错的 —— 审计是合规证据, 7 天保留会把证据顺手删掉。

### 设计
- 独立留存设置 `audit_retention_days`(默认 180 天)与 `audit_archive_enabled`(默认开)。
- 独立任务 `audit_archive`(每天一次, 启动即跑), 与调用日志归档分开。
- **顺序强制**: 先把超期记录写入当日 NDJSON 并 `fsync`, 确认落盘后才删除。
  归档失败时本轮**一条都不删**, 并发告警 `audit_archive_failed` —— 磁盘能修, 证据不能。
- 运维接口(管理员):
  - `POST /api/v1/ops/audit-lifecycle/run`(需 step-up) 立即归档清理;
  - `GET /api/v1/ops/audit-lifecycle/list` 查看归档文件与留存配置。
- 归档目录 `data/archives/audit/audit-YYYYMMDD.ndjson`, 追加写, 与调用日志归档并列。

### 验证
`internal/op/audit_archive_test.go`: 归档后删除且新记录保留、归档失败零删除、关闭归档只删不写。

## 2. 用户多级配额

### 设计
- `users` 新增 `tier` 列(free/basic/pro/enterprise), 通过 `EffectiveTier()` 统一出口。
- **空串回落 free**: 存量账号列值是空串, 若当成"无限制"会让所有老用户一夜之间无上限, 这是最危险的方向。
- 等级配额集中在设置键 `user_tier_policies`(JSON), 默认四档:
  | 等级 | RPM | TPM | 并发 |
  |---|---:|---:|---:|
  | free | 20 | 40,000 | 2 |
  | basic | 120 | 400,000 | 8 |
  | pro | 600 | 2,000,000 | 32 |
  | enterprise | 不限 | 不限 | 不限 |
- **兜底而非覆盖**: 仅当用户维度没有任何显式限流策略时才按等级补位;
  管理员为个别用户单独配的 RPM/TPM 永远优先。否则精细策略会被等级整个盖掉。
- 配置写坏(非法 JSON / 未知等级 / 负数)时校验拒绝保存; 运行期读不到则回落出厂默认, 不会变成"全平台无限制"。
- `role` 与 `tier` 正交: 管理员也可以是免费用户, 渠道商也可以是 pro。

### 验证
`internal/model/user_tier_test.go`、`internal/op/user_tier_test.go`: 空串回落、未知等级兜底、非法 JSON 拒绝、清空等级、默认值方向正确。

## 上线记录(v1.5.0)

- 生产已升级到 v1.5.0, 健康检查 status/db=ok, 版本一致。
- 迁移 `users.tier` 成功, 存量账号该列为空串(生效 free), 逐一核对无非法值。
- 数据保全核验:
  - `billing_records` 12510 -> 12519、`balance_ledgers` 25621 -> 25640 为升级窗口内的真实业务流量;
    抽查用户 1 余额减少 0.00123487182 与其 `total_spent` 增加完全一致, 新增 12 笔均为正常计费。
  - `audit_logs` 292 条、`channels`、`channel_models` 逐列比对未变。
- 新设置键已播种: `audit_retention_days=180`、`audit_archive_enabled=true`、`user_tier_policies` 默认四档。
- 首次部署出现过一次保护性回滚: 校验脚本按整行比对 `users`, 迁移新增的 `tier` 列使每行都"不同"而误判。
  回滚后服务恢复旧版本、数据库无损; 改为"只比对迁移前已存在的列"后重新升级成功。该检查已固化为列级比对。

## 未做的事(明确不在本批)
- 等级不改变模型访问权限, 只影响限流兜底额度; 按等级限制可用模型属于另一项工作。
- 审计归档不推到对象存储(S3/OSS), 仅落本地磁盘; 异地推送如需要应复用备份的远端通道单独设计。
