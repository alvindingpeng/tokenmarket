# 更新说明

## v1.10.16 — 2026-11-21

**SQLite 并发写入"抖动"的根因治理: 短事务在读后写之前先取写锁, 日结不再靠重试硬扛**

### 一、问题: 报错不是"偶尔锁一下", 是必然会失败

压测下的现象是: 转发写入偶发 `database is locked`, 日结单生成在并发时大面积失败(靠重试
救回), 首包/尾包延迟出现秒级尖刺。第一反应都是"把 `busy_timeout` 调大"或"全局开
`_txlock=immediate`" —— 实测两条路都不对:

- `busy_timeout` **完全不覆盖** 真正的那条错误路径(见下), 调多大都没用;
- 全局 `_txlock=immediate` 让**只读事务**也去抢写锁: 一条 1 秒的只读事务会把写入者挡
  1.032 秒(实测), 抖动不但没消失还扩散到了纯读路径。

根因在 SQLite WAL 的事务模型: 一个事务若**先读后写**, 它一开始只持有读快照; 写的那一刻要把
快照"升级"成写事务, 而升级窗口内只要库里有任何别的提交, 升级就被拒绝并返回
`SQLITE_BUSY_SNAPSHOT`(**517**) —— 这条路径**不看** `busy_timeout`, 唯一出路是整
事务回滚重来。窗口宽度 = 从第一条读到最后一条写之间的时间, 所以事务越长、并发写入越密,
失败率越接近必然。

### 二、修法: 只为"短的读后写事务"提前取写锁

新增一张**永远为空**的锚点表 `db_write_anchor` 与 `db.AcquireWriteLock(tx)`: 事务开头
执行一条零命中的空更新(`UPDATE db_write_anchor SET id = id WHERE 0`), 用一次无害的写把事务
**在第一条语句就变成写事务**。等价于 `BEGIN IMMEDIATE`, 但**按事务选择**而不是全库选择。

为什么用独立空表而不是借用业务表: 业务表会被留存策略清理、被 ETL、被未来改名, 而取锁语句
必须永远成立 —— 依赖它们的可用性就是把资金路径的稳定性押在别处的实现细节上。

实测(6 路热写 + 4 路短读后写, 8 秒窗口):

| 指标 | 修前 | 修后 |
|---|---|---|
| 短事务失败次数 | **21136** | **0** |
| 热路径 p99 | 34.4ms | **2.45ms** |
| 一次取锁的 WAL 增量 | — | **0 字节**(2000 次取锁后锚点表仍 0 行) |

牺牲的上界是"取锁后要等真正持锁者释放": 实测在一个持锁 1.2 秒的对手后面排了 1.265 秒,
`err=nil`。这是**排队**而不是失败 —— 排队时间有界(等于对手事务长度), 失败重试的等待没有界。

覆盖范围刻意收窄到 **17 处短事务**(webhook 建立/更新/测试/重放、兑换码、充值通知与人工完成、
提现审核/解冻/标记已付/回滚、申诉审核、分组更新与加项、渠道自动加组、日结写入、Webhook 租约领取)。
判断标准只有一条: **第一条语句是写的事务不需要改**(它天然就是写事务); **只读事务绝不能取锁**
(会把读路径变成写路径); **长事务绝不能开头取锁**(会把整条写路径串行化)。

### 三、日结结算单: 从"长事务里读+写"拆成"只读聚合 + 短写事务"

日结原本是"一个事务里先按日聚合(几十万到上百万行)再写结算单", 正是最坏的形状 ——
窗口宽度等于整表聚合耗时。现在拆成两步: 聚合(含当日批准退款与退款原账单三处读取)在**一个
只读事务**里读(只为拿到同一个快照, 在 WAL 下只持读快照、永不升级、不挡任何写入者),
写事务只剩"读几行已有单据 + 写几行结算单", 并在开头取锁。

- 原子性没有损失: 结算单不可变由 `settlementWriteRow` 的状态守卫(条件更新)保证,
  已审核单的差异校验也在持锁的写事务里重新读当前行, 校验用的旧值与写入时的现值同一时刻。
- 语义上多了一个**明确的时间窗**: 聚合读发生在取锁之前, 期间新落库的明细属于下一次重算
  (幂等 upsert 会补齐)。这是有意选择 —— 代价是"日结晚一轮才看到最后几条", 换来的是"不再失败"。

实测(6 路转发写 + 日结, 对照三种形状):

| 形状 | 日结失败 | 日结 p50 | 热路径 p99 |
|---|---|---|---|
| A 单事务整表聚合(修前) | **24 次** | 561ms | 恶化到秒级 |
| B 聚合挪到事务外 + 短写事务(现在) | **0** | 62ms | 3.49ms |

### 四、重试窗口: 750ms → 1.1~1.7s, 10ms 起指数退避截顶 500ms + 正向抖动

