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

