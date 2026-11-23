# 四张资金表留存与日汇总(180 天) — 实施说明

> 范围: 非 SaaS 主线版本。本文记录"billing_records / balance_reservations / balance_ledgers /
> billing_settlement_tasks 四张表 180 天留存"这件事的完整设计与落地细节。
> 租约式选主(多实例 leader election)**明确不在本次范围内**, 见文末。

## 1. 为什么不能"直接删超期行"

四张表以前没有任何清理策略: 只增不减。而自动备份是 `VACUUM INTO` 物理快照, 存量会被每份
备份整份复制 —— 明细越多, 备份越大越慢, 这是必须清的理由。

但直接删会**算错钱**, 有三处硬约束:

| 表 | 谁在读 | 直接删的后果 |
| --- | --- | --- |
| `billing_records` | `BillingTotal`(用户累计消费)、`maturedRevenueIn`(可提现额度)、`ProfitMarginGet`(平台全期毛利)、`ChannelProfit`、`BillingDaily/Monthly` 曲线、`UserStats*` | 终身口径随删除一起缩水: 用户的累计消费变小、可提额度凭空减少、看板收入比实际低 |
| `billing_records` | `DisputeReview`(`tx.First(&record, dispute.RecordID)`)、`SettlementRun`(按 `record_id` 回查退款明细) | 被申诉引用的行没了就**退不了款** |
| `balance_reservations` | `BillingReleaseStale`: `NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE reservation_id = ...)` 判断"有没有结算意图" | 删掉任务行后, 一笔仍在等结算的预扣会被当成泄漏释放 —— 冻结额退了, 随后结算再落账, 同一笔钱既退又扣 |
| `billing_settlement_tasks` | 人工对账(`manual` 状态是自动路径放弃判断后唯一的证据) | 删掉就没法人工复核 |

## 2. 做法: 先固化汇总, 再删明细

新增 `billing_rollups` 表, 粒度 = **自然日 × 维度(user / owner / channel / platform)**。

- 删除某一天的明细**之前**, 在同一个事务里把这一天的聚合值写进汇总表, 然后删这一天的明细;
- 终身口径查询 = `汇总表 ∪ 明细表`;
- 汇总行带 `day`(YYYYMMDD), 所以 `created_at <= cutoff` 这类"按时间窗过滤"的语义仍然成立;
- 汇总行带 `refunded_*` 一组列, 因为 `BillingTotal` 计**全部**行, 而 `maturedRevenueIn` /
  `ChannelProfit` 只计**未退款**行 —— 只存一份合计必有一方算错, 存"合计 + 其中退款"即两边都精确;
- 单轮删除量有上限、逐日分批事务, 超出的留到下一轮(留存是幂等收敛的, 慢一天没有代价,
  把线上写事务拖住有代价)。

## 3. 删除前的四个硬条件

1. **归档先行**: 先把要删的行原样写成 NDJSON(`data/archives/billing/billing-YYYYMMDD.ndjson`),
   `file.Sync()` 落盘后才进入删除; **归档失败 ⇒ 本轮一条都不删**(与 `AuditLifecycleRun` 同约定)。
2. **归档上界**: 归档时记录四张表各自覆盖到的最大主键, 删除侧只允许删 `id <= 该上界` 的行。
   只按 `created_at` 划上界不够 —— 归档读与删除之间的时间差里可能写进新行, 按时间戳删就会删到
   没归档的那条, 而删除不可逆。
3. **申诉保护**: `billing_records` 只删"没有任何 `status <> 'rejected'` 的 dispute 引用"的行。
   判据用 `<> rejected` 而不是"存在任何申诉", 因为已驳回的行不再有任何读者。
4. **结算意图保护**: `balance_reservations` 只删 `settled = true` **且**没有任何
   `billing_settlement_tasks` 行的行; `billing_settlement_tasks` 只删 `completed` 且对应预扣
   已结算的行。`pending` / `retry` / `manual` 一行不删。

## 4. 哪些读口被改成"汇总 ∪ 明细"

