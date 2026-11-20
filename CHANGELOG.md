# 更新说明

## v1.10.14 — 2026-11-21

**四张核心资金表开始有留存策略(180 天), 且清理不会让任何终身数字缩水**

### 一、问题: 四张表只增不减

`billing_records` / `balance_reservations` / `balance_ledgers` / `billing_settlement_tasks`
从来没有清理策略。自动备份是 `VACUUM INTO` 物理快照, 存量会被每份备份整份复制 ——
明细越多, 备份越大越慢, 而其中绝大部分是半年前的死数据。

但**直接删会算错钱**, 三处硬约束(每处都在 `internal/op/billing_retention.go` 顶部写明):

- `billing_records` 是"用户累计消费""可提现额度""平台全期毛利"的唯一数据源, 删了这些数字会缩水;
- 某一行可能正被 `disputes` 引用: 批准退款时 `tx.First(&record, dispute.RecordID)` 找不到行就退不了款;
- `balance_reservations` / `billing_settlement_tasks`: `BillingReleaseStale` 用
  `NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE reservation_id = ...)` 判断"有没有结算意图"。
  删掉任务行, 一笔仍在等结算的预扣会被当成泄漏释放 —— 冻结额退了, 结算再落账就是同一笔钱既退又扣。

### 二、做法: 先固化日汇总, 再删明细

新增 `billing_rollups` 表, 粒度 = **自然日 × 维度(user / owner / channel / platform)**。

- 删某一天的明细之前, 在同一事务里把这一天的聚合值写进汇总表, 再删这一天的明细;
- 终身口径查询 = 汇总表 ∪ 明细表;
- 汇总行带 `day`(YYYYMMDD), 所以"只有 `created_at <= cutoff` 的部分才成熟"这个语义仍然成立;
- 汇总行带 `refunded_*` 一组列: `BillingTotal` 计全部行, `maturedRevenueIn` / `ChannelProfit`
  只计未退款行, 只存一份合计必有一方算错。

被改成"汇总 ∪ 明细"的读口: `BillingTotal`、`BillingDaily`/`BillingMonthly`、`maturedRevenueIn`、
`ProfitMarginGet`(overall + trends)、`ChannelProfit`。

### 三、删除前的四个硬条件

1. **归档先行**: 先把要删的行原样写成 NDJSON(`data/archives/billing/billing-YYYYMMDD.ndjson`),
   `file.Sync()` 落盘后才进入删除; **归档失败 ⇒ 本轮一条都不删**。
2. **归档上界**: 归档时记录四张表各自覆盖到的最大主键, 删除只允许落在 `id <= 该上界` 的范围内 ——
   只按 `created_at` 划界, 归档读与删除之间新写入的行会被"没归档就删掉"。
3. **申诉保护**: 只删没有任何 `status <> 'rejected'` 的 dispute 引用的明细。
4. **结算意图保护**: 预扣只删 `settled = true` 且无结算任务行的; 结算任务只删 `completed` 且
   对应预扣已结算的。`pending` / `retry` / `manual` 一行不删。

### 四、补上"假零值"风险

汇总表没有逐行的 `fx_day` / 汇率快照, 补不出对账与重估所需的数据。这类读口改为**显式报错**
而不是返回 0(假零值会被读成"这段时间没问题", 人一旦据此定性, 错误结论就进了对账单):

- `upstreamBillOwnerRevenue`: 区间无明细但存在汇总行 ⇒ `ErrBillingWindowArchived`;
- `GET /api/v1/admin/fx/revaluation`: 该月明细已归档 ⇒ 400;
- `GET /api/v1/admin/fx/reconciliation`: 窗口内明细已归档 ⇒ 400。

### 五、配置与运维

| 设置键 | 默认 | 范围 |
| --- | --- | --- |
| `billing_retention_days` | 180 | 1–3650 |
| `billing_archive_enabled` | true | 布尔 |