修前总退避窗口 750ms 比对手事务还短, 于是每轮重试都落在对方的持锁区间里 —— 实测并发下
**26 次生成 24 次失败**。窗口拉长后单靠重试也只能救到 6/8: 重试是**兜底**, 真正的修法是上面的
锁获取顺序, 两者不可互相替代。

退避从"固定 150ms"改成 `min(2^n × 10ms, 500ms)` 再叠加 `[0, 上界/2)` 的**正向**抖动:
抖动只向上取, 保证"越等越久"仍然单调(否则抖动会把退避退化回上一轮的长度); 用尝试次数做
确定性混合而不是全局随机源, 让退避序列可重现、可断言。

### 五、测试

- 新增 `internal/db/txlock_test.go`: 锚点表恒空、nil 事务无副作用、缺表时自动建表、
  "延迟升级必失败 / 提前取锁则排队成功"的对照、8 写 + 4 读后写并发下零冲突。
- 改写 `TestSettlementRetryWALSnapshot` 为
  `TestSettlementGenerateQueuesOnWriterLockAndRefreshes`: 第二个连接**真实持有**写事务,
  断言日结既不在持锁期间完成、也不以锁错误失败, 对方提交后正常完成且**全程 0 次 517**;
  并钉住"取锁前的明细属于下一轮"这条时间窗语义, 防止将来被误当 bug 改掉。

验证: `go build ./...` + `go vet` 通过; `internal/op`(101s)、`internal/db`(含 migrate)、
`internal/task` 全绿。

### 六、顺带修掉一个被 e2e 抓到的窄屏布局缺陷(运维中心页签盖住按钮)

本轮把 `scripts/check.sh` 全量跑通时, 结算补偿后台的 390px 用例一直超时: "处理到期任务"按钮
点不动。看错误快照才发现不是偶发 —— **14 个运维页签在窄屏只放得下第一行, 其余行溢出到
页签列表盒子之外, 正好压住下面的操作按钮**, 点击落在别的页签触发器上。宽屏(1280px)一直正常,
所以这个缺陷从上线起就没被发现过。

根因是页签列表的基础样式写死了 `h-9`(36px), 而 `flex-wrap` 只能换行、不能撑高盒子。
修法: 页面级覆盖 `h-auto!` 解掉固定高度, `[&>button]:h-8!` 把触发器从 `100%-1px` 改成固定行高
(换行后每行才登高), `w-full` 只在窄屏生效、宽屏维持原来的 `w-fit` 外观。

验证: `billing-recovery.spec.ts` 5 个用例全绿(此前 390px 那条必挂); `eslint` 与 `tsc -b --noEmit` 通过。

## v1.10.15 — 2026-11-21

**前端 lint 清零, 并修掉三条被 lint 挡住的真缺陷**

`pnpm run lint` 在 v1.10.13 上就是红的(18 errors / 3 warnings), `scripts/check.sh` 的 lint 阶段
因此从来没能通过过。逐个看, 其中三条是真缺陷而不是规则噪音:

1. **`useMemo` 写在提前 return 之后(`market/index.tsx`)** —— 出错分支比正常分支少调用一个
   hook, 恢复时多调一个, 这是 React 的 hook 顺序违规(会直接崩)。收藏集合的 useMemo 提到 return 之前。
2. **组件体内定义组件(`CliHint.tsx` 的 `CopyButton`)** —— 每次渲染都是新的组件类型, React 整棵
   子树重建, 复制后的对勾状态随之丢失, 父组件每敲一个字符还会重建一次。提成文件级组件。
3. **`rows = query.data ?? []`(`accounts/index.tsx`)** —— 每次渲染产生新的空数组字面量,
   下游两个 useMemo 的依赖每次都变, 过滤与汇总等于每帧重算。包一层 useMemo。

其余为未用导入/未用变量/`any`/Fast-Refresh 导出约定:

- `eslint.config.mjs`: `no-unused-vars` 放开 `_` 前缀(mock 的 fetch 签名本就是占位);
  `react-refresh/only-export-components` 的 allowExportNames 补上三个被单测直接断言的纯函数导出;
- `AccountImport.tsx` / `AccountImportBatch.tsx`: `any` 换成 `Record<string, unknown>`, 并把 name
  收窄成 string —— 直接贴凭据 JSON 时 `item.name` 可能是对象, 取出来会得到 `[object Object]` 条目名;
- `AddToGroup.tsx`: 排序 useMemo 的依赖写全, `missingCount` 改在 useMemo 内就地算
  (外层函数每次渲染都是新引用, 放进依赖数组等于关掉记忆化);
- `analytics/index.test.tsx`: 删掉未用的 `todayRange` 绑定。

验证: `eslint .` 退出 0(0 error / 0 warning); `tsc -b --noEmit` 通过; `pnpm test` 62 文件 549 用例全绿。

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
