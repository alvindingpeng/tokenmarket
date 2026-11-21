# 留存期之外仍可「按模型」—— billing_rollups 的第 6 个维度

> 范围: 非 SaaS 主线版本。本文记录"给日汇总表补上 model 维度, 让超出留存期(默认 180 天)的
> 按模型用量与金额仍可查"这件事的完整设计与落地细节。
> 前置文档: [billing-retention-180d.md](billing-retention-180d.md)(四张资金表留存与日汇总的整体设计)。
> 本文只覆盖 model 维度这一段, 不改动前五个维度的语义。

## 1. 为什么需要第 6 个维度

留存机制把明细按天固化进 `billing_rollups` 再删除。那张表原本只有五个维度:
user / owner / channel / apikey / platform。**没有 model**。

后果是"总额与拆分不对称":

| 读口 | 明细被清理后 |
| --- | --- |
| 概览 `total_cost` / 曲线 / 账单页 | 仍正确 —— 由 user 维度汇总补齐 |
| 统计页「按模型」那一栏 | **整段消失** —— 只能读 `billing_records` |

于是用户看到"这个月的总金额有数, 但按模型的拆分没了", 而且响应里没有任何字段能解释
这个缺口。这不是显示问题: 同一份数据在两个读口给出互相矛盾的事实, 用户对账时无法自洽。

补 model 维度是唯一不伪造数据的解法 —— 它把"按模型"这一层的寿命拉到与总额同一量级。

## 2. 维度的形状: 为什么模型名进唯一键, 而不是塞进 dimension_id

`billing_rollups` 的身份列原本是 `(dimension, dimension_id, day)`, 其中 `dimension_id` 是 `uint`。
模型名是字符串, 直接映射成数字只有两条路: 哈希(会碰撞, 且碰撞后无法还原)或查一张映射表
(多一次 JOIN、多一份需要维护的状态)。两者都会在"钱"的表上引入不必要的失真来源。

实际做法是**新增一列 `model_name`, 并把它并进唯一键**:

```sql
-- 新列: 其余五个维度的行恒为空串
ALTER TABLE "billing_rollups" ADD COLUMN model_name VARCHAR(128) NOT NULL DEFAULT '';

-- 唯一键: 3 列 -> 4 列
DROP INDEX IF EXISTS "idx_billing_rollup_key";
CREATE UNIQUE INDEX idx_billing_rollup_key
    ON "billing_rollups" (dimension, dimension_id, day, model_name);
```

三个要点:

1. **老维度的语义一点没变**。它们的 `model_name` 恒为空串, 所以 4 列唯一键在它们身上退化成
   原来的 3 列键 —— 不可能因为扩容而"多出一行", 也不需要回填任何数据。
2. **必须用空串而不是 NULL**。NULL 在唯一索引里互不冲突(SQLite/MySQL/Postgres 都把多个 NULL
   视作互不相同), 那样"同一维度同一天只能有一行"这条保护就失效了, 汇总可以被写成重复行,
   而重复行会让终身口径(用户累计消费、可提现额度)直接虚高。
3. **model 维度的 `dimension_id` 记的是付费方 user_id**, 模型名在 `model_name` 列。
   这样一个"某用户某天的按模型用量"仍是一次 `(dimension_id, day)` 的索引区间扫描 ——
   `idx_billing_rollup_dim_day` 直接可用, 不需要为 model 维度另建索引。

若反过来把模型名当 `dimension_id`: 用户维度的过滤就得扫全表(每个 model 行散落在所有用户里),
而"按模型"查询本来就要按用户切分, 这个取舍没有悬念。

> 未选"另开一张 `billing_rollup_models` 表"的理由: 汇总表的删除/固化/读取全都按
> `(维度, id, 日)` 走, 另开一张表意味着固化要写两处、删除要保两处一致、备份要覆盖两处 ——
> 收益只是"列少一点", 代价是引入一个新的不一致面。

## 3. 固化侧: 写第 6 行, 顺序不变

固化发生在 `sealAndDeleteBillingDay` 的同一事务里, 顺序仍是
**归档 → `file.Sync()` → 固化汇总 → 删明细**, 本次改动没有碰这个顺序。