| 读口 | 位置 | 合并口径 |
| --- | --- | --- |
| 用户/发布者累计 | `BillingTotal` | 全部明细 + 汇总合计 |
| 用户/发布者曲线 | `BillingDaily` / `BillingMonthly` | 明细日桶 + 汇总日桶, 按日期合并 |
| 可提现额度 | `maturedRevenueIn` | 未退款明细 + 汇总里 `owner_revenue - refunded_owner_revenue`(且 `day <= cutoff`) |
| 平台全期毛利 | `ProfitMarginGet`(overall + trends) | 全部明细 + `platform` 维度汇总 |
| 渠道利润 | `ChannelProfit` | `status = settled` 明细 + `channel` 维度汇总的"合计 − 退款" |

不合并、改为**显式报错**的读口(汇总表补不出来, 因为它们要逐行看 `fx_day` / 汇率快照):

| 读口 | 行为 |
| --- | --- |
| `upstreamBillOwnerRevenue` | 区间内无明细但存在汇总行 ⇒ 返回 `ErrBillingWindowArchived`, 不返回 0 |
| `/api/v1/admin/fx/revaluation` | 该月明细已归档 ⇒ 400 明确报错, 不给"无需重估"的假零值 |
| `/api/v1/admin/fx/reconciliation` | 窗口内明细已归档 ⇒ 400 明确报错 |

理由一致: **假零值和假差额比报错危险**。对账/重估给出的 0 会被读成"这段时间没问题",
而人一旦据此把差额定性成"已解释", 错误结论就留在了对账单上。

## 5. 配置与运维

| 设置键 | 默认 | 范围 | 说明 |
| --- | --- | --- | --- |
| `billing_retention_days` | 180 | 1–3650 | 四张表留存天数 |
| `billing_archive_enabled` | true | 布尔 | 删除前是否归档 |

- 后台任务 `billing_retention`, 24 小时一次, **启动即跑**; 单轮 10 分钟超时, 内部逐日分批事务;
- 设置改完经 `task.ApplySetting` 立即热更新(已登记在 `settingTasks`);
- 运维页 `/api/v1/ops/billing-lifecycle/{run,list}`(run 需二次验证 step-up), 设置页新增
  「资金表留存」卡片, 展示五张表当前行数 + 归档文件清单 + 立即清理按钮;
- 归档文件自身受 `archive_retention_days` 管理(前缀 `billing-`), 最近 3 份始终保留。

## 6. 运维判读

- `archived` 与 `deleted_records` 不相等是正常的(归档受开关控制, 且归档上界与删除上界一致时
  应相等); 明显不等时看 `retained_records`(被申诉保护的行数)与 `truncated`(是否被单轮上限截断)。
- `truncated = true` 表示本轮达到 `billingRetentionMaxRecordsPerRun` 上限, 下一轮会继续, 无需干预。
- `archive_error` 非空 ⇒ 本轮一条都没删, 先查归档目录可写性与磁盘余量。

## 7. 明确不做: 租约式选主

多实例部署的 leader election(库级租约 / 单写者选主)**不在本次范围内**, 本次只解决
"单实例下四张表无限膨胀 + 清理不能算错钱"。多实例遗留问题(`internal/op` 与
`internal/server/handlers` 的进程级缓存、内存限流桶、进程内定时器)按现状保留,
详见 `docs/feature-analysis-v1.10.13.md` §2.2。


## 8. 留存固化事务为什么在开头就取写锁

按天固化的事务是典型的"先读后写": 读当天明细 → 固化汇总 → 删明细。首条语句是 `SELECT`,
只拿读快照; 随后那条写语句要把读快照升级成写锁, 而 WAL 下这次升级在有人并发提交时**必失败** ——
报 `SQLITE_BUSY_SNAPSHOT`(扩展码 517), `busy_timeout` 对这条路径完全无效, 唯一出路是整轮重来。

线上与 CI 都复现过: 留存任务与日结/审核同时跑时, 单测 `-shuffle=31337` 下报
`create billing rollup user/1/20251017: database is locked (517)`。

修法是在事务第一条语句先取写锁(`db.AcquireWriteLock`), 让并发方在本事务的写锁上按
`busy_timeout` 排队, 而不是让自己的读-写升级去撞 517。这条修法之所以安全, 是因为该事务
**一天一批**, 天然短小, 不会长占写锁; 也正因为如此, 长事务(整轮归档)不取锁, 只读事务永不取锁。

