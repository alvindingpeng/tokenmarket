# 渠道准入审核 + 观察期 + 服务质量展示 设计方案(第二批运营能力)

> 状态: 设计稿, 未实施。基于现有渠道/健康探测/路由指标/分享页代码事实设计, 按 P1→P3 给出状态机、数据模型、API 与验收标准。
> 姊妹篇: 《交易与结算运营闭环》已交付 (v0.30.0) —— 交易可追溯、退款可冲正、结算可对账; 本篇解决供给侧「渠道从哪里来、能不能信任、表现怎么样」。

## 0. 结论先行

| 需求 | 技术上可行? | 结论 |
|---|---|---|
| 渠道准入审核(渠道商提交 → 管理员审批) | **可行, 改动小** | 现有 `ChannelPublish` 只是布尔开关, 无审批闸口; 加一个审核状态机 + 配置指纹复审, 存量渠道一次性回填不打扰 |
| 观察期(新渠道试运行 + 自动升降级) | **可行, 原料齐全** | `ChannelHealthRecord`、`StatsChannelDaily`、relay `modelMetrics` 已提供全部观测数据, 缺的只是「按日评估 + 升降级」的定时任务 |
| 服务质量展示(对内明细 + 对客质量等级) | **可行** | `relay/score_view.go` 的评分算法(价格/延迟/成功率 + 可靠性软惩罚)可直接复用到渠道级; 分享页加脱敏质量摘要 |

一句话: **把「发布即上架」改成「提交 → 审核 → 观察 → 转正」四段式, 质量数据按日沉淀并同时服务管理员、渠道商和公开分享页(脱敏)。**

设计铁律(与既有资金/运营改造一致):
1. **不中断存量流量**: 门禁只管「新暴露」(发布、进公开候选), 已在分组里跑的成员不受审核状态影响;
2. **不引入新外部依赖**: 全部落在现有 GORM 模型 + SQLite/PG 事务 + `internal/task` 周期任务上;
3. **不碰资金不变量**: 与计费/钱包/结算完全正交, 质量升降级不改任何余额口径。

---

## 1. 现状盘点(代码事实)

### 1.1 渠道生命周期现状
- `internal/model/channel.go`: `Channel{UserID, Shared bool, ShareCode string, Enabled bool, ChannelConfig, StatsMetrics}` —— **没有审核/信任字段**, `shared` 一按即公开。
- `op.ChannelPublish(id, shared, scope, ctx)` (`internal/op/channel.go`): 置 `shared` + 生成 10 位 `share_code`, 无审批、无校验。
- `op.ChannelCreate`: 渠道商(`RoleReseller`)可自助建渠道、自动探测并入模型(`AutoAddChannelModels`)、一键改价(`reprice`), 全程无管理员介入。
- 权限基座: `model.Scope{ID, Role}` + `scope.Owns()`(`internal/model/scope.go`); 管理员全量, 渠道商仅自有; 非归属访问按 404 语义。
- 审批语义先例: `SettingKeyRegisterApproval`(注册后管理员审批)已经存在, 审核流的文案/交互可参照。

### 1.2 观测数据现状(观察期的全部原料)
| 数据 | 位置 | 覆盖 | 缺口 |
|---|---|---|---|
| 健康探测 | `op/channelhealth.go` + `model.ChannelHealthRecord` | 延迟/状态码/是否健康, 每渠道保留最近 1000 条 | 会被轮转, 不能当长期历史 |
| 告警 | `model.ChannelAlert` + `op/alertrules.go` | 连续失败(`alert_fail_streak` 默认 5)、抖动抑制(`alert_dedup_minutes`)、延迟阈值(`health_latency_ms` 默认 1000) | 无「观察期收紧阈值」概念 |
| 运行指标 | `internal/relay/metric.go` | `modelMetrics[channelModelID]`: 首响应耗时 EMA、成功率 EMA、样本数(进程内 + `RouteMetricRecord` 快照恢复) | 无渠道级聚合视图 |
| 评分 | `internal/relay/score_view.go` | `MemberScoreView`(0-100 分, 价格/延迟/成功率三维 + 可靠性软惩罚 + 冷成员中性 50) | 只到「分组成员」粒度, 未到渠道粒度 |
| 日粒度统计 | `model.StatsChannelDaily` / `StatsChannelModelDaily`(已建表) | 请求成功/失败、`WaitTime` 累计、token/成本, 按渠道与渠道模型分日 | 已有趋势原料, 但未连到任何质量 UI |

