# 优化待办: 按证据排序(2026-11-03)

> 本文只记**已在生产或代码里核实过**的问题, 每条附证据与影响。
> 与 [未实施功能清单](./backlog-unimplemented.md) 的分工: 那份记"有设计稿但没做",
> 这份记"**做了但没生效 / 没人清理 / 缺一条链路**"——即优化项而非新功能。

---

## A. 已修

### A1. 出口 ID 从未落库 → 按出口归因的统计永久为空 (v1.5.16)

**证据**: `relay_request_attempts.egress_id` 全表 **5949 行全为 0**, 而同一时刻已有 7 个渠道配了出口
且正在承载流量(ch=34 当天 254 次成功)。

**根因**: 轮次开始时 `setRoundEgress` 把出口 ID 写进 `r.roundTiming.EgressID`;
轮次结束后 `setRoundTiming(roundTiming.Snapshot())` **整体覆盖** `roundTiming`,
而 `rhttp.ConnTiming.Snapshot()` 只含 DNS/建连/TLS/首字节, 根本不知道出口是什么 —— 于是 ID 被抹回 0。

**影响**: 「运维 → 延迟归因」按出口分组永远无数据; 出口健康看板的"是否真的在用"无从验证;
出口池择优完全不可观测 —— 配了几条线路, 看不出流量走了哪条。

**修法**: `setRoundTiming` 覆盖前回填 `timing.EgressID = r.roundEgressID`。
回归测试 `TestEgressIDSurvivesTimingSnapshot` 复现原顺序(先选出口后登记快照), 已做变异校验。

**上线验证(v1.5.16 于 15:18 部署)**: ch=34 在 15:18 之前的 **612 次尝试 egress_id 全为 0**;
15:18 之后 **133 次连续尝试全部记为 egress_id=9**(该渠道唯一的 direct 出口)。
归因链路就此打通 —— 出口耗时看板终于有数据(平均 13.7 s, 133/133 成功)。

### A2. 停服硬关连接 → 每次部署掐断在途流式回答 (v1.5.16)

**证据**: `server.Close()` 直接调 `httpSrv.Close()`, 立即断开所有连接、不等在途请求;
`Shutdown()` 在全库从未被调用。部署走 `systemctl stop`(SIGTERM), 今天重启 4 次;
线上 98.3% 是流式请求, 一条 SSE 流可挂几分钟。

**影响**: 每次部署都打断正在生成的回答。症状是"回答突然中断", 而**服务端日志里没有任何错误**,
所以一直没被发现。第一次带优雅关闭的部署(15:57)打出 `graceful shutdown timed out after 30s`,
说明当时确有请求在途中 —— 换成旧版本就是几条被掐断的流。

**修法**: `Close()` 改 `Shutdown(ctx)`, 上限 30 秒(远小于 systemd 的 90 秒 `TimeoutStopSec`);
超时才记 WARN 并硬关。`TestCloseWaitsForInflightRequest` 用真实慢处理器 + 真实 `Close()` 断言响应完整,
`TestCloseStopsAcceptingNewRequests` 断言停服后新连接被拒(优雅关闭不能退化成"一直开着")。

### A3. 模型单价可存负数 → 调用反而加钱 (v1.5.17)

**证据**: `LLMPrice.Cost` 是纯乘法再求和, 无下界钳制; 而 `createLLM`/`updateLLM` 只校验名称非空,
媒体模型早有 `MediaPrice.Validate`、文本模型没有。前端 `parseFloat(x) || 0` 还会把 `abc` 静默变成 0。

**影响**: 负价让费用为负, 即调用反而给用户加钱; 上游可据此无上限充值。

**修法**: `LLMPrice.Validate`(拒 NaN/Inf、负值、超量级)在落库前拦截, 回 400 并记 WARN;
前端改 `Number()` + 显式区间校验, `1.2.3` 不再被当成功。四个价格框加 `min="0"`。

### A4. 前端失败静默 / 危险操作一击即发 / 英文残留中文 (v1.5.17)

- 78/79 处 `useMutation` 无 hook 级 `onError`, 失败时界面什么都不说 → 已逐点补齐。
- 四个列表页只看 `data` 不看 `isError`, 失败时列表为空、`VirtualizedGrid` 在 `rowCount === 0` 直接
  `return null` → 整页纯空白(连"暂无数据"都没有) → 新增 `ListErrorState` 并接线。
- 删除限流策略(误点即解除全局限流)、清空日志、提现通过/回滚 均为单击即执行 → 改就地两步确认。
- `en.json` 计费四段 76 条值为中文(键位测试只检查"有值"不检查"是英文") → 全部翻译,
  并加一条"英文不得含 CJK"的用例; `zh_hant` 同 76 条为简体未转换 → 已转。

---

## B. 待修: 有明确证据的缺陷

