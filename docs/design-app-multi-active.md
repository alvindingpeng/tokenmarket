# P3 应用多活改造方案(具体实施设计)

> 状态: 设计稿, 未实施。本文把《多区域前端加速 + 数据库双机高可用》§6 的 P3 展开到函数/SQL 级实施设计。
> 前提: P2 已落地(PG 主备集群 + HAProxy 统一入口), 本方案的所有实例连同一个 PG 入口。

## 0. 目标与非目标

- **目标**: 应用层 N 实例水平扩展(区域就近接入 + 实例故障自愈), 对外正确性与单实例**严格等价**: 统计不丢、限流不超发、任务不重复、设置即时生效、资金零差错。
- **非目标**: 数据库双写(已在主文档否决); 前端边缘分发(P1 覆盖)。
- **设计铁律**: 每项改造在单实例下**行为等价**、可独立发版; 灰度顺序是"单实例先上全部改造 → 稳定后再开第二实例"。

## 1. 单例源盘点(改造清单的由来)

| # | 单例源 | 位置 | 多实例症状 | 改造项 |
|---|---|---|---|---|
| 1 | 统计内存累计 + 全量快照覆盖写 | `op/stats.go`(`persistStatsSnapshots`/`persistDailyStats`, "缓存值即权威累计") | 两实例交替覆盖, **互相吞掉对方增量** | §2.1 |
| 2 | 限流内存桶 | `internal/ratelimit/limiter.go`(`buckets`/`active` map) | 额度×N 超发 | §2.2 |
| 3 | 后台任务进程内 ticker(10 个) | `internal/task` | 重复执行: 备份×N、核对×N、告警风暴 | §2.3 |
| 4 | 设置/渠道等内存缓存 | `op/setting.go`(`settingCache`)、`op/channel.go`(`channelCache`)、`op/ratelimit.go`(策略缓存) | A 实例改配置 B 不感知, 行为分叉 | §2.4 |
| 5 | 路由状态/冷却内存缓存 | `op/routepersist.go` + relay 状态 | 各自学习, 行为不一致(可容忍) | §2.5(接受分叉) |
| 6 | 请求历史/日志实时流实例局部 | `relay/state.go`(`requests` map + `publishRequestLocked`)、`handlers/log.go`(`streamOverview` SSE) | 管理端只看到本实例 | §2.6 |
| 7 | 启动迁移(AutoMigrate + migrate) | `internal/db/db.go` | N 实例同时启动竞态加列 | §3.2(迁移锁) |

资金链路(钱包/预扣 `BalanceReservation`/结算/兑换码核销/支付回调)全是 DB 事务 + 条件更新 + 幂等 upsert —— **多实例天然安全**, 本方案不动它, 只做回归验收。

## 2. 改造设计

### 2.1 统计: 快照覆盖写 → 事务性增量冲刷(核心改造)

**现状**: relay 每请求结束后 `relay/state.go:449` 调 `StatsTotalUpdate/StatsHourlyUpdate/StatsDailyUpdate/StatsAPIKeyUpdate`(+ 渠道/渠道模型/凭据维度)只写内存; `stats_save` 任务周期性把内存快照**全量覆盖**写库(`persistStatsSnapshots`/`persistDailyStats`, `OnConflict{UpdateAll}`)。

**方案: 增量冲刷 + 幂等批次**

1. 内存结构从"权威累计"改为"**未冲刷增量**"(delta), 保留全部现有 `StatsXXXUpdate` 签名与调用点(relay 零改动)。
2. 冲刷时生成 `batch_id`(UUID), 在**一个事务**内:
   ```sql
   INSERT INTO stats_flush(batch_id, created_at) VALUES (?, ?);      -- 唯一约束 = 幂等锚点
   UPDATE stats_total SET requests = requests + ?, ... WHERE id = 1;
   INSERT INTO stats_daily(date, ...) VALUES (?, ...)
     ON CONFLICT(date) DO UPDATE SET requests = requests + excluded.requests, ...;  -- mysql: ON DUPLICATE KEY UPDATE x = x + VALUES(x)
   -- hourly / stats_api_key / channel* / channel_model* / channel_key* 同式
   ```
3. **commit 成功才清 delta**; 失败保留待重试。因为同 batch_id 重放会被 `stats_flush` 唯一约束整体跳过, "commit 实际成功但客户端拿到错误"的重试也不会双计(恰好一次)。
4. 读路径: `StatsTotalGet/StatsHourlyGet/StatsAPIKeyGet/List` 现在直读内存; 改为**本地读缓存 + 定期(3-5s)从 DB 重载**(仪表盘可容忍秒级滞后), `StatsGetDaily` 等 DB 查询不变。
5. 生命周期对齐: `op.InitCache` 启动不再把 DB 行当"待覆盖的权威值"装回(改为装读缓存); `op.SaveCache` 停机改为**冲刷未决 delta**(而非快照覆盖)。
6. 时区/翻转: `statsHourlyCache [24]` 与日翻转逻辑保持; 冲刷 key 带 hour/date 唯一键, 跨实例按同一时钟键写同一行(增量合并天然正确)。