回归用例 `TestBillingRetentionSurvivesConcurrentWriter` 在留存事务运行期间持续提交新明细,
断言留存能跑完且终身口径不变; 把取锁那行删掉立即变红(`SQLITE_BUSY`)。

**计量**: 2000 次取锁 = 0 WAL 字节、0 行写入 —— 锚点表的空更新只用来拿锁, 不改任何数据;
`TestAcquireWriteLockAnchorTableStaysEmpty` 守住这条。



## 9. 并发抖动复检: `internal/op` 剩余短事务的逐个判定

§8 只讲了留存固化这一处为什么要提前取写锁。那份修法落地后, 还有一个更要紧的问题没人回答:
**`internal/op` 里剩下的那些显式事务, 是不是也都踩在同一个坑上?** 本节是对这个问题的穷举复检,
结论是**不需要新增任何一处锁** —— 但"不需要新增"本身是结论, 得能被下一个审计者独立复核。

### 9.1 结论先行

用 AST 穷举(不是行级 grep, 见 §9.4)`internal/op` 非测试文件, 共 **40 处**
GORM 事务（含 `channelModelTransaction` 包装的三处渠道事务），按"事务首条语句 + 事务长短"分成四类:

| 类别 | 处数 | 判据 | 动作 |
| --- | --- | --- | --- |
| A. 已在开头取写锁 | 18 | 首条是 `db.AcquireWriteLock` 的锚点 UPDATE | 已正确, 维持 |
| B. 首条即写 | 20 | 事务第一条执行语句就是 INSERT/UPDATE/DELETE | **不需要**加锁, 也不许加 |
| C. 纯只读事务 | 1 | 事务内一条写语句都没有 | **绝不能**加锁 |
| D. 长事务 | 1 | 事务体是整轮归档/整份导入, 秒级到分钟级 | **绝不能**加锁 |

合计 40 = 18 + 20 + 1 + 1。**没有一处落在"短小的读后写"这一唯一需要加锁的类别里。**
这句话的含义要说清: 不是"我扫了一遍没发现", 而是"四类互斥、每类都有明确判据与全量名单,
名单总数 40 与 AST 计数相等", 所以"漏掉一处"在这里是可证伪的。

一句话记住本节判据: **只有"事务很短 + 首条是读 + 后面要写"三者同时成立才需要提前取锁**;
三者缺一 —— 首条是写(写锁已经到手)、只读(永远不写)、长(提前拿锁等于把全库写入口堵死)。

### 9.2 每一类为什么这样判

**A 类(18 处, 已加锁): 首条本身就是取锁。**
`db.AcquireWriteLock(tx)` 展开成的是 `UPDATE db_write_anchor SET id = id WHERE 0` —— 一条**零命中**
的 UPDATE。它不改任何行、不写任何页(WAL 字节数为 0), 但 SQLite 在评估谓词之前就先开写事务,
所以它一定去拿写锁, 拿不到就按 `busy_timeout` 排队。因此这 18 处事务的"第一条执行语句"是写,
B 类的判据对它们同样成立 —— 这正是 §9.6 里护栏只写一条断言就能覆盖 18 处的原因。它们的首条业务语句
是 SELECT, 但读到的是**已经持有写锁之后**的快照, 不存在读-写升级窗口。

**B 类(20 处, 首条即写): 不需要加锁, 加了是纯浪费; 也不许"为了整齐"补一行。**
SQLite 的写锁是"第一条写语句拿到手并**一直持有到事务结束**"。因此事务首条是 INSERT/UPDATE/DELETE 时,
它在第一毫秒就已经拿到写锁, 后续 SELECT 读的是自己持有的写事务快照, 升级窗口在物理上不存在。
此时再补一句 `AcquireWriteLock` 只是把已经到手的锁再要一次, 换不来正确性, 只多一次往返
与一次谓词评估。这类里有两种写法值得单独点名:

- **把判定压进写语句的谓词**: `billing.go:57 BillingReserve` 的首条是
  `UPDATE users SET frozen = frozen + ? WHERE id = ? AND balance - frozen >= ?`
  —— 余额够不够是**写语句自己的谓词**, 不是先 SELECT 再算。够就扣、不够就 0 行、事务失败。
  把"检查"与"扣减"压进同一条语句, 是抗并发的最优形态。
- **借业务表拿锁(自赋值空更新)**: `billing.go:117/251`、`billing_recovery.go:101`、`user.go:298`、`withdrawal.go:270`
  的首条是 `UPDATE ... SET x = x WHERE id = ?` 或把 `settled` 置真 —— 命中 0 行或命中但不改值,
  **目的纯粹是先拿锁再读**。这与 A 类的锚点 UPDATE 是同一手法, 区别只是借用了业务表。
  它们已经是"不用 `AcquireWriteLock` 的 `AcquireWriteLock`", 更不需要额外再加。
  `billing.go:117/251` 的代码注释里已写明"事务首个语句即写, 拿到写锁再读"。

**C 类(1 处, 只读): 绝不能加锁 —— 加了是把"偶发一条失败"换成"所有写入排队"。**
`settlement.go:217 settlementAggregateDay` 的事务体只有 `settlementAggregateSnapshot`
(按日 GROUP BY owner_id 的全表聚合 + 一次退款聚合), 一条写都没有。给它加锁的后果是实测过的:
一个持写锁 1 秒的只读事务会把普通写入从 0ms 推迟到 **1032ms**(计量见 `internal/db/txlock.go` 顶部注释)。
这与"不用全局 `_txlock=immediate`"是同一个理由 —— 只读事务不该进入写锁队列。
它的正确形态是"读到一份自洽快照然后不管了", 允许与写入并发。

**D 类(1 处, 长事务): 绝不能加锁, 理由见 §9.5。**

### 9.3 全量清单(40 处: 文件:行 → 函数 → 首条执行语句 → 判定)

首条语句按**实际执行顺序**记录(见 §9.4 的三个坑): 这张表是 AST 解析 + 递归展开包内 helper 之后得到的,
不是按源码行号从上往下读出来的。

**A 类 — 已在开头取写锁(18 处)**

| # | 位置 | 函数 | 取锁行 | 首条业务语句 |
| --- | --- | --- | --- | --- |
| 1 | `internal/op/billing_retention.go:251` | `sealAndDeleteBillingDay` | :256 | `Find` 当日明细 |
| 2 | `internal/op/channel_autoadd.go:314` | `AutoAddChannelModels` | :316 | `Find` 渠道模型 |
| 3 | `internal/op/dispute.go:147` | `DisputeReview` | :149 | `First` 争议单 |
| 4 | `internal/op/group.go:199` | `GroupUpdate` | :201 | `Updates` 分组 —— **首条本是写, 见 §9.5 决策二** |
| 5 | `internal/op/group.go:368` | `GroupAddItems` | :370 | `Find` 现有分组项 |
| 6 | `internal/op/recharge.go:421` | `RechargeNotify` | :423 | `Take` 订单 |
| 7 | `internal/op/recharge.go:643` | `RechargeManualComplete` | :643 | `Take` 订单 |
| 8 | `internal/op/redemption.go:200` | `RedemptionRedeem` | :202 | `Take` 兑换码 |
| 9 | `internal/op/settlement.go:249` | `settlementGenerateDayOnce` | :252 | `Find` 当日结算单 |
| 10 | `internal/op/webhook.go:173` | `WebhookCreate` | :175 | `Count` 配额 |
| 11 | `internal/op/webhook.go:304` | `WebhookUpdate` | :306 | `First` webhook |
| 12 | `internal/op/webhook.go:398` | `WebhookTest` | :400 | `First` webhook |
| 13 | `internal/op/webhook.go:532` | `WebhookReplayDelivery` | :534 | `First` webhook |
| 14 | `internal/op/webhook_dispatch.go:263` | `webhookClaimDelivery` | :265 | `First` 投递行 |
| 15 | `internal/op/withdrawal.go:338` | `WithdrawReview` | :338 | `Take` 提现单 |
| 16 | `internal/op/withdrawal.go:389` | `withdrawUnfreeze` | :389 | `Take` 提现单 |
| 17 | `internal/op/withdrawal.go:450` | `WithdrawMarkPaid` | :450 | `Take` 提现单 |
| 18 | `internal/op/withdrawal.go:625` | `WithdrawRollback` | :625 | `Take` 提现单 |

