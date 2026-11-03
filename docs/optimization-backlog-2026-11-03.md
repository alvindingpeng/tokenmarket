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

---

## B. 待修: 有明确证据的缺陷

### B1. 出口运行时成绩不持久化, 每次重启重新学习

**证据**: `internal/relay/egress.go` 的 `egressStates` 是进程内 map, 全库无对应表;
今天服务重启 **4 次**(每次部署一次)。

**影响**: 出口择优靠 EMA 成功率与等待时间。重启后所有出口回到"冷"状态,
冷启动期按已观测出口的平均值参与, 即**头几次请求可能选到较差的那条**。
部署越频繁, 这个窗口越常出现。

**修法**: 复用 `RouteSnapshotSave` 已有的落库通道(它已持久化成员指标), 给出口加一张快照表,
退出时随 `FlushRoutePersist` 落库, 启动时恢复。代价: 中。

### B2. 三张业务表从不清理, 年增约 100 万行

**证据** (线上行数与增长):

| 表 | 行数 | 日均增长 | 清理路径 |
|---|---|---|---|
| `balance_reservations` | 15514 | 621 | **无** |
| `billing_settlement_tasks` | 8824 | 609 | **无** |
| `balance_ledgers` | 31031 | 1242 | **无** |
| `billing_records` | 15164 | 609 | **无**(财务证据, 应保留) |
| `rate_limit_usage_hourly` | 2160 | — | **无** |
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

### B5. 四条通知链路未接线

**证据**: `internal/op/notification_helper.go` 导出 7 个通知函数, 实际调用点:

| 函数 | 调用点 |
|---|---|
| `NotifyBalanceLow` | 1 (`balance_alert.go`) |
| `NotifyTicket` | 1 (`ticket.go`) |
| `NotifyRateLimitHit` | **0** |
| `NotifyChannelDown` | **0** |
| `NotifyChannelReview` | **0** |
| `NotifyDispute` | **0** |

另有 `notifyAsync` 直调: 提现审核通过、充值成功各 1 处。
(`notifications` 线上 0 行, 但充值/提现/工单本身也从未发生过, 故这一条不构成反向证据。)

**影响**: 用户被限流、所属渠道故障、渠道审核结果、计费申诉处理 —— 这四类事件**用户完全不知情**,
只能靠外部联系。通知中心建好了却没有事件源。

**修法**: 在上述四处业务节点补调用(限流触顶处、渠道健康状态变更处、审核结论处、申诉状态变更处)。
代价: 小, 但要注意去重(`NotificationCreateDedup`)避免刷屏。

---

## C. 待评估: 稳健性与安全

### C1. HTTP 服务未设任何超时, 且用 Close() 而非 Shutdown()

**证据**:
- `internal/server/server.go` 只设了 `Addr` 与 `Handler`, 无 `ReadTimeout` / `WriteTimeout` /
  `ReadHeaderTimeout` / `IdleTimeout`;
- `server.Close()` 调 `httpSrv.Close()` —— **立即硬关所有连接**, 不等待在途请求;
  `Shutdown()` 在全库**从未被调用**。

**影响**: 部署时 `systemctl stop`(见 `swap_deploy.sh`)会立即切断所有在途 SSE 流。
线上 98.3% 是流式请求, 每次部署都会打断正在生成的回答 —— 对用户表现为"回答突然中断"。
今天重启 4 次。日常由 nginx 兜住慢连接, 故 C1 的前半段风险较低, 后半段是**每部署必发生**的。

**修法**: `Close()` 换成带超时的 `Shutdown(ctx)`(如 30 秒), 并设 `ReadHeaderTimeout`。代价: 小。

### C2. `channel_egresses.url` 明文入库, 且进备份

**证据**: 信封加密的覆盖范围(`internal/db/secretfields.go`)只有 `settings.value` 与
`channel_keys.key/api_secret`; `channel_egresses.url` 不在其中, 库内可直接读出
`http://user:pass@host:port`。自动备份是 `VACUUM INTO` 物理快照, 因此备份文件里也是明文。

v1.5.14 已处理**回显与错误信息**两个泄漏面, 但**落库与备份**仍是明文
(对比: `channel_keys.key` 是 `sysec$` 信封密文)。

**修法**: 给 `channel_keys` 那套 serializer tag 复制一份到出口 URL。
注意 AAD 绑定需要行归属(`channel_id`), 出口行有该列, 可行。代价: 中, 涉及迁移(存量明文重加密)。

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
| ~~P0~~ | ~~A1 出口 ID 落库~~ | **已修 v1.5.16** |
| P0 | C1 优雅关闭 | 每部署必发生, 用户可见(回答中断); 改动小 |
| P1 | B2 结算任务快照清理 | 唯一随业务量无界增长的 big-row 表; 改动小 |
| P1 | B5 四条通知接线 | 通知中心已建好却无事件源, 用户不知情; 改动小 |
| P2 | B3 备份/归档清理 | 只随运维动作增长, 暂无压力 |
| P2 | B1 出口成绩持久化 | 有重启学习窗口, 但影响限于冷启动几次请求 |
| 外部 | B4 开 Cloudflare 橙云 | 只有运维能操作; 开了才会解锁国家维度与区域亲和 |
| P2 | C2 出口地址加密 | 需迁移, 风险中等, 收益是消除备份里的明文凭据 |
| P3 | C3 告警静默窗口 | 新功能, 非缺陷 |
