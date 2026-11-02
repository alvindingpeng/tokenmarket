# 调用分析：数据与性能边界

## 故障根因

统计前端曾请求 `/user-stats/*`，但真实服务端路由是 `/api/v1/user-stats/*`。生产只读探测分别返回 404 和 401（后者正常进入鉴权）。前端把失败后的 undefined 默认成零，并等待三个查询全部结束，重试时间被误认为数据库慢。余额告警 API 存在相同的路径前缀问题，一并修正。

## 查询策略

统计只读取登录用户的 billing_records，时间使用闭开区间 `[start, endDate 次日零点)`。数据库负责 COUNT/SUM/GROUP BY，不拉取所有明细到前端或内存。计费口径（调用量/费用/词元含 refunded 行，模型分组只取 settled）保持不变；请求成功率改用独立数据源，见下节。

## 成功率口径（v1.5.6 修正）

修复前的成功率是 `settled 计费行 / 全部计费行`，结构上恒等于 100%：失败的调用不会产生计费行（没有用量就没有结算），所以分母里根本没有失败样本。线上 13861 条计费行全部 settled，而调用日志是 4388 成功 / 102 失败 / 5 取消（真实成功率 97.8%）。此外把 refunded 读成“不成功”也不对——退费是计费动作，与请求是否失败无关。

现在：

| 字段 | 数据源 | 含义 |
|---|---|---|
| `total_requests` / `total_cost` / `total_tokens` | billing_records | 计费口径，含 refunded 行 |
| `success_rate` | relay_requests 终态 | `success / (success + failed)` |
| `request_total` / `request_success` / `request_failed` / `request_canceled` | relay_requests | 请求口径计数，用于解释百分比 |
| `request_coverage_start` / `request_retention_days` | relay_requests + 设置 | 真实覆盖起点与日志留存天数 |

两条约束写死在实现里：

- **canceled 不进分母。** 客户端主动断开或超时取消是调用方行为，不代表服务不可用；计入分母会让“用户自己中断”拉低平台可靠性指标。
- **没有样本时返回 0 且计数为 0，由前端显示“—”。** 绝不因为计费行齐全就回填 100%。
- **计费口径与请求口径同时展示，各自标注。** 「总调用量」仍是只含产生用量调用的计费口径，卡片下方注明；成功率卡片给出成功/失败/取消三个计数，两者本就不相等，界面必须自己说清楚，否则会被拿去互除。

代价是 relay_requests 按 `log_retention_days`（生产 7 天）滚动删除，所以“最近 30 天”的成功率可能只覆盖最后几天。页面必须显式提示 `request_coverage_start`，与延迟面板同一原则：不假装覆盖整个区间。

趋势图按桶合并两个数据源。只按计费行建桶会把“当天有调用但一次都没结算”（即全失败）整段抹掉，恰恰是最该被看到的一天，因此日志桶也参与建桶，这类桶的调用量取日志终态计数。归桶规则由 `statsBucketExprOn` 参数化列名复用同一套表达式，确保两条序列按同样的日期切片。

新增 `idx_relay_requests_user_started (user_id, started_at)` 复合索引：成功率与延迟都按“用户 + 时间窗”聚合 relay_requests，只有单列索引时 SQLite 只能二选一。

新增 `(user_id, created_at)` 复合索引，由 GORM AutoMigrate 幂等创建；SQL WHERE 不在 created_at 上包日期函数，日期函数仅用于 GROUP BY，确保范围条件仍可走索引。SQLite 分桶先取存储时间的日期前缀，避免带 +08:00 等偏移的凌晨记录被 DATE 隐式转 UTC 后归入前一天；周桶标签为当周周一。MySQL/PostgreSQL 使用各自方言的日期表达式。原有单列索引保留。

不引入全局共享用户缓存或无失效机制的统计缓存，避免用户串数据、结算或退费后长期显示旧值。前端查询短期缓存由 TanStack Query 管理，并把当前用户身份纳入查询键，避免切换账号后复用另一用户的统计缓存。

## 性能验证

修复前生产约 13,861 条计费记录；成功率聚合改用 relay_requests 后的查询计划为 `SEARCH relay_requests USING INDEX idx_relay_requests_user_started (user_id=? AND started_at>? AND started_at<?)`，实测区间内 4,493 成功 / 102 失败 / 5 取消，成功率 97.78%。而修复前的三次只读聚合只走 user_id 单列索引，分别约 9.2 / 7.4 / 7.3 ms——可见当时页面卡顿主要不是数据库执行时间。复合索引的收益应在“历史记录多、选择的日期范围小”场景下评估，而不是承诺全范围聚合的数量级提速。

可用 `python3 scripts/benchmark-analytics-index.py` 复现独立 SQLite 内存实验：30 万条模拟记录、10 个用户、选取一周命中 535 条，20 次测量中位数从约 20.28 ms 降到 0.36 ms，COUNT/SUM 结果一致。此为选择性范围的算法验证，不代表生产全范围查询或页面端到端耗时。

回归检查包含真实 HTTP 路由鉴权/用户隔离、日期边界及周分桶、空集、EXPLAIN QUERY PLAN，以及前端 API 路径和失败/加载状态。生产验收不生成付费调用或修改业务数据。
