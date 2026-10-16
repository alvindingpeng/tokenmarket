# 多区域前端加速 + 数据库双机高可用架构改造方案

> 状态: 设计稿, 未实施。本文是调研结论 + 改造方案, 按阶段给出变更项与验收标准。

## 0. 结论先行

| 需求 | 技术上可行? | 结论 |
|---|---|---|
| 前端放不同服务器并行, 分区域提速 | **可行, 改动很小** | 静态资源分离到 N 个边缘节点(nginx) + 反代回源; 前端构建产物本就独立可发布, 缓存策略已 CDN 就绪 |
| 数据库两台互备、实时同步、故障自动切换 | **可行, 但必须换引擎** | SQLite 没有生产级的"双机实时同步+自动切换"方案; 推荐迁 PostgreSQL 主备流复制 + Patroni 自动切换 —— 代码已原生支持 postgres 方言 |
| 双主互写(严格意义的"互为备份") | **不采纳** | 钱包/计费/兑换码是资金一致性场景, 双写冲突合并(last-write-wins)会吞账; 资金系统必须单写者 |
| 应用层多活水平扩展 | **暂不满足** | 有三个明确障碍(统计快照覆盖写、限流内存桶、后台任务进程内调度), 见 §6; 建议保持"1 活 + 1 热备" |

一句话: **前端可以立刻分布式, 数据库换 PostgreSQL 后可以双机自动切换, 应用保持单活即可拿到这两个收益。**

## 1. 现状架构盘点(代码事实)

### 1.1 部署形态
- 单体 Go 二进制: Gin API + GORM + 嵌入前端, systemd 服务 `switchyard`, 目录 `/opt/switchyard/{switchyard, data/data.db}`。
- 前端 React SPA 构建到 `static/out`(`web/vite.config.ts` outDir), 经 `go:embed`(`static/static.go`)打进二进制。
- 静态缓存策略(`internal/server/middleware/static.go`): 带 hash 的资源 `public, max-age=31536000, immutable`, `index.html` `no-cache` —— **已经是标准 CDN 就绪形态**。
- 无用户上传文件(multipart 仅用于 relay 请求改写, 不落盘); 磁盘上只有 `data.db` + `backups/`(自动备份, 可推 WebDAV 异地)。

### 1.2 数据库层(这是本方案的好消息)
- `internal/db/db.go` 的 `InitDB(dbType, dsn)` **已支持 sqlite | mysql | postgres** 三种方言(`gorm.io/driver/postgres`、`mysql` 都是直接依赖)。
- 配置即用: `config.json` 的 `database.type` / `database.path`(实为 DSN), 或环境变量 `SWITCHYARD_DATABASE_TYPE` / `SWITCHYARD_DATABASE_PATH`(`internal/conf/config.go` + viper)。
- 迁移框架(`internal/db/migrate/`)按 `db.Dialector.Name()` 分支处理方言差异; postgres 还专门处理了迁移后的 prepared plan 失效(`DEALLOCATE ALL`)。说明 **postgres 是一等公民, 不是摆设**。
- SQLite 现参数: WAL、synchronous=NORMAL、busy_timeout=5s、foreign_keys=ON; 连接池 MaxIdle=10 / MaxOpen=100。
- **现成的异构迁移通道**: `op.DBExportAll`(整库导出 JSON `DBDump`) + `op.DBImportIncremental`(按主键 upsert 导入) —— SQLite → PostgreSQL 的数据搬迁可以直接用它, 不必写新工具(或辅以 pgloader 做大批量)。

### 1.3 无状态与有状态的分界(决定能不能拆、能不能多活)
- **无状态**: 鉴权是 JWT + HttpOnly Cookie `auth`(`middleware/auth.go`), 无服务端 session; token 版本(`users.token_version`)与鉴权密钥(设置表)都在库 → 多前端/多实例天然可用。
- **进程内状态(多实例的障碍)**:
  1. **统计缓存**: `op/stats.go` 内存累计 + `persistStatsSnapshots` / `persistDailyStats` **全量快照覆盖写**(注释原文"缓存值即权威累计, 覆盖写使重试安全")。两个实例会互相覆盖对方的累计值 → **多实例丢数据**。
  2. **限流**: `internal/ratelimit` 内存桶(`buckets map` + `active map`) → 每个实例独立计数, N 实例等于额度×N。
  3. **后台任务**: `internal/task` 进程内 ticker, 共 10 个周期任务(统计落库/自动备份/渠道健康/计费回滚/每日核对/日志归档/价格更新/更新检查/视频清理/LLM 清理), 周期来自系统设置可热更。多实例会**重复执行**。
  4. 路由状态/冷却(RouteStateRecord 等)有 DB 持久化 + 内存缓存, 多实例各自学习, 行为可容忍但不一致。
