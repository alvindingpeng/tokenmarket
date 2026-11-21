# 统计页按用户时区展示(两套日历并存)

> 目标: 用户所在时区与服务器时区不一致时, 统计页的「今天」不再是服务器的一天, 小时刻度也不再偏几小时;
> 同时**不动**任何资金/结算/留存口径。

## 1. 问题

改造前, 统计页的每一个时间概念都锚在 `time.Local`(服务器时区):

| 位置 | 代码 | 口径 |
| --- | --- | --- |
| 区间解析 | `internal/op/user_stats.go` `parseStatsDateRange` | `time.ParseInLocation("2006-01-02", ..., time.Local)` |
| 日/周/月桶 | `statsBucketExprOn` | 服务器挂钟日期前缀 |
| 小时桶 | `statsBucketExprOn`(hour) | 服务器挂钟 `HH:00` |
| 日汇总裁剪 | `userStatsRollupDayRange` → `dayStart`/`dayKey` | 服务器自然日 |
| 留存截止 | `billingRetentionCutoff` | 服务器自然日 00:00 |

前端把浏览器本地的 YYYY-MM-DD 直接发给后端(见 `web/src/components/modules/analytics/dates.ts`,
那里的注释已经明确拒绝用 `toISOString().slice(0,10)`, 因为那是 UTC 日期)。
于是当浏览器时区 ≠ 服务器时区时, 同一个字符串在两端指的不是同一段时间: 偏差 = |服务器偏移 − 用户偏移| 小时。

以服务器 +08、用户 +07 为例, 用户说的「11-21」被后端解释成服务器 11-21 00:00 → 11-22 00:00,
即用户本地 11-20 23:00 → 11-21 23:00: 少算用户 11-20 的最后 1 小时, 多算 11-21 的最后 1 小时;
而 X 轴上标着 `2026-11-21` 的那一格, 横跨的其实是用户本地的 11-20 23:00 → 11-21 23:00。
对 UTC−5 的用户, 这个漂移是 13 小时, 「今天」几乎整体错位到昨天。

## 2. 不能做的事(以及为什么)

看起来最自然的修法是把日/周/月桶也切到用户时区, 那样「今天」就是用户的一天。**这条路被否决**, 原因是
这套日历不只属于统计页:

1. `billing_rollups` 是按**服务器自然日**固化的。迁移 45 之后按模型维度也是。汇总行只代表整天,
   一天的合计无法还原它在用户日历里被切开的两半。
2. 明细与汇总的**可加性不变量**建立在「要么全在明细、要么全在汇总」上(见
   `internal/op/billing_retention.go` 与 `docs/billing-retention-180d.md`)。留存截止
   (`billingRetentionCutoff`) 按服务器自然日对齐, 一旦统计页按用户日历切, 同一个用户在统计页与
   账单页会对同一个区间给出不同的钱 —— 而对不上账是无法向用户解释的。
3. 结算与对账(`ReconcileDay`)共用同一套日键。

所以最终形态是**两套日历并存, 并如实披露**:

- **窗口边界**按用户时区解释(这是用户输入的那个「11-21」的本意),
- **日/周/月分组与留存**按服务器日历(与账单页/汇总表/结算同源),
- 两者的差异只在**偏移不同**时才提示; 名字不同但偏移相同(如 `Asia/Shanghai` 与 `Asia/Chongqing`、
  或都处于 +08 的部署)时挂钟一致, 不提示。

## 3. 实现要点

### 3.1 偏好持久化

`users.timezone`: 空串(老账号与未设置) = 跟随服务器; 否则是 IANA 名(长度 ≤ 64, 必须能被
`time.LoadLocation` 解析)。列由 AutoMigrate 建出, 不占迁移版本号 —— 这次没有数据搬迁,
空串天然就是「跟随服务器」的历史语义, 不需要回填。

接口 `POST /api/v1/user/timezone` 只改这一列; 改它**不需要**重新登录(与改用户名不同, 后者会换
token_version)。取值非法 → 400, 空串合法。

### 3.2 边界换算

`parseStatsDateRange(startDate, endDate string, loc *time.Location)` 用 `loc` 解析区间,
**返回前统一 `.In(time.Local)`**。为什么必须转回来: 下游的整日裁剪(`userStatsRollupDayRange`)、
日键生成(`dayKey`)与 SQL 比较全部建立在服务器挂钟上 —— 尤其是 SQLite, 它按挂钟文本比较时间串。
传一个别的时区进去会让边界与既有裁剪逻辑错位, 反而制造出「半个服务器日」的错算窗口。

### 3.3 小时标签

小时桶本身仍是服务器挂钟的整点切片(`statsBucketExprOn` 未改), 只在出结果后把**展示标签**
平移到用户时区: `statsHourLabelIn`。两个后果必须照实保留:

- 用户时区偏移若非整小时(`Asia/Kathmandu` +05:45), 标签会带分钟位(`09:45`), 前端刻度改为显示 `HH:MM`;
- DST 切换那天会出现两个桶落到同一标签(本地那一小时真的重复了)。不去合并、不四舍五入。

### 3.4 概览里的口径字段

`UserStatsOverview` 增加 5 个字段:`stats_timezone`、`stats_timezone_offset_minutes`、
`stats_timezone_source`(`user`/`server`)、`stats_calendar_timezone`、
`stats_calendar_timezone_offset_minutes`。前端只用它们做说明与刻度, 不重算任何统计;
两个 offset 相等 = 挂钟一致 = 不渲染提示。老后端不带这些字段时前端一律不提示(不编造)。

## 4. 未做的事

- **不改** `dayStart` / `dayKey` / `userStatsRollupDayRange` / `billingRetentionCutoff`;
- **不**为日/周/月桶维护第二套(用户日历的)汇总表 —— 那需要另一张表与另一套留存/对账口径,
  且会让「统计页金额 = 账单页金额」这条硬约束失效;
- **不**做每请求的 `tz` 查询参数: 偏好存账号侧, 跨设备一致, 也不会让链接里带上时区导致缓存碎片化。

## 5. 验证清单

- `internal/op/user_stats_timezone_test.go`: 边界换算、核心包含/排除断言、小时标签、5 个口径字段、
  汇总表在非整日对齐时不被拉进来、非法偏好回落。
- `web/src/components/modules/analytics/timezone.test.ts`: 三种提示分支、非整点刻度、`-0` 分钟。
- `web/src/components/modules/setting/timezoneOptions.test.ts`: 选项去重与顺序、非法名判定。