### 1.3 展示面现状
- `internal/op/share.go` + `handlers/share.go`: 公开分享页(`/share/:code`, `share.html` 纯前端模板)只输出**上架模型 + 对客价**, 无任何质量信息;
- `web/src/components/modules/channel/`: `Stats.tsx` 已有成功率(`successRate`)与熔断剩余时长(`isBroken`/`remainText`)的展示, 是质量面板的天然落点;
- `internal/task`: 周期任务框架(统计落库/渠道健康/每日核对/日志归档等 10 个), 新的「观察期评估」「质量快照」任务直接挂这里。

---

## 2. 目标与非目标

- **目标**:
  1. 渠道上架前必须经管理员审核(渠道商提交 → 管理员批准/驳回), 关键配置变更触发复审;
  2. 审核通过后进入观察期(默认 7 天), 达标自动转正, 不达标自动延长或收回发布;
  3. 服务质量按日沉淀: 管理员/渠道商看明细与趋势, 公开分享页看脱敏等级。
- **非目标**:
  - 不做渠道自动评分定价(改价仍走现有 reprice 流程);
  - 不做用户评价/评论系统(主观数据, 不在本次范围);
  - 不改选路算法本身(`selectByScore` 等 7 种分组模式保持现状, 观察期只做标注与折减);
  - 不动计费/结算/提现任何口径。

---

## 3. P1 渠道准入审核

### 3.1 状态机

```
        ┌─────────────┐   submit    ┌─────────┐  approve  ┌──────────┐
        │ draft(草稿) │ ──────────► │ pending │ ────────► │ approved │
        │  建渠道默认  │             │ 待审核  │           │ 已通过   │
        └─────────────┘             └─────────┘           └────┬─────┘
              ▲                          │ reject              │ 指纹失配(关键配置被改)
              │                          ▼                     ▼
              │                    ┌──────────┐          ┌──────────┐
              └── 修改后重新提交 ── │ rejected │          │  stale   │
                                    │  已驳回  │          │ 待复审   │
                                    └──────────┘          └──────────┘
```

- `draft`: 渠道创建后的默认态, 自用(自己分组里跑)不受限, 但**不能发布/进公开候选**;
- `pending`: 渠道商提交审核; 同一渠道同时只允许一条 pending 提交;
- `approved`: 允许 `ChannelPublish`、允许出现在 `ChannelGrantCandidates` 的对外候选;
- `rejected`: 附驳回原因, 渠道商修改后可重新提交;
- `stale`: 审核通过后**关键配置**被改动, 复审前不新增暴露; 存量分组流量不断。

### 3.2 数据模型(扩展 `model.Channel`)

```go
// internal/model/channel.go — ChannelConfig 平铺列之外新增审核列
type Channel struct {
    // ... 现有字段不动 ...
    ReviewStatus      string     `json:"review_status" gorm:"size:16;not null;default:draft;index"` // draft|pending|approved|rejected|stale
    ReviewNote        string     `json:"review_note" gorm:"size:512;not null;default:''"`          // 提交说明 / 审核意见 / 驳回原因
    SubmittedAt       *time.Time `json:"submitted_at"`
    ReviewedBy        uint       `json:"reviewed_by" gorm:"not null;default:0"`
    ReviewedAt        *time.Time `json:"reviewed_at"`
    ConfigFingerprint string     `json:"config_fingerprint" gorm:"size:64"` // 审核通过时的关键配置指纹, 更新后比对触发 stale
    TrustLevel        string     `json:"trust_level" gorm:"size:16;not null;default:observing;index"` // observing|trusted|probation|revoked (P2)
    ObservationStart  *time.Time `json:"observation_start"`                                              // P2
    ObservationUntil  *time.Time `json:"observation_until"`                                              // P2
}
```