- **流式通道**: relay SSE(`/v1`)与管理端日志 SSE(`text/event-stream`) → 反代必须关闭缓冲(`proxy_buffering off`)。请求体上限: 管理端 32MiB / relay 512MiB(`internal/conf/config.go`)。

## 2. 需求拆解

- **需求 A**: 前端放不同服务器并行, 不同区域用户访问提速。
- **需求 B**: 数据库两台互备, 数据实时同步, 单库故障自动切换。

注意边界: A 只解决**静态资源与页面加载**的区域延迟; 若用户跨洋访问 API, 接口延迟仍在(回源单点)。要区域级 API 提速需要应用多活(§6, 阶段 P3), 与 B 解耦, 不建议一起上。

## 3. 需求 A: 前端多服务器并行(阶段 P1, 零后端代码改动)

### 3.1 拓扑

```
  区域用户A ──> 边缘节点A(nginx: static/out + 反代) ──┐
  区域用户B ──> 边缘节点B(nginx: static/out + 反代) ──┼──> 应用源站(单活 switchyard)
  (可选 CDN) ──> 静态资源命中边缘/CDN 缓存, /api、/v1 回源┘
```

### 3.2 边缘节点配置要点(nginx)
- SPA 回退: `try_files $uri /index.html`(静态资源已在应用端带 hash 文件名, 可长缓存)。
- 反代 `/api/` 与 `/v1/` 到源站; **`proxy_buffering off`** 且读超时放宽(SSE/流式生成); `client_max_body_size 512m`(relay 生图/生视频)。
- 财务类接口(余额/充值/提现/兑换/计费)若置于 CDN 之前, 必须显式 `Cache-Control: no-store` 或按路径绕过 CDN —— 默认**不缓存任何 /api 响应**。
- 健康检查用现成 `/healthz`(含 db 状态与版本)。

### 3.3 域名与会话(唯一的代码决策点)
现在 Cookie 是 host-only(`SetCookie("auth", ..., "/", "", false, true)`, 无 Domain、Secure=false)。三种做法:
1. **同域名 + GeoDNS/CDN 智能解析**(推荐): 用户始终在同一个域名下, Cookie 天然有效, 不改代码; 域名解析按来源区域返回不同边缘节点。
2. 各区域独立子域(app-a / app-b): 零改动, 但用户跨区要重新登录(会话不共享)。
3. 共享父域 Cookie(`.example.com` + `SameSite=None; Secure`): 需改 `SetCookie` 一处 + 全站 HTTPS; 换来跨子域免登录。

### 3.4 构建流水线变更
- `scripts/build-local.sh` 之外新增"前端发布物": 把 `static/out` 打成 tar 制品(CI 的 `scripts/build.sh` 顺手产出), 边缘节点只部署这个目录, 不需要二进制。
- 源站继续用现二进制(内嵌前端作为兜底), 边缘挂掉时可直连源站 —— 这是白拿的降级路径。

### 3.5 真实收益边界(诚实说明)
SPA/静态资源/首屏在区域就近命中, 页面加载显著提速; 但**接口调用仍回源站**, 跨洋 API 延迟(50-300ms/RTT)不变。对"操作类后台"体验提升明显, 对"纯 API 调用"没有提速。若需要后者, 见 §6 应用多活, 或先用 CDN 的 TCP 优化/就近接入缓解。

## 4. 需求 B: 数据库双机实时同步 + 自动切换(阶段 P2)