`billingDayRollups` 在原来 5 次 `accumulate` 之外多了一次:

```go
// 只固化 settled 行, 与「按模型」读侧 status='settled' 的口径对齐
if record.UserID != 0 && record.Status == model.BillingStatusSettled && strings.TrimSpace(record.ModelName) != "" {
    accumulate(ref(model.BillingRollupModel, record.UserID, record.ModelName), record)
}
```

三个条件都不是随手加的:

- `UserID != 0`: model 维度挂在付费方身上, 没有付费方就没有"谁花了多少"可拆。
- `Status == settled`: 明细侧的模型页历来只算已结算(退款行不计入拆分, 由
  `TestUserStatsRefundKeepsBillingScope` 锁定)。若按"合计"固化, 明细一删, 同一区间会**突然多出**
  一笔退款额 —— 按模型费用在留存期边界上凭空跳变。也不能靠读侧减 `refunded_*` 修正: 那组列
  只有金额, 没有退款行的词元与笔数, 减完词元仍然偏大。
- `ModelName != ""`: 空串在"按模型"结果里是一个没有名字的桶, 既对不上任何模型, 又会把别的
  用量并进去(读侧按模型名分组, 空串自成一组)。

`sealBillingRollup` 的匹配条件同步加了 `model_name`:

```go
tx.Where("dimension = ? AND dimension_id = ? AND day = ? AND model_name = ?",
    row.Dimension, row.DimensionID, row.Day, row.ModelName).First(&existing)
```

少这一项, "同一用户同一天"的多个模型行会互相累加 —— 拆分照样失真(只是从"全丢"变成"张冠李戴")。
对其余五个维度, 空串让这条条件等价于原来的三列条件。

固化仍是"累加"而不是"覆盖": 同一天可能被固化多次(先因未驳回申诉保留, 驳回后再固化成可删),
累加才能让"汇总表里的数 = 已删明细之和"这个恒等式一直成立。

## 4. 读侧: 明细 ∪ model 汇总, 只在整天上相加

`UserStatsModelBreakdown` 从"只读明细"改成"明细 ∪ model 汇总", 遵循与
`userStatsBillingAggregate` 完全相同的三条不变量:

| 不变量 | 原因 |
| --- | --- |
| 汇总只在**整天**上并入(`userStatsRollupDayRange`) | 窗口首尾若不是自然日边界, 整天的汇总行会把窗口外的半天也带进来 |
| 明细与汇总**不会含同一笔**明细, 所以是加法不是去重 | 某天被删之前, "那天的可删明细"已整日固化; 受申诉保护的明细不进汇总 |
| 逐日桶相加,**绝不按比例摊** | 半天窗口的缺失宁可少算, 也不能把整天汇总按时间比例摊成假数字 |

合并是在 Go 侧做的(两侧各一个累加器, 最后统一产出), 不是 SQL 的 `UNION ALL`: 汇总侧一天最多
几个模型行, 分摊到一个月也就几十行, 而跨方言的 `UNION` + 分组在 SQLite/MySQL/Postgres 上
写法各不相同, 为这点数据引三条方言分支不值。

缓存命中率的分母在汇总侧要**还原**: 汇总表没有单独的 `input_token` 同名语义可用
(它有这一列, 但读侧统一走"四项之和"更省心), 命中率分母 = `input` 与 `cache_read` 之和,
两者都从汇总列的对应列直接取, 不需要估算。

`APIKeyCostSumDB`(max_cost 判定的数据库兜底)同理并上 **apikey** 维度汇总
(该维度自迁移 43 就有, 只是这个读口此前没接):

```go
SELECT COALESCE(SUM(user_cost - refunded_user_cost), 0)
FROM billing_rollups WHERE dimension = 'apikey' AND dimension_id = ?
```

这里没有"半天"问题: 它问的是**全期累计**、不带时间窗, 所有汇总行都能直接计入。
"只计已结算"靠减法实现(`合计 - 其中退款`), 与明细侧 `status='settled'` 等价 ——
若不减, 明细段与汇总段会用两种不同口径相加, 接缝处凭空多出退款额。