- 后台任务 `billing_retention`: 24 小时一次, 启动即跑, 单轮 10 分钟超时, 内部逐日分批事务;
- 设置改完经 `task.ApplySetting` 立即热更新;
- 运维页新增 `/api/v1/ops/billing-lifecycle/{run,list}`(run 需二次验证), 设置页新增「资金表留存」卡片
  (五张表当前行数 + 归档文件清单 + 立即清理);
- 归档文件纳入 `archive_retention_days` 的清理(前缀 `billing-`), 最近 3 份始终保留。

### 六、明确不做: 租约式选主

多实例 leader election 不在本次范围。本次只解决单实例下"四张表无限膨胀 + 清理不能算错钱";
多实例遗留问题(进程级缓存、内存限流桶、进程内定时器)按现状保留。

### 七、顺带修的一处测试竞争

`TestBreakerCacheFailedWritesDoNotChangeCache` 在 `-race` 下报数据竞争: 用例在清理阶段摘掉
gorm 更新回调时, `RecordChannelKeyError` 起的告警派发协程还在同一个全局回调表里执行 `Updates`。
现在先 `AlertDispatchDrain()` 再摘回调。

## v1.10.13 — 2026-11-17

**修复: 上游 429 后的重试不退避, 把"偶发限流"放大成"整段请求必然失败"**

订阅模型调用时用户看到：

```
failed to stream request: Request failed: Too Many Requests, error:
Resource has been exhausted (e.g. check quota)., code: 429, type: RESOURCE_EXHAUSTED
```

而 `retrieveUserQuota` 显示 17 个 bucket 全部 `remainingFraction=1`（额度满，重置在下周）——
额度没耗尽，是上游的速率窗口被打满。

**根因**：`ErrorClassRate` 分支原本是**裸 `continue`** —— 上游刚用 429 说"本窗口已满"，
网关立即重选同一成员接着打。单账号订阅渠道只有一个候选，摘掉探测名额后下一轮又选它，
于是 `max_total_attempts=10` 在约 **2 秒内**全部砸向已满的窗口。线上实测各轮间隔：

```
22:45:42 limited dur=269ms
22:45:43 limited dur=215ms   ← 2 秒内 10 次, 每条都是真实上游 429
22:45:43 limited dur=217ms
22:45:44 limited dur=270ms   ... 第 10 轮后 max_total_attempts 耗尽
```

同一账号、同一模型、同一 payload 的对照实测：

| 节奏 | 结果 |
| --- | --- |
| 间隔 8 秒，串行 10 次 | **10/10 成功**（1.0–1.4s） |
| 间隔 2 秒，12 次 | **12/12 成功** |
| 间隔 0.6 秒，12 次 | **12/12 成功** |
| 网关 10 轮内部重试（≈2 秒内） | **10/10 全部 429** |

上游本身在正常节奏下完全可用 —— 是网关自己的重试风暴把它打挂了。

**修复**（`internal/relay/handler.go`）：429 之后**退避再重试**，且这类重试**不吃** `MaxTotalAttempts`：

1. 退避时长 = `member_retry_interval_seconds` × 1/2/4/8（指数递增，封顶 8 倍）；
2. 单请求最多 3 次 429 退避重试，超过则透传上游 429；
3. 剩余 deadline 不足以支付退避时**立刻**返回，不把用户挂到超时；
4. 预算检查为"退避换来的那一轮"放行（`pendingRateRetry`）—— 否则紧预算（实测可到 1）下
   一次瞬时 429 会在退避前就被判死。

不计入总预算的理由：429 说明请求根本没被模型处理（与准入拒绝同性质）。若照计，退避反而
更快撞到 `max total attempts exceeded`，比不修更差。真正打到模型并失败的轮次照旧计。

**验证**：新增 6 条测试，其中 2 条端到端（httptest 上游返回真实 429 报文）证明
"两次 429 后退避 3.2s 成功"与"持续 429 时 7.2s 快速失败"。变异验证：把退避改成 0 后
端到端测试捕获到"总耗时仅 16.5ms"——正是原始缺陷的特征。