**B 类 — 首条即写, 不需要也不允许加锁(20 处)**

| # | 位置 | 函数 | 首条执行语句 | 备注 |
| --- | --- | --- | --- | --- |
| 19 | `internal/op/billing.go:57` | `BillingReserve` | `UPDATE users SET frozen = frozen + ? WHERE id = ? AND balance - frozen >= ?` | 余额判定压在写语句谓词里 |
| 20 | `internal/op/billing.go:117` | `billingSettleApply` | `Update` 置 `settled` | 注释已写明"首条即写" |
| 21 | `internal/op/billing.go:251` | `billingRelease` | `Update` 置 `settled` | 同上 |
| 22 | `internal/op/billing_recovery.go:101` | `BillingSettle` | `UPDATE balance_reservations SET settled = settled WHERE id = ?` | 自赋值空更新, 只为拿锁 |
| 23 | `internal/op/channel.go:173` | `ChannelCreate` | `Create(&channel)` | 26 条语句, 但首条即写 |
| 24 | `internal/op/channel.go:253` | `ChannelUpdate` | `Updates(&model.Channel{...})` | 24 条语句, 但首条即写 |
| 25 | `internal/op/channel.go:682` | `ChannelDel` | `Update` 清空 `active_item_id` | 经 `clearActiveItems`, 见 §9.4 坑 1 |
| 26 | `internal/op/notification.go:66` | `notificationCreate` | `notificationCreateTx` 先 `Create(notification)` | 目标预读在**事务外**, 见下 |
| 27 | `internal/op/redemption.go:143` | `RedemptionBatchCreate` | `Create(&batch)` | 批量发码, 首条即写 |
| 28 | `internal/op/ticket.go:200` | `TicketReply` | `Update` 按"未关闭"条件顶 `updated_at` | 守卫写在前, 消息写在后 |
| 29 | `internal/op/ticket.go:297` | `TicketAssign` | `Update("assigned_to", ...)` | 单语句事务 |
| 30 | `internal/op/ticket.go:349` | `TicketAutoCloseResolved` | `Updates` 条件更新 | 单语句事务 |
| 31 | `internal/op/ticket.go:398` | `setTicketStatus` | `Updates` 带旧状态做条件 | 单语句事务 |
| 32 | `internal/op/user.go:298` | `userSetBalanceOnce` | `UPDATE users SET balance = balance WHERE id = ?` | 自赋值空更新, 只为拿锁 |
| 33 | `internal/op/user.go:422` | `UserDelete` | `Delete` webhook_delivery | `HasTable` 刻意留在事务外 |
| 34 | `internal/op/wallet.go:43` | `WalletCredit` | `Updates` 加余额(在 `walletCreditTx`) | 函数体是一行调用, 见 §9.4 坑 3 |
| 35 | `internal/op/webhook.go:379` | `WebhookDelete` | `Delete` webhook | 两条 Delete |
| 36 | `internal/op/withdrawal.go:270` | `WithdrawApply` | `UPDATE users SET total_revenue = total_revenue WHERE id = ?` | 自赋值空更新, 只为拿锁 |
| 37 | `internal/op/balance_alert.go:113` | `BalanceAlertCheck` | 条件 `Update` 抢占 `last_triggered_at` | 事务同时写入历史、站内通知和 Webhook 投递行 |
| 38 | `internal/op/model_catalog.go` | `replaceModelCatalog` | `Delete` 清除旧的 models.dev 能力条目 | 模型参考资料与渠道上下文在同一事务更新 |

> `notification.go:66` 顺带记录一条已经做对的模式: `notificationCreate` 把 webhook 目标列表的
> **只读预读放在事务外**(`webhookEnqueueTargets`), 事务里只剩两条 INSERT。这是"短小的读后写"的
> 另一种正解 —— 不需要锁, 只要把读挪出去。它也是 §9.5 里"backup 导入该不该上移校验"的对照:
> 当读很便宜且不依赖写事务快照时, 挪出去是免费的; 当读必须与写同快照时, 挪出去就是错。