## 5. 覆盖边界: 什么样的历史确实补不回来

这是本文最重要的一节, 因为"缺数据"和"数据是 0"在页面上长得一模一样, 而后者是谎报。

### 5.1 能补回来的

迁移 45 上线**之后**被清理的日子, 有 model 维度行 —— 按模型拆分与总额活得一样久。

### 5.2 补不回来的

迁移 45 上线**之前**就已固化的日子, 只有 user/owner/channel/apikey/platform 五行, **没有 model 行**。
那些天的明细当时就被删了, 谁也变不回来。具体有两大类:

1. 已经跑过留存清理的老数据(超过 180 天的那段);
2. 迁移窗口内刚好被清理的日子。

### 5.3 读口如何如实表达

`UserStatsModelCoverageStart` / 响应头 `X-Model-Coverage-Start` / 概览字段
`model_breakdown_coverage_start` 的语义是:

```
覆盖起点 = min(区间内最早明细日, 区间内最早的 model 维度汇总日)
```

**不是** min(user 维度汇总日): 迁移前固化的日子 user 维度有钱、model 维度没钱, 拿 user 的边界
当模型边界, 前端就会把"确实没有模型数据"的那段当"有数据但为 0"展示 —— 等于把缺失画成 0。

两种空值语义必须分清:

| 返回值 | 含义 | 前端应显示 |
| --- | --- | --- |
| 空串 | 区间内既无明细也无 model 汇总 | "暂无按模型数据" |
| 某个日期 | 该日期之前无按模型数据 | "此日期之前无按模型数据(总额仍可查)" |

禁止的两件事: 填 0、按比例把汇总金额摊到现存模型上。后者比缺数据更有害 —— 编出来的数字
会被用户拿去对账, 而它经不起任何核对。

## 6. 迁移

| 项 | 值 |
| --- | --- |
| 版本号 | **45** |
| 文件 | `internal/db/migrate/045_billing_rollup_model_dimension.go` |
| 类型 | `RegisterAfterAutoMigration`(建表之后跑) |
| 数据搬迁 | 无 |
| 回填 | 无 |

实际执行的 SQL(以 SQLite 为例):

```sql
ALTER TABLE "billing_rollups" ADD COLUMN model_name VARCHAR(128) NOT NULL DEFAULT '';
DROP INDEX IF EXISTS "idx_billing_rollup_key";
CREATE UNIQUE INDEX idx_billing_rollup_key
    ON "billing_rollups" (dimension, dimension_id, day, model_name);
```

索引**必须显式重建**: GORM 的 AutoMigrate 只会补"缺失的索引", 同名索引已存在就跳过 ——
3 列的旧唯一键会原样留着, 而它会让同一用户同一天的不同模型行互相覆盖。

### 6.1 幂等

```
第 1 次: added column billing_rollups.model_name
        rebuilt unique index idx_billing_rollup_key to include model_name
第 2 次: (无结构变更日志, 直接早退)
```

判定方式是"实时读列 + 实时读索引列集合": 新列已在、唯一键已含 `model_name` ⇒ 直接返回。
索引重建放在**单个事务**里 —— 中途失败留下"没有唯一索引"的表, 那段时间汇总写入可能产生
重复行, 而重复行会让终身口径虚高。

### 6.2 表不存在时安全跳过

全新库还没跑到建表那一步, 或不归本版本管的库: `HasTable` 为假则直接返回, 不报错、不建表。

## 7. 边界与不变的东西

- 前五个维度的语义、`refunded_*` 列、"退款口径靠减法还原"的设计: **全部不变**。
- 小时粒度的曲线仍然只读明细: 一天的汇总行无法还原该日内各小时的分布, 强行并进来只会
  伪造出"那天所有量都堆在某个小时"的桶。
- 单轮删除上限、逐日分批事务、`db.AcquireWriteLock` 先取写锁: 不变。
- 多实例并发固化仍依赖唯一索引兜底; 真正的多活改造(ON CONFLICT / 租约式选主)**不在本次范围**。