### ~~B1. 出口运行时成绩不持久化, 每次重启重新学习~~ (已修 v1.5.18)

**证据**(改动前): `internal/relay/egress.go` 的 `egressStates` 是进程内 map, 全库无对应表;
当时服务每天重启数次(每次部署一次)。

**影响**(比原描述更严重): 不只是"头几次可能选到较差的"。冷出口按 `neutral`(已观测出口的平均值)
参与打分, 而**全部出口都在冷态时 neutral = 0, 全部并列** —— 排序完全由 `weight` 决定。
也就是说: 重启后第一批请求,**权重最高的那条线路被无条件优先选中, 无论它是否还通**。
线上 7 个出口的 weight 全是 0, 并列时由遍历顺序决定, 选中的是谁基本是随机的。

**修法**: 新增 `egress_metrics` 表(EMA 成绩 + 冷却截止), 复用既有的脏标记 + 5 秒批量落库;
`RestorePersist` 启动时恢复, 并清掉已删除出口的残留行。冷却也一并恢复。

**实现要点(踩到的坑)**:
- 顺序: 采样在 `egressMu` 内, 但 `markEgressDirty` 必须放到**放锁之后** —— 落库协程的取锁顺序是
  `persistDirtyMu` → `egressMu`, 锁内再取 `persistDirtyMu` 会成环。
- 写失败要重新标记脏, 否则这批成绩永远不会重试, 重启后又回到零样本。
- 已过期的冷却不进状态, 否则每次落库都会把无意义的行写回库。
- 清理残留行时 `len(existing) == 0` 要单独处理: gorm 不接受"NOT IN 空列表"的整表删除。

### B2. 三张业务表从不清理, 年增约 100 万行

**证据** (线上行数与增长):

| 表 | 行数 | 日均增长 | 清理路径 |
|---|---|---|---|
| `balance_reservations` | 15514 | 621 | **无** |
| `billing_settlement_tasks` | 8824 | 609 | **无** |
| `balance_ledgers` | 31031 | 1242 | **无** |
| `billing_records` | 15164 | 609 | **无**(财务证据, 应保留) |
| `rate_limit_usage_hourly` | 2160 | — | 有，30 天保留任务 (RateLimitUsageRetentionRun) |
| `channel_health_records` | 22289 | 1136 | 有(每渠道留 1000 条) |
| `relay_requests` | 5662 | 708 | 有(7 天) |

`billing_settlement_tasks` 最严重: 每行带 `snapshot` 文本约 **773 字节**,
且 **completed 状态的行不再被读取**(`Omit("snapshot")` 只在恢复时查未完成的),
即已完成的快照是纯占位。8600 行已占 8.7 MB, 年增约 168 MB。

**影响**: 现库 47 MB / 磁盘余 394 GB, **短期不痛**; 问题是 SQLite 单表变大后
索引与 VACUUM 成本上升, 且备份文件随之变大(已 42 MB/份 × 3 份)。

**修法**: `billing_settlement_tasks` 完成后清空 `snapshot` 或按保留期删除 completed 行;
`balance_reservations` 已结算且超期(如 30 天)的行可删(账本 `balance_ledgers` 才是权威留痕);
`rate_limit_usage_hourly` 按 30 天清理。代价: 小, 但要先确认没有对账流程依赖这些行。

**`billing_settlement_tasks` 已完成 (v1.5.18)**, 其余两项仍待处理:

- 清空动作放在"置 completed"的**同一个 UPDATE** 里, 不另起语句 —— 否则会开出"已终态但快照还在"
  的中间态, 而 `BillingSettle` 里"判 `reservation.Settled`"与"读 snapshot"之间存在窗口。
- 新增迁移 038 清理存量 completed 行。
- **manual 行的快照必须保留**: 那是自动路径放弃判断后, 人工对账的唯一证据。
- **行一条都不能删**: `BillingReleaseStale` 用 `NOT EXISTS (SELECT 1 FROM billing_settlement_tasks ...)`
  判断"这笔预扣有没有结算意图", 删行会让等待结算的预扣被当成泄漏释放 —— 钱会退错。
  所以这里只能清列, 不能按保留期删行。
- 既有测试 `TestBillingRecoveryDurableFailureAndReplay` 断言"快照不得被改写", 与改动相反,
  已改为两侧都断言(待执行时必须在, 完成后必须为空)。

### B3. 归档文件与部署备份从不清理

**证据**:
- `data/archives/` 20 个 ndjson, 33 MB, 最早 2026-10-15, **无任何删除逻辑**
  (`ArchiveDir()` 只被写入与列举调用, 没有 prune)。
- `data/backups/` 共 **1.2 GB**, 其中 9 个 `pre-*` 目录占 **996 MB** —— 那些是历次部署
  留下的整库副本 + 旧二进制(`deploy/swap_deploy.sh` 只停服换二进制, **不清理**)。
  自动备份本身是对的(keep=3, 42 MB/份)。