**配置指纹口径(关键决策)**: 只对「会改变供给行为」的字段取指纹(`base_url` + 五个协议路径 + `dialect` + 模型清单)。**价格变动不触发复审** —— 改价已有 `models/reprice` 审计链路, 且价格高频调整不应反复卡审核。

**自动加模型不失配(实施决策)**: 模型自动发现(`AutoAddChannelModels`)只枚举已过审上游的供给面(地址与路径未动), 新增模型默认不上架、不产生新暴露, 因此**不触发 stale** —— 自动发现后推进指纹基线(`channelReviewSyncBaseline`), 避免下一次人工保存被误判指纹失配。人工增删模型仍走 `ChannelUpdate` 指纹比对触发 `stale`。

### 3.3 门禁(新增暴露的唯一闸口)

| 动作 | 现状 | 新规 |
|---|---|---|
| `ChannelPublish(shared=true)` | 无条件 | 必须 `review_status=approved` 且 `trust_level≠revoked` |
| `ChannelGrantCandidates` 对外候选 | shared=true 即出现 | 额外要求 `approved` |
| 新建/编辑渠道、跑自己分组 | 无限制 | **不变**(自用不设门禁, 观察期数据正是从自用流量里长出来的) |
| 存量已在分组里的成员 | — | **不变**(不中断存量流量) |

### 3.4 API 与审计

```
POST /api/v1/channel/review/submit    {id, note}           渠道商/管理员 → pending
GET  /api/v1/channel/review/list      ?status=&page=       管理员全量, 渠道商只看自有提交
POST /api/v1/channel/review/approve   {id, note}           管理员 → approved + 写指纹
POST /api/v1/channel/review/reject    {id, note}           管理员 → rejected(必填原因)
```
审计动作: `channel.review_submit` / `channel.review_approve` / `channel.review_reject` / `channel.review_stale`(指纹失配时由 `ChannelUpdate` 内联记录, 复用 `audit()`)。

### 3.5 存量迁移(不打扰)
- 启动时一次性回填: 现有 `shared=true` 的渠道 → `approved`(它们本就已在公开面), 其余 → `draft`; 回填动作写审计一条汇总。
- 管理员自有渠道(`UserID` 为 admin)可配置自动通过(设置 `channel_review_admin_bypass`, 默认开), 避免管理员自己也被审核流程卡住。

---

## 4. P2 观察期

### 4.1 语义
- 审核通过的渠道 `TrustLevel=observing`, `ObservationStart=审核通过时刻`, `ObservationUntil=+N 天`;
- 观察期内:**允许发布、允许接流量**(否则没有观测数据), 但:
  1. 告警阈值收紧一档(失败连击阈值 ÷2, 延迟阈值 ×0.5), 便于尽早发现问题;
  2. `MemberScoreView` 增加 `observing` 标注 + 观察期软折减(与可靠性软惩罚同构, 折减幅度固定 0.9), 让它在 `score` 模式里天然略靠后;
  3. 单日成功率跌破红线(默认 90%)或触发宕机告警 → **立即转入 `probation` 并通知管理员**。

### 4.2 毕业与降级(每日评估, 挂 `internal/task` 新任务 `channel_trust_eval`, 每日 01:00)

```
观察期到期:
  成功率 ≥ 毕业线(默认 0.98) 且 样本数 ≥ 最低样本(默认 100) 且 无未恢复告警
      → trust_level = trusted(转正)
  否则:
      首次不达标 → probation, 延长 7 天(只延长一次)
      probation 再不达标 → revoked: 自动 shared=false, 回到 pending 待复审
```