**验收测试**: 双实例并发打 N=10k 请求 → 断言 `stats_total.requests` 等精确等于 10k; 同 batch_id 重放不双计; 日期/小时翻转边界; 崩溃(冲刷前 kill)丢的是增量窗口内的统计(接受, 见风险)。

### 2.2 限流: 内存桶 → 共享存储实现(接口已预留)

**现状**: `internal/ratelimit` 固定 60s 窗口, RPM/TPM/并发三维度, `Reserve` → `Reservation.Settle/Release` 预留-结算语义; 代码注释明言"**多实例部署时以同一接口换成共享存储实现即可, 调用方无需改动**"。

**方案 A(推荐起步): PG 原子计数**(不引入新组件)

新表:
```
rate_window(scope_key text, window bigint, requests bigint, tokens bigint, forced bool, PRIMARY KEY(scope_key, window))
rate_active(scope_key text PRIMARY KEY, count bigint)   -- 并发/流计数
```

- **预留(RPM/TPM)**: 事务内 `SELECT ... FOR UPDATE` 点查当前窗口行, 判定后 `UPDATE rate_window SET requests=requests+1, tokens=tokens+est`; 超限返回 `Decision{Allowed:false, RetryAfter}`(窗口刷新时刻不变)。固定窗口行是热点行但每请求仅 1-2 次点写, PG 下数万 RPM 无压力(relay 瓶颈在上游 LLM 而非 DB)。
- **结算**: 保持"按真实用量回补差额"语义 —— `UPDATE ... SET tokens = tokens - est + actual`; 票据(settle/release)恰好一次: 用票据状态列或 `rows affected` 判定。
- **并发**: `UPDATE rate_active SET count = count + 1 WHERE count < ?` 放行判定, 释放减一; 崩溃遗留计数由 leader 清理任务按 TTL 收(或心跳续存)。
- **强制触顶**(上游 429 的 forced 标记): 写共享窗口行, 跨实例生效。
- 窗口过期: 顺手 `DELETE FROM rate_window WHERE window < ?`(写入路径)或 leader 任务清扫。

**方案 B: Redis(INCR + EXPIRE + Lua 原子预留结算)** —— 性能更好, 但引入新组件; 若后续要全站缓存层再考虑, 接口同样不变。

**验收测试**: 同一 API Key 限额 100 RPM, 双实例并发打 300 → 放行≈100(含窗口边界±1); 结算/释放各恰好一次; forced 标记跨实例生效。

### 2.3 后台任务: ticker 全开 → PG 租约选主(仅主执行)

**现状**: `internal/task` 10 个周期任务(统计落库/自动备份/渠道健康/计费回滚/每日核对/日志归档/价格更新/更新检查/视频清理/LLM 清理), 周期来自设置热更(`settingTasks` 映射)。

**方案: 租约 + fencing epoch**

```
task_leader(id = 1 PRIMARY KEY, instance_id text, lease_until timestamptz, epoch bigint)
```

- 抢占: `UPDATE task_leader SET instance_id = ?, lease_until = now() + interval '30 s', epoch = epoch + 1 WHERE lease_until < now() OR instance_id = ?`; `rows affected = 1` 即持主, 每 10s 续租。
- **仅持主实例的 task 循环运行**; 从实例只抢租约不跑任务。主实例优雅停机时主动清租约(`lease_until = now()`)加速接管; 硬宕机则 30s 租约过期后接管 → **RTO(任务层)< 30s**。
- **fencing**: 任务每次执行前校验 `instance_id` 仍是自己且 epoch 未变, 防网络分区下旧主继续跑(与 PG 主备的 fencing 同思路)。
- 时钟统一用 `now()`(DB 时钟), 不用实例本地时钟。
- 任务幂等复核(即使选主也建议做): `billing_rollback`/`billing_reconcile` 确认条件更新幂等; `auto_backup` 文件名带时间戳(重复只是浪费); `log_archive` 需防双归档(选主已解决)。

### 2.4 设置/渠道缓存: 失效传播(版本轮询)

**现状**: `settingCache`/`channelCache`/限流策略缓存都是"写路径刷本进程缓存 + 读路径不触库"; 设置热更(`settingTasks`、任务周期重算)只作用本进程。**A 改配置 B 不感知**。

**方案: 缓存版本号轮询**(简单可靠)

- settings 表已有更新时间或可加 `updated_at`; 每实例后台协程每 5s 查 `SELECT max(updated_at)`, 变化则:
  1. 重载 `settingCache`(整表);
  2. 执行与本进程相同的热更链: `ratePolicyCacheReload`、任务周期重算(`task.ApplySetting`)、渠道/模型缓存重载、支付网关配置重建。
- 复用现有 `op.InitCache` 的装载逻辑, 抽出 `ReloadCaches()` 供启动与轮询共用。
- 备选: PG `NOTIFY/LISTEN` 即时生效(复杂度高, 先不做)。
- **盘点清单**(逐项确认走版本重载): settings、rate policy、channel/channelModel/key、LLM 价格表、告警规则缓存。验收用"改 A 5 秒内 B 生效"。

### 2.5 路由状态: 接受分叉(显式决策, 不改造)