**影响**: 磁盘增长与部署次数线性相关, 与业务量无关。当前 394 GB 余量下不紧迫,
但这是唯一会随"运维动作"而非"业务量"增长的目录。

**修法**: 部署脚本收尾时保留最近 N 份 `pre-*`; 归档目录加保留期(与审计归档同口径)。代价: 小。

### B4. Cloudflare 代理未开, 国家码链路整条空转

**证据**:
- `tt.xjj.se` 公网解析直接返回源站 IP `202.6.204.216`(CNAME 到 `cdn.cloudflare.net` 但**未开橙云**);
- 响应头 **无 `cf-ray`** —— 橙云开启时必然存在;
- 因此 nginx 里那段精心写的 `geo $realip_remote_addr $from_cloudflare` 恒为 0,
  `map` 把 `CF-IPCountry` 一律置空;
- 结果: `relay_requests.client_country` **5584 行全空**。

**影响**: 区域亲和(C2)永远不可用; 国家维度统计全空; 按国家排查延迟无从下手。
反代配置本身没问题(`source_check` 用的 `$realip_remote_addr` 判定是对的),
**缺的只是 Cloudflare 后台把橙云打开** —— 纯外部操作。

**注意**: 开启橙云会改变源站看到的来源 IP 与协议, 需与现有 `set_real_ip_from` 白名单一起验证。

### B5. 通知链路接线（渠道审核与申诉已完成）

**证据**: `internal/op/notification_helper.go` 导出 7 个通知函数, 实际调用点:

| 函数 | 调用点 |
|---|---|
| `NotifyBalanceLow` | 1 (`balance_alert.go`) |
| `NotifyTicket` | 1 (`ticket.go`) |
| `NotifyRateLimitHit` | **0** |
| `NotifyChannelDown` | **0** |
| `NotifyChannelReview` | 2（审核批准/驳回） |
| `NotifyDispute` | 1（审核完成后异步） |

另有 `notifyAsync` 直调: 提现审核通过、充值成功各 1 处。
(`notifications` 线上 0 行, 但充值/提现/工单本身也从未发生过, 故这一条不构成反向证据。)

**当前状态**: 渠道审核批准/驳回与计费申诉审核完成已接入异步通知；限流触顶和渠道健康状态通知已在后续批次接入。

**剩余工作**: 核对限流触顶与渠道健康状态通知在当前版本的实际调用，并补齐覆盖；审核通知的去重由状态机保证结论只成功一次，异步失败只记录日志，不影响审核/退款主流程。

---

## C. 待评估: 稳健性与安全

### ~~C1. HTTP 服务未设任何超时~~ (已修 v1.5.18)

**已修**: `Close()` 改为带 30 秒超时的 `Shutdown(ctx)`(见 A2);
`ReadHeaderTimeout`(10s)与 `IdleTimeout`(120s)已设。

**刻意不设 `ReadTimeout` / `WriteTimeout`**, 这条比"设上"更重要:
`WriteTimeout` 从读完请求头开始计时, 到点直接掐断连接。线上 98.3% 是流式请求,
一条 SSE 流实测最长 156 秒 —— 设了写超时就等于把 A2 刚修好的"回答突然中断"换个触发条件重演。
`ReadTimeout` 同理, 它把请求体读取也一起限时(转发接口上限 512 MiB, 慢速上传会被掐)。
慢的上游等待由转发路径自己的上下文超时管, 那里能给出比"连接被断"更准确的错误。

测试里专门有一条 `TestApplyTimeoutsShapesServer` 断言这两个字段**必须为 0** ——
加超时看起来总是更"稳健", 后来者补上时应该先看到测试失败, 再去读一遍那条注释。

**顺带修掉一个并发缺陷**: 为了让停服路径可注入测试实例, 暴露出包级 `httpSrv` 的结构性问题 ——
`Close()` 在监听协程仍在读该结构体时就地改写它, `-race` 下报
`net/http.(*conn).setState` 数据竞争。改为加锁的指针 + "整体替换"语义, `Close` 现在幂等可重入
(重复调用、从未启动时调用都安全返回 nil)。生产里 `httpSrv` 只在启动时写一次, 所以线上没出过症状,
但这是真实的竞争。

### ~~C2. `channel_egresses.url` 明文入库, 且进备份~~ (已修 v1.5.18)

**证据**: 信封加密的覆盖范围(`internal/db/secretfields.go`)只有 `settings.value` 与
`channel_keys.key/api_secret`; `channel_egresses.url` 不在其中, 库内可直接读出
`http://user:pass@host:port`。自动备份是 `VACUUM INTO` 物理快照, 因此备份文件里也是明文。