### 4.1 为什么不能继续 SQLite
- SQLite 是单文件单写者, WAL 不提供网络复制; "两台互备实时同步 + 自动切换"在 SQLite 生态没有生产级方案:
  - **LiteFS**: 单主 + FUSE 文件系统代理 + 租约选主, 仍是单写者, 项目成熟度有限, 跨地域同步延迟同样存在, 运维复杂度反而高于 PG。
  - **Litestream**: 异步 WAL 归档到对象存储, 本质是"分钟级温备/备份", 不满足"实时同步 + 自动切换"。
  - **DRBD/共享存储**: 文件级复制对 SQLite 的锁与 WAL 不友好, 不建议。
- 现架构自身也不适合双写: 统计覆盖写、限流、后台任务都假设单写者(§1.3)。

### 4.2 推荐方案: PostgreSQL 流复制 + Patroni 自动切换

```
                ┌─ Patroni + etcd(2 DB 节点 + 1 witness, 3 节点仲裁)
  应用(1活+1热备) ─> HAProxy/VIP(5432 统一入口) ─> PG 主 ─(同步流复制)─> PG 备
```

- **复制模式**: 同城/同机房建议**同步复制**(`synchronous_commit=on` + `synchronous_standby_names`): 切换零数据丢失(RPO=0), 代价是写延迟 +1 RTT 且备库宕机会阻塞写(可配 quorum)。跨地域建议异步复制并明确 RPO(通常秒级), 或半同步。
- **自动切换**: Patroni 用 etcd 租约维持 leader, 主库失联(进程挂/网络分区/磁盘满)后自动提升备库, 秒级完成; HAProxy(或 keepalived VIP)把连接打到当前 leader, **应用 DSN 不变、无感切换**(连接断开后重连即可)。
- **仲裁**: 两台 DB 无法构成多数派(2 节点脑裂风险), 需要第 3 个轻量见证(witness, 可以是应用机或小规格 VPS)。
- **应用侧**: 保持 1 活 + 1 热备(systemd 拉起 + 健康检查/人工切换), 与 DB 故障切换解耦。
- **等价替代**: MySQL InnoDB Cluster(Group Replication + MySQL Router), 代码同样支持, 二选一即可。

### 4.3 明确否决: 双主互写
"互为备份"若理解为两台都可写(双主), 强烈不建议:
- 余额、预扣(`BalanceReservation`)、结算、兑换码核销全部依赖行级原子性与单写者时序; 双主冲突合并会直接吞账。
- 支付回调(易支付/支付宝/Stripe/Creem)的幂等靠条件更新, 双写下两个节点同时处理回调会重复入账。
- 故障切换语义在双主下反而复杂(冲突、乱序、双向延迟), 钱包场景不可接受。

### 4.4 数据迁移(利用现成通道)
1. 停写窗口(分钟级): 源站停服 → `DBExportAll` 导出 JSON(或直接 pgloader 逐表)。
2. PostgreSQL 建库(应用空启动即可 AutoMigrate 建表) → `DBImportIncremental` 导入(逐表 upsert, 保 id)。
3. 校验: 各表行数 + 资金总额核对(users.balance / total_revenue / 提现冻结 / 兑换码状态分布), 跑一遍每日核对任务(`billing_reconcile`)。
4. 切配置 `database.type=postgres` + DSN 指向 HAProxy, 重启应用。
5. 回归重点: 支付回调幂等(含金额篡改拒付)、预扣超时回滚、每日核对、统计落库、备份导出。

### 4.5 备份策略调整
- 现有自动备份(整库文件 + 可选 WebDAV 异地)在 PG 下改为: `pg_dump` 每日全量 + WAL 归档(或交给 Patroni/pgBackRest), WebDAV 异地推送保留。
- 现有 `backup decrypt/verify` 工具链保留用于归档解密。

## 5. 变更项清单(按是否改代码)