**C 类 — 纯只读, 绝不能加锁(1 处)**

| # | 位置 | 函数 | 事务内语句 | 判定 |
| --- | --- | --- | --- | --- |
| 39 | `internal/op/settlement.go:217` | `settlementAggregateDay` | `Scan` + 2 × `Find`(全表按日聚合) | 零写语句; 加锁会把全库写入拖到秒级 |

**D 类 — 长事务, 绝不能加锁(1 处)**

| # | 位置 | 函数 | 事务体 | 判定 |
| --- | --- | --- | --- | --- |
| 40 | `internal/op/backup.go:121` | `DBImportIncremental` | 校验 API Key 后逐表 `createDoNothing`/`createUpsertAll`(13 张表, 分批 2000 行) | 见 §9.5 决策一 |

**范围外但已归类(供下次审计对齐)**: `internal/db/secretmigrate.go:162` 也有一处 `.Transaction`,
首条即写, 属 B 类; 不在 `internal/op` 也不在本次委托范围, 仅记录不动作。
`internal/server` 与 `cmd` 下**没有**任何 `.Transaction(`; 全仓也没有 `db.Begin()`、
`Session{NewDB: true}`、手工改 `Statement.ConnPool` 这类绕过显式事务的写法 —— 也就是说
"显式事务"这个面已被这 40 处完整覆盖。

### 9.4 三个静态分析的坑(下一个审计者一定会踩)

这张表不能靠"从上往下读源码找第一条语句"得到。我最初两版扫描器就错在这里:

1. **未执行的 builder 不是语句。** `channel.go:682 ChannelDel` 走的 `clearActiveItems`(`channel.go:1596`)
   里有一句 `itemIDs := tx.Model(&model.GroupItem{}).Select("id").Where(...)`。它看着像第一条语句,
   但 `Model/Select/Where` 只是往 `Statement` 上挂 `BuildClauses`, **不发 SQL**;
   要到被当作子查询嵌入下一条语句时才求值。真正的首条是紧随其后的
   `tx.Model(&model.Group{}).Where("active_item_id IN (?)", itemIDs).Update("active_item_id", 0)`。
   结论: `ChannelDel` 是 B 类, 不是"读后写"。
2. **只跑 `Statement.Parse` 的 helper 是零 SQL 的。** `channel.go:173 ChannelCreate` 与
   `channel.go:253 ChannelUpdate` 都会先调 `channelConfigUpdateColumns`(`:344`)/
   `channelExplicitConfigColumns`(`:376`) 算要写的列集合; 这两个函数内部只有
   `gorm.Statement.Parse`(schema 反射), **一条 SQL 都不发**。把它们当"首条语句"就会把两个
   B 类事务误判成"读后写"。
3. **函数体是单行调用的, 首条语句在下一层。** `wallet.go:43 WalletCredit` 的事务体只有一行
   `return walletCreditTx(tx, ...)`, 真正首条是 `wallet.go:15` 里的
   `tx.Model(&model.User{}).Updates(...)`。只看事务字面量会把这种"委托给 helper"的事务读成空事务。

所以 40 处清单是这么得到的: 用 Go `ast` 解析 `internal/op` 全部非测试文件, 定位每个以
`*ast.FuncLit` 传给 `.Transaction(` 或 `channelModelTransaction(` 的调用点; 对函数体按**调用点顺序**记录每个 GORM finisher
(`Create/First/Take/Find/Scan/Count/Update/Updates/UpdateColumn(s)/Delete/Exec/Save/Pluck/Rows/Row`)
或 `db.AcquireWriteLock`; 遇到包内 helper 调用则**递归展开**并把语句插在调用点位置; 排除内建函数
(`len/make/cap/append/new/panic/recover/print/println/delete/copy/close`)与非 GORM 的 `SelectorExpr`。
去掉递归展开, 上表至少 4 处会判错(`ChannelDel`、`ChannelCreate`、`ChannelUpdate`、`WalletCredit`);
只用行级 grep 更差: `Where(...).Update(...)` 这种跨行链式调用永远匹配不到"同一行的 SELECT"。