v1.5.14 已处理**回显与错误信息**两个泄漏面, 但**落库与备份**仍是明文
(对比: `channel_keys.key` 是 `sysec$` 信封密文)。

**已修 (v1.5.18)**: 复用 `channel_keys` 那套 serializer 机制 —— 模型 tag + codec +
`secretstore.ChannelEgressBinding(channelID, name)`(列名 `url` 也进绑定)。
存量明文由 `MigrateSecretRows` 的出口段在启动时封存, 统计新增 `egresses_encrypted`。

**这里有一个只加 tag 修不好的坑**: `op.syncChannelEgresses` 用 `Updates(map)` 更新出口,
而 gorm 的 map 写入**绕过字段序列化器**。只加 tag 的话, 保存出口时明文照旧写进库 ——
模型层的加密形同虚设, 而且不会有任何报错。所以 db 侧的 map 写入兜底同步新增了
`channel_egresses` 分支; 同时给那处 `Model()` 补上身份, 否则兜底回调推不出归属会 fail closed,
保存出口整条失败(与 `syncChannelKeys` 的 `identity` 同口径)。

**仍未覆盖**: 渠道级 `channel_proxy`、`base_url`、`custom_header`、`param_override` 仍是明文 ——
它们与出口池是两套配置, 本次没有一起修。

**生产验证**: 库里 7/7 行均为信封, 明文行 0; 启动日志 `egresses=7`。
**残留窗口**: 加密之前生成的两份自动备份里仍是明文(`keep=3`, 会随新备份轮换掉)。
**回退边界**: 旧二进制没有该列的 serializer, 会把密文当代理地址原文拨号 —— 回退必须回到加密前的备份, 或先在库里解回明文。详见 runbook 6.1 / 6.2。

### C3. 通知/告警无静默窗口

**证据**: `internal/alert/` 与 `internal/op/` 全库无 silence / maintenance / mute 相关实现。

**影响**: 计划内维护(部署、迁移)期间产生的告警无法抑制, 只能事后忽略。代价: 小, 但属新功能。

---

## D. 已核实为"没问题"的项(避免重复排查)

- `/metrics` 鉴权正常: 无令牌返回 401, 令牌在库里是 `sysec$` 信封密文, 常量时间比较。
  (用 shell 从库里取到的不是明文, 故无法直接 curl 验证 —— 这是设计如此, 不是缺陷。)
- `channel_health_records` 有容量控制(每渠道保留 1000 条)。
- 自动备份有保留策略(keep=3)与 `VACUUM INTO` 一致性快照, 且校验可打开。
- 审计归档与调用日志归档留存期相互独立(审计 180 天不被日志 7 天顺手删掉)。
- 任务调度器有 panic 恢复(`runBody` 的 `recover`), 任务崩溃不会带走进程。
- 邮箱验证码有发信频控(`emailCodeRateOK(email, clientIP)`)与验证次数上限(`emailCodeMaxVerify`)。
- 限流策略覆盖用户/渠道/模型维度, 登录有失败锁定与退避。

---

## E. 建议顺序

| 优先级 | 事项 | 理由 |
|---|---|---|
| ~~P0~~ | ~~A1 出口 ID 落库~~ | **已修 v1.5.16**, 生产已验证 133/133 归因成功 |
| ~~P0~~ | ~~A2 优雅关闭~~ | **已修 v1.5.16**, 首次部署即打出"等待超时"证明在途流被保住 |
| ~~P0~~ | ~~A3 负价~~ | **已修 v1.5.17** |
| ~~P0~~ | ~~A4 前端静默失败~~ | **已修 v1.5.17** |
| ~~P1~~ | ~~C2 出口地址加密~~ | **已修 v1.5.18**; 全库最后一处明文凭据列 |
| ~~P1~~ | ~~B2 结算任务快照~~ | **已修 v1.5.18**(清列, 不删行; manual 保留) |
| ~~P1~~ | ~~B1 出口成绩持久化~~ | **已修 v1.5.18**; 原估"影响限于几次请求"偏轻, 实际是全权重定序 |
| ~~P2~~ | ~~C1 剩余的超时设置~~ | **已修 v1.5.18**; 只设读头与空闲, 刻意不设读写超时 |
| P1 | B5 四条通知接线 | 通知中心已建好却无事件源, 用户不知情; 改动小, 是剩下的最高价值项 |
| P1 | B2 balance_reservations 清理评估 | 需先确定账务留存与重放/对账证据策略；rate_limit_usage_hourly 已由 30 天任务清理 |
| P2 | B3 备份/归档清理 | 只随运维动作增长, 暂无压力 |
| 外部 | B4 开 Cloudflare 橙云 | 只有运维能操作; 开了才会解锁国家维度与区域亲和 |
| P3 | C3 告警静默窗口 | 新功能, 非缺陷 |