### 4.3 设置项(沿用现有设置表, 不新增配置面)
| 键 | 默认 | 说明 |
|---|---|---|
| `channel_observation_days` | 7 | 观察期天数 |
| `channel_observation_min_samples` | 100 | 毕业最低请求数 |
| `channel_observation_success_floor` | 0.98 | 毕业成功率线 |
| `channel_review_admin_bypass` | 1 | 管理员自有渠道免审 |

### 4.4 数据模型
- 不新建表: 复用 `Channel.TrustLevel / ObservationStart / ObservationUntil` 三列(P1 一并迁移), 升降级写审计(`channel.trust_promote` / `channel.trust_probation` / `channel.trust_revoke`)。

---

## 5. P3 服务质量展示

### 5.1 数据分层(重要: 三种数据源各司其职)
| 层 | 来源 | 用途 | 时效 |
|---|---|---|---|
| 实时指标 | relay `modelMetrics`(EMA) | 管理界面「近况」数字 | 进程内, 重启由 `RouteMetricRecord` 恢复 |
| 日趋势 | `StatsChannelDaily` / `StatsChannelModelDaily` | 30 天趋势曲线 | 持久化, 每日落库 |
| 探测可用率 | `ChannelHealthRecord` | 可用率/延迟(不走转发也能量) | 保留最近 1000 条 |

**新增轻量快照表**(health 记录会轮转, 质量历史需长期保留):

```go
// internal/model/quality.go
type ChannelQualitySnapshot struct {
    ChannelID    int     `json:"channel_id" gorm:"primaryKey;index"` // 渠道 + 日期复合主键
    Date         string  `json:"date" gorm:"primaryKey;size:16;index"` // YYYYMMDD
    RequestCount int64   `json:"request_count"`
    SuccessRate  float64 `json:"success_rate" gorm:"type:decimal(8,6)"`  // 0..1, 来自 StatsChannelDaily
    AvgWaitMs    int64   `json:"avg_wait_ms"`                            // WaitTime/请求数
    UptimePct    float64 `json:"uptime_pct" gorm:"type:decimal(6,3)"`   // 当日探测健康占比
    HealthChecks int64   `json:"health_checks"`
    HealthFails  int64   `json:"health_fails"`
    Grade        string  `json:"grade" gorm:"size:2"` // A|B|C|D, 每日固化
    CreatedAt    time.Time `json:"created_at"`
}
func (ChannelQualitySnapshot) TableName() string { return "channel_quality_snapshots" }
```

生成时机: 现有 `stats_save`/每日核对任务之后, 新任务 `channel_quality_snapshot`(每日 01:30)按 `StatsChannelDaily` + 当日 `ChannelHealthRecord` 汇总 upsert; 保留 180 天(旧数据同模式裁剪)。

### 5.2 质量等级口径(可解释, 与 `score_view` 同源)

```go
// 综合分 = 0.4×成功率维度 + 0.35×延迟维度 + 0.25×可用率维度 (复用 normalizedDim 思路)
// A: 综合分 ≥ 90 且成功率 ≥ 99.5%;  B: ≥ 75;  C: ≥ 60;  D: 其余(或样本 < 30 显示 "--")
```
- 成功率维度: 直接用成功率;
- 延迟维度: `AvgWaitMs` 分段(<2s 满分, 2-5s 线性, >5s 0 分);
- 可用率维度: `UptimePct`;
- **样本不足(当日 < 30 请求)不评级**, 显示 `--`, 避免小样本刷高分。

### 5.3 API

```
GET /api/v1/channel/quality/:id           归属者/管理员: 当前等级 + 成功率 + 延迟 + 可用率 + 近30天快照
GET /api/v1/channel/quality/snapshot/:id  ?days=30        日快照趋势明细
GET /api/v1/share/:code                   (现有接口) 响应新增 quality 字段(脱敏)
```

**对客脱敏口径(公开分享页)** —— 只给区间不给精确值, 防止竞争情报泄露:

```json
"quality": {
  "grade": "A",
  "uptime_band": "99.9%+",           // 99.9%+ | 99%+ | 95%+ | <95%
  "latency_band": "<2s",             // <2s | 2-5s | >5s
  "success_band": "99%+",            // 99.5%+ | 99%+ | 95%+ | <95%
  "observing": true,                 // 观察期标注(透明化, 不隐藏)
  "since": "2026-10-01"              // 质量数据起算日
}
```

### 5.4 前端落点
| 界面 | 内容 | 复用 |
|---|---|---|
| 渠道列表卡片(`channel/Card.tsx`) | 质量徽章: 等级色块 + 成功率% + 平均延迟; `observing` 角标 | `Stats.tsx` 的 `successRate` |
| 渠道详情(新建 `Quality.tsx`) | 30 天趋势(请求量/成功率/延迟三线) + 等级说明 + 观察期进度条(剩余天数 + 当前指标 vs 毕业线) | `useChannelDailyStats` 图表样式 |
| 审核队列(渠道页内 Tab) | pending 列表 + 批准/驳回; 渠道商看自有提交状态 | 设置页审核先例 |
| 公开分享页(`share.html`) | 模型清单上方加质量摘要条(等级 + 三段 band + 观察期标注) | 现有纯前端模板 |

---

## 6. 权限矩阵

| 动作 | 普通用户 | 渠道商 | 管理员 |
|---|---|---|---|
| 提交审核 | — | 自有渠道 | 全部 |
| 批准/驳回 | — | — | ✓ |
| 查看审核队列 | — | 仅自有提交 | 全量 |
| 质量明细(内部) | — | 自有渠道 | 全部 |
| 质量摘要(公开) | 经分享码 | 经分享码 | 经分享码 |

非归属访问继续按 404 语义(`scope.Owns`), 不暴露资源存在性。

---

## 7. 实施顺序与验收

### P1 审核(先行, 1 个版本)
- [x] `Channel` 加审核列 + 指纹; `ChannelPublish`/`ChannelGrantCandidates` 加门禁;
- [x] 四个 review API + 审计;
- [x] 存量回填任务; 前端审核 Tab;
- **验收**: 渠道商提交 → 管理员批准 → 才能发布; 驳回带原因; 改 `base_url` 触发 stale; 存量分组流量零中断。

### P2 观察期(第二个版本)
- [ ] `TrustLevel` 三列 + 四个设置项; `channel_trust_eval` 日任务;
- [ ] 观察期折减进 `score_view`; 前端观察期进度条;
- **验收**: 新审核渠道 7 天内观察期标注可见; 模拟成功率跌破线 → probation; 达标自动 trusted + 审计。

### P3 质量展示(第三个版本)
- [ ] `ChannelQualitySnapshot` + 日快照任务(180 天裁剪);
- [ ] quality API ×2 + 分享页脱敏字段; 前端徽章/趋势/分享页摘要;
- **验收**: 等级与手工计算一致; 样本不足显示 `--`; 分享页只出 band 不出精确值; 三语文案齐全。

---

## 8. 风险与边界

1. **指纹误报**: 价格不入指纹(见 §3.2), 模型清单用「排序后哈希」避免无序抖动;
2. **EMA 进程内指标**: 质量展示以后端持久化统计为准(`StatsChannelDaily`), EMA 仅作实时参考, 重启不回填历史;
3. **小样本刷分**: 样本 < 30 不评级, 观察期毕业另有 `min_samples` 门槛;
4. **公开情报边界**: 分享页只出区间 band 与等级, 不出精确成功率/延迟/请求量;
5. **审核积压**: pending 超过 72h 未审 → 告警(`ChannelAlert` 复用), 不自动通过;
6. **与选路的关系**: 本次不改 `selectByScore` 等 7 种模式的算法, 观察期折减只影响 `score` 模式的排序输入, failover/price 等模式不受影响。

---

## 9. 一句话路线图

**P1 把「发布」变成「审批通过才能发布」→ P2 让新渠道在聚光灯下跑 7 天、达标转正 → P3 把每天的表现固化成可解释的等级, 同时照亮管理员、渠道商和买家三个视角。**