| # | 变更项 | 层 | 改代码? | 说明 |
|---|---|---|---|---|
| 1 | `database.type=postgres` + DSN | 配置 | 否 | 已支持, 指向 HAProxy/VIP |
| 2 | SQLite→PG 数据迁移 | 运维 | 否(用现成 DBExportAll/DBImportIncremental) | 或 pgloader |
| 3 | 前端发布物打包(static/out tar) | 流水线 | 否(加一步打包) | 边缘节点只发这个 |
| 4 | 边缘 nginx(SPA 回退 + 反代 + 关缓冲) | 运维 | 否 | 配置模板化 |
| 5 | 域名/会话策略(三选一, §3.3) | 配置/小改 | 选 3 才改(SetCookie 一处) | 推荐选 1 不改代码 |
| 6 | 财务接口 no-store / 绕过 CDN | 运维+小改 | 视 CDN 而定 | 防缓存泄漏 |
| 7 | PG 双机 + Patroni + etcd witness + HAProxy | 运维 | 否 | 含监控与切换演练 |
| 8 | 备份改 pg_dump + WAL 归档 | 运维 | 小改(备份任务适配) | WebDAV 保留 |
| 9 | 复制延迟/failover 事件接入告警 | 小改 | 建议 | 复用现有 alert 通道 |
| 10 | 应用多活三障碍(统计增量写/限流集中计数/任务选主) | 代码 | 是 | **P3 才做**, 单活不需要 |

## 6. 阶段 P3(可选): 应用多活 / 区域 API 就近接入

前提改造(缺一不可, 否则多实例会丢数/超发/重复跑任务):
1. **统计**: 快照覆盖写 → 增量 upsert(`column = column + ?`), 或按实例分片行、读时聚合。
2. **限流**: 内存桶 → 集中计数(DB 原子计数或 Redis), 或按 API Key 一致性哈希到固定实例。
3. **后台任务**: 10 个 ticker → 库级租约选主(仅 leader 跑), 或独立"调度实例"。
4. 路由状态/冷却: 可接受各自学习, 或改集中存储。
5. 日志 SSE 是实例局部的, 需按实例标注或集中收集。

完成 P3 后: 区域用户就近接入区域应用节点(每个节点连同一个 PG 集群入口), 才是完整意义上的"全球提速"。**建议与 P2 至少间隔一个稳定期**, 资金正确性优先。

## 7. 故障切换语义与验收标准

- 目标: **RPO = 0**(同步复制) / RPO < 5s(异步, 明确接受); **RTO < 60s**(Patroni 自动, 含连接重连)。
- 演练清单(上线前必做):
  1. `kill -9` 主库进程 → 自动提升, 应用重连, 写入恢复, 无数据丢失。
  2. 主备网络分区(防火墙模拟) → 不出现双主(witness 参与仲裁)。
  3. 备库宕机 → 写入按 quorum 策略继续/阻塞符合预期。
  4. 主库磁盘满 → 告警与降级行为符合预期。
  5. 切换期间的支付回调重试(模拟回调超时重发) → 幂等成立, 不重复入账。
- 一致性验收: 切换后跑 `billing_reconcile`(每日核对)与资金总额快照对比; 统计面板无断层。
- 监控: 复制延迟、leader 变更、failover 事件、连接错误率 → 接入现有告警通道(webhook/渠道告警)。

## 8. 里程碑(估期)

| 阶段 | 内容 | 估期 | 风险 |
|---|---|---|---|
| P1 前端分离与边缘加速 | 发布物打包 + nginx 模板 + 域名/会话决策 + 验证 | 2-3 天 | 低(零后端改动, 源站兜底) |
| P2 数据库双机 | PG 集群 + Patroni/etcd + HAProxy + 迁移 + 演练 + 回归 | 1-2 周 | 中(资金数据迁移, 靠演练与核对兜底) |
| P3 应用多活 | 三个内存态改造 + 多实例部署 + 验证 | 另行评估 | 中高(触及统计/限流/任务正确性) |

## 9. 常见误区提醒

- "前端多服务器"不等于"全球 API 提速": 静态资源提速成立, 接口延迟仍回源(§3.5)。
- "两台数据库互为备份"不等于"双主": 资金系统单写者 + 自动切换才是安全语义(§4.3)。
- "SQLite 也能双机"(LiteFS/Litestream)是伪需求满足: 前者单写者+实验性, 后者是备份不是实时互备(§4.1)。
- 同步复制不是免费的: 写延迟与备库故障策略要在上线前想清楚(§4.2)。