RouteStateRecord/RouteCooldown/熔断状态在 DB 有持久化 + 内存缓存。多实例各自学习渠道健康是**合理行为**(等价于 LB 后多台独立观察, 还带区域亲和收益)。仅做两点:
- 回写已是对主键 upsert ✓ 多实例并发安全(最后写胜出);
- 监控与管理端按 `instance_id` 维度展示冷却/熔断状态, 避免误读。

### 2.6 实时通道与实例局部视图

- relay 流式/SSE: 连接级, 无影响 ✓。
- 管理端"实时请求"SSE(`relay.publishRequestLocked` → `streamOverview`)与请求列表(`listLogs` 读 relay 内存 `requests` map): **实例局部**。
  - 方案: 列表改读 DB(`RelayRequest` 表本就持久化了全部请求, 加索引即可), 全局视图; 实时 SSE 流保留但消息带 `instance_id`, 管理端标注/可按实例过滤。
- 日志 SSE(`handlers/log.go` 读本机日志): 同上标注实例, 或后续接集中日志(Loki), 本期只做标注。
- 告警去重: 告警产生点都在任务/业务路径, 选主后天然单点; 业务路径告警(如支付金额不符)可能多实例各发一次 —— 告警表加 `dedupe_key + 窗口去重`(同 key N 分钟内只发一条)。

## 3. 基础设施变更

### 3.1 库表变更(全部新增/加列, 无破坏)
`stats_flush`、`rate_window`、`rate_active`、`task_leader`、settings/`channel` 的 `updated_at`(如缺)、可选 `instance` 注册表(id、版本、心跳、租约, 便于观测)。

### 3.2 迁移锁(启动竞态)
N 实例同时启动会并发跑 `AutoMigrate` + `migrate` 前后置迁移(竞态加列)。启动流程改为:
1. 先抢 `migrate_lock` 租约(同 `task_leader` 机制);
2. 持锁者执行迁移, 完成后写 `schema_version` 并放锁;
3. 其他实例等待 `schema_version` 到位后跳过迁移启动。

### 3.3 配置与部署
- config 新增 `instance.id`(显式, 或启动生成 UUID 并打日志); 其余配置全实例一致。
- systemd ×N(或容器), LB/nginx upstream 按区域加权; DB 走 HAProxy 入口(DSN 不变)。
- 优雅停机: `shutdown` 注册链已有(停机冲刷 delta + 释放租约 + 关闭 DB)✓ 顺势补齐。

## 4. 一致性边界与验收

| 正确性 | 保证机制 | 验收方法 |
|---|---|---|
| 资金: 钱包/预扣/结算/兑换码/支付回调 | DB 事务 + 条件更新 + 幂等 upsert(已有) | 双实例并发压测后跑 `billing_reconcile` + 资金总额快照对比; 同一支付回调并发打两实例 → 入账一次 |
| 统计不丢不重 | 增量冲刷 + batch_id 幂等 | 压测 10k 请求, DB 计数精确=10k |
| 限流不超发 | 共享计数 | 并发压测放行数≈限额 |
| 任务不重复 | 租约 + fencing | 双实例跑 24h, 任务执行日志每周期一次; 杀主 30s 内接管 |
| 设置即时生效 | 版本轮询(5s) | A 改设置 B 5s 内生效 |
| 实例故障自愈 | LB 健康检查 + 租约过期 | kill -9 实例, 流量转移无错误率突刺(连接重试) |

## 5. PR 切分(每步独立发版, 单实例行为等价)

| PR | 内容 | 版本建议 | 依赖 |
|---|---|---|---|
| PR1 | 统计增量冲刷 + stats_flush + 读缓存改造(单实例语义不变) | 补丁 | - |
| PR2 | 限流共享存储实现(Limiter 接口后端替换)+ 表 | 补丁 | - |
| PR3 | 任务租约选主 + 迁移锁 | 补丁 | - |
| PR4 | 设置/渠道缓存失效传播(版本轮询 + ReloadCaches) | 补丁 | - |
| PR5 | 请求列表读库 + SSE/日志标注 instance_id + 告警去重 | 补丁 | - |
| PR6 | 双实例集成测试(compose)+ 压测对账 + 运维手册 | 小版本 | PR1-5 |

PR6 完成后开第二实例灰度: 2 实例跑一周 → 对账无差异 → 按区域扩容。

## 6. 风险与未决项

- **统计增量的恰好一次**: 依赖 `stats_flush` 唯一锚点; "commit 歧义"窗口已消除, 但极端时序(冲刷前崩溃)丢该窗口统计 —— 统计口径可接受, 资金链路不经此路径。
- **共享计数热点**: 全局 scope 的窗口行是单行热点; 观测到瓶颈再按 scope 分片或换 Redis(接口不变)。
- **租约时钟**: 统一用 DB `now()`; 不信任实例本地时钟。
- **PG NOTIFY 即时失效**是 PR4 的后续优化项, 先轮询。
- **未决**: 实例数上限(当前统计写路径每请求一次内存更新 + 周期冲刷, 预计单实例 200-500 RPS 无压力; 上限靠压测定)。