### 9.5 两条明确的决策

**决策一: `backup.go:121 DBImportIncremental` 保持不加锁, 并且不把 `validateImportedAPIKeys`
的 `Find` 挪出事务(即不动 `apikey_import.go:13`)。**

它是 D 类唯一一处, 事务体是"校验 API Key 归属 → 13 张表逐表导入(每批 2000 行)"。它与 §8 的留存事务
形状上很像(都是先读后写), 但有一条决定性差异: **时长**。留存是"一天一批、几毫秒"的短事务;
导入是"整份备份、秒级到分钟级"的长事务。提前取写锁 = 在整份导入期间堵住全库写入口, 转发路径的每一次
余额冻结/统计落库都要排队等它。这与 `_txlock=immediate` 的错误完全同构: 用一个不存在的风险
(大规模导入期间的并发 517)去换一个确定的代价(导入期间全库写入停摆)。而 517 在这里本来就极难触发:
导入是低频运维动作, 通常伴随停机窗口或至少是低峰; 即便真撞上, 长事务外层本来就有"偶发冲突 + 重试"
的既有策略, 一次重来比堵死全库便宜得多。

"那把校验挪到事务外, 事务首条就变成写了, 不就不需要锁了吗?" —— 不划算, 而且会损失语义。
`validateImportedAPIKeys` 读的是**导入前该库 api_keys 的现状**(按主键查归属冲突、按 `key_hash`
查重复凭据), 它的判定必须与后续 `createDoNothing` 落在**同一份快照**上。挪到事务外, 校验与
实际写入之间就多了一个窗口: 期间若有并发写改了 `api_keys`, 校验通过的行可能已经不成立。
**为了把一个不存在的 517 风险挪走, 去换掉一条真实存在的"校验-写入同快照"保证, 不划算。**
结论: 保持现状, 一行不改。

顺带把"长事务的正确做法"写死: 长事务不占写锁, 遇冲突靠**外层重试**; 想彻底消除冲突就该把它**拆短**
(按表/按天分批, 每批一个短事务), 而不是给它上锁。拆短会动 3 个以上文件、并影响导入的原子性语义,
不在本次范围内 —— 需要的话单开任务。

**决策二: `group.go:199 GroupUpdate` 的取锁是冗余的, 但刻意保留, 不加也不删。**

AST 显示它的首条业务语句本来就是 `Updates`(`:206`), 写锁在事务开局就已到手,
`:201-202` 的 `AcquireWriteLock` 是"把已经到手的锁再要一次"。判定:
**冗余但无害** —— 代价是一次零命中 UPDATE, 锚点表 0 行写入、0 WAL 字节, 已被
`TestAcquireWriteLockAnchorTableStaysEmpty` 守住。之所以刻意不动: 删掉它要重新论证"这一处的读序列
确实不含先行 SELECT"(而它的顺序本来就是写→读→写), 收益是省一次空更新, 风险是把一处已经正确的代码
改错。**审计的价值在于证明现状已经正确, 而不是硬凑出几处改动。** 这一类"已知冗余"以后还会出现
(任何在 `AcquireWriteLock` 落地之后才改成"首条即写"的事务都长这样), 所以这条决策要当惯例用:
**冗余的取锁不删, 只记录。**

### 9.6 与已加锁 18 处的关系, 以及护栏怎么守住全部 40 处

护栏用例 `internal/op/txlock_audit_test.go` 用**一条**核心断言覆盖上表:
**"op 里每一处显式事务, 事务内执行的第一条语句必须要么本身是写(`INSERT/UPDATE/DELETE`),
要么是 `db.AcquireWriteLock`。"** 它自动覆盖 A 类 18 处(A 类首条正是零命中的锚点 UPDATE),
也自动覆盖 B 类 18 处 —— 因此**不需要**为"已加锁的 18 处"另写 18 条用例, 那是重复劳动。
C 类与 D 类**应该**违反这条, 所以白名单是**逐条显式列出**的两个例外,
不用"函数名含 backup 就跳过"这类正则 —— 正则白名单会随重命名静默失效, 而那正好是它该报警的时候。
白名单条目带自己的**反向校验**, 不是免死金牌:

- `settlementAggregateDay`(C 类): 断言事务内**零**写语句 —— 给它加锁即红;
- `DBImportIncremental`(D 类): 断言事务内**没有取锁** —— 给长事务加锁即红。

### 9.7 为什么护栏是静态 AST 扫描, 而不是运行时 GORM 回调

运行时方案(在 GORM 回调里记录"事务内第一条 SQL")先做了一版, 被三个实测结果否掉:

1. **`Before` 锚点里 `Statement.SQL` 是空串**。`Before("gorm:query")` / `Before("*")` 触发时
   BuildClauses 还没跑, 拿不到语句文本 —— 护栏会变成**静默空转**, 比没有护栏更危险(它给的是假绿)。
   终端锚点要用 `After("gorm:query")` 这一类;
2. **`ConnPool` 是不是 `*sql.Tx` 判不出"在不在 op 自己的显式事务里"**。GORM 默认给
   create/update/delete 各包一层隐式单语句事务, 于是单条 `db.Create()` 的连接也是 `*sql.Tx`,
   结论会混进大量"隐式事务"。要绕开它就得查 GORM 内部 key(`gorm:started_transaction`)或按
   `*sql.Tx` 指针分组再靠"组内含 SELECT 或 ≥2 条语句"反推 —— 前者随版本脆, 后者要处理异步通知；
3. **覆盖率只有被驱动过的那几个 op**。运行时方案要每个 driver 手工准备 fixture、关掉记录窗口、
   drain 异步通知(Ticket*/NotificationCreate/RedemptionRedeem/WithdrawMarkPaid 都会派生),
   而静态扫描一次覆盖全部 40 处, 且没有"某个 op 忘了加 driver"这种缺口。

静态扫描必须处理三件不显然的事, 全部对应 §9.4 的三个坑, 并且都实测过:

- 只认**terminal 方法**(`Find/First/Take/Scan/Count/Pluck/Create/Update/Updates/Delete/Exec/Save/Row/Rows`),
  `Model/Select/Where/Order/Limit/Preload` 一律不算 —— 坑 1 的原始形态;
- 按**执行顺序**遍历语句节点, `if` 的 Init 先于 Body(`if err := db.AcquireWriteLock(tx); err != nil`
  正是最常见的取锁写法, 只看 Body 会漏)、`return` 的表达式也算语句、`.Error` 这种选择子要穿透到接收者链;
- 递归展开包内 helper(**只展开参数里真的用过 `tx` 的函数**, 深度上限 4 层): 事务体只有一行委托时
  (`return walletCreditTx(tx, ...)`), 首条语句在下一层; 去掉展开, 40 处里至少 4 处会判错 ——
  其中 `ChannelDel`/`ChannelCreate`/`ChannelUpdate`/`WalletCredit` 会被读成"空事务"而漏检。

失败信息指名道姓到"哪一处", 形如
`withdrawal.go:389 withdrawUnfreeze: 事务内首条语句是读(Take, 见第 390 行), 期望写: ...`
—— 只报"某事务首条不是写"等于把定位工作又推给下一个人。

护栏自己也有兜底断言, 否则会在"扫描器坏了"时假绿:

- 处数必须等于 **38**(事务被增删时必须重新判定并同步本节的清单);
- 四类计数必须等于 **18 / 18 / 1 / 1**(少报一处 = 递归展开或 terminal 名单坏了);
- 事务体内一条 terminal 都找不到**直接报错**(那只能是"委托没展开", 不能当"空事务, 安全")。

反向验证(每条都真跑过, 不是设想):

| 破坏方式 | 护栏反应 |
| --- | --- |
| 把 `withdrawUnfreeze` 的取锁挪到 `Take` 之后 | 红: `withdrawal.go:389 withdrawUnfreeze: 事务内首条语句是读(Take, ...)` |
| 给只读的 `settlementAggregateDay` 加锁 | 红: `settlement.go:217 settlementAggregateDay: 只读事务里出现了写语句(AcquireWriteLock, 第 218 行)` |
| 给长事务 `DBImportIncremental` 加锁 | 红: `backup.go:121 DBImportIncremental: 长事务里出现了取锁(AcquireWriteLock, 第 122 行)` |
