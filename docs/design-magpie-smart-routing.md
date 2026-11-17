# 移植 magpie 智能路由 —— 方案（先结论，再细节）

> 状态：**方案稿，未实现**。本文所有 magpie 结论都来自源码取证（文件:行号），
> 所有本系统结论都来自当前仓库取证与**生产库实测**。凡是未经实证的判断，文中明确标注「未验证」。

---

## ⚠️ 先看这一节：生产现状推翻了「照搬 magpie」的前提

写方案过程中我查了你的**生产库**（`/opt/switchyard/data/data.db`，2026-11-16 实测），
结论和最初设想差别很大，必须写在最前面：

```
channel_keys 总数: 25
account_kind 分布: "" = 25 个（全部为空，即全是 API Key，没有订阅账号）
account_platform 分布: "" = 25 个
有 2 个 key 的渠道: 只有 1 个（channel 9 / cun.ai）
其余 24 个渠道: 每渠道 1 个 key
```

**三件事因此成立：**

1. **你现在没有「订阅账号池」**。25 个 key 全是第三方中转站（`sharellm.net`、
   `kakouai.com`、`budgetai.cc`、`cun.ai` …）的 API Key，`dialect` 全是 `generic`。
   而 magpie 的智能调度，前提就是**一个上游挂多个订阅账号**。
2. **渠道内多 key 轮询是死代码**（比"用得少"更彻底，已核实）—— `SelectChannelKey`
   在转发主路径**永不执行**（`op/ChannelGrantGet` 恒回填固定 key，见 3.1 更正）。
   24/25 个渠道只有 1 个 key 只是表象；即使有多个 key，那套轮询也不会跑。
3. **第三方中转站读不到额度窗口**。`anthropic-ratelimit-unified-*`、`x-codex-*` 这些
   头是**官方端点**才发的；中转站多半会把它们剥掉或改写。也就是说：
   A 阶段（额度感知）在**当前渠道结构下收益极小**。

**所以原方案（"建额度画像 → 用额度选路"）在你的现状下不成立。**
下面的方案已按实测现状重写。请直接看 **第九节「按你的现状重排的方案」**，
前面部分保留为 magpie 的取证记录（有用，但优先级变了）。

---

## 一、先说结论

### 1. magpie 的「智能」不是排序算法，是**额度感知**

这是本次调研最重要的发现。magpie 有 11 个路由策略（`internal/routes/strategies.go`），
但其中 10 个都只是**同一个排序函数的变体**，真正让它显得"智能"的不是算法，而是
`Seat`（`internal/routes/routes.go:22-130`）里那些**别的网关拿不到的数据**：

| 字段 | 含义 | 我们的系统有吗 |
|---|---|---|
| `Used` | 该模型算的额度已用百分比 0-100 | **没有** |
| `Soon []time.Time` | 各窗口的**重置时刻** | **没有** |
| `Lapsing` | 即将过期作废的额度占比 | **没有** |
| `Pace` | 本周剩余额度按小时摊平的速度 | **没有** |
| `Left` / `LeftIn` | 预付钱包余额及其币种单位 | **没有** |
| `TTFT` / `TPS` | 实测首字延迟/出字速度（按模型） | 部分有（延迟，但未按模型细分） |
| `Free` / `Credits` | 该模型是否免费 / 是否可透支额度 | **没有** |

**magpie 的"智能"= 它知道每个账号的订阅额度窗口什么时候重置、还剩多少、哪部分会作废。**
算法本身（`Smart`，`strategies.go:150-202`）只是把这些数据按"能用→快用完→用完"三档排开。

### 2. 所以我们不需要移植算法，需要移植**数据**

如果直接照搬 `Smart` 的排序逻辑，而 `Used`/`Soon` 全填零，结果会和现在没有区别 ——
所有账号都在第 0 档（"能用"），排序退化成一个不稳定的顺序。
**这是我建议的移植顺序：先建"额度感知"能力，再用它驱动选路。**

### 3. 我们现有的选路已经比 magpie 的"排序"更完整

取证结论（`internal/model/group.go:12-18`、`internal/relay/route.go`）：

- 已有 7 种分组模式：`manual` / `failover` / `price` / `latency` / `success` / `score` / `random`
- `score` 已经是**多维加权评分**（价格+延迟+成功率，权重可配，`GroupRelayConfig.ScorePriceWeight` 等）
- 已有**灰度分流**（金丝雀，`CanaryItemID`/`CanaryPercent`）与**成员亲和**（`MemberAffinitySeconds`）
- 已有**冷却**（`MemberCooldownSeconds`）与**熔断**（key 级 + model 级，含 trips 翻倍与硬熔断）
- 已有**多级重试**（`MaxTotalAttempts` / `MemberMaxAttempts` / 跨成员轮次）
- 已有**用户侧偏好覆盖**（`routing_preference`）

**这些 magpie 反而没有**：magpie 的策略是"纯排序"，没有灰度、没有价格维度
（`Cheapest` 只是按静态价排，`strategies.go:603`），也没有多维加权评分。

### 4. 因此方案分三层，优先级从高到低

| 层 | 内容 | 价值 | 成本 |
|---|---|---|---|
| **A** | 额度感知（采集每个订阅账号的窗口用量与重置时刻） | ★★★★★ | 中高 |
| **B** | 基于额度的选路策略（把 A 的数据接入现有 score） | ★★★★★ | 中 |
| **C** | 补齐 magpie 的几个纯排序策略（Drain/Rotate/FreeFirst） | ★★ | 低 |

**强烈建议只做 A+B，C 可选。** 理由：C 里那些策略在我们已有 score 模式下是冗余的，
而 A+B 才是真正缺失的能力。

---

## 二、magpie 到底怎么选路（取证）

### 2.1 分层架构

```
请求 → provider（一个上游厂商）→ 该 provider 的所有 key/账号 = 一个 pool
                                    ↓
                              routes.Strategy 只负责「排序」
                                    ↓
                     gateway 负责「休息、亲和、故障切换」（不在 Strategy 里）
```

关键设计：`routes` 包注释明确说了分工 ——
> "The gateway lists the pool, rests, affinity and failover itself; a Strategy only says the order."
> —— `internal/routes/routes.go:1-11`

**这个分工值得照搬**：策略只排序，生命周期（休息/亲和/切换）在网关层。
我们现在其实是反的 —— 选路逻辑散在 `handler.go` 的重试循环里。

### 2.2 十一个策略（全部只是排序函数）

| 策略 | 位置 | 排序依据 |
|---|---|---|
| `Smart`（默认） | `strategies.go:150` | 三档：能用 → 快用完 → 用完；同档内按重置时刻、作废时刻、余额 |
| `Ordered` | `:235` | 按配置顺序，第一个直到失败 |
| `Drain` | `:278` | 专挑"已耗尽"的，用于主动清空即将作废的额度 |
| `Rotate` | `:306` | 均摊请求数 |
| `LeastUsed` | `:330` | 已用最少优先 |
| `Pace` | `:416` | 剩余额度/剩余小时，避免前期用完 |
| `Weighted` | `:473` | 平滑加权轮询（NGINX 算法，`round.go`） |
| `FreeFirst` | `:509` | 该模型免费的账号优先 |
| `FreeExpiring` | `:538` | 免费/试用账号优先消耗 |
| `Cheapest` | `:603` | 该模型在此账号上的成本最低 |
| `Fastest` | `fastest.go:60` | TTFT/TPS 实测最快 |

### 2.3 Smart 的具体排序（这是最有价值的）

`strategies.go:150-202`，逻辑照抄如下：

1. 按 `seatTier` 分三档（`:48-56`）：
   - **0 档（能用）**：`Used < 90%`；但 `First`（用户指定优先花完）或 `WeekLow` 也算 0 档
   - **1 档（快用完）**：`Used >= 90%`
   - **2 档（用完）**：`Used >= UsedShare`（`provider.SpentShare`）
2. **0 档内部**排序（`:163-198`），依次比较：
   - `Learns` 优先（能从响应里学到额度的账号优先，因为它会自我修正）
   - 各窗口重置时刻（**按小时截断**，避免几分钟差异导致抖动）
   - 作废时刻 `Lapse`（同样按小时截断）
   - 钱包余额（`byLeft`，`:69-107`）
3. **1 档内部**按 `Used` 升序（`:199`）
4. **2 档内部**按 `spentBefore`（`:227-232`）：能透支的先，然后剩余多的先
5. 最后 `leadFirst`（`:209-222`）：用户设为 `UseFirst` 的 provider 提到前面

**三处值得学的工程细节**：
- **按小时截断时间**（`Truncate(time.Hour)`）—— 防止毫秒级差异把稳定顺序打乱
- **"未上报"排在"已上报"之后**（`x.IsZero()` 分支）—— 信息缺失时保守处理
- **`Learns` 优先** —— 会自我修正的账号值得优先试（`:148-149` 注释解释了为什么）

### 2.4 故障分类（这才是 magpie 最厚的部分）

`internal/gateway/routing.go:570` 的 `failure(status, body)` 把上游响应分类成不同的
**失败原因**，每种对应不同的休息时长。这是 2733 行文件的主体。

分类维度（不是简单的 4xx/5xx）：
- **`failProxy`**（`:572-580`）：502 但其实是代理没连上 —— **不能算账号失败**。
  注释记录了踩坑：误判为账号失败导致 ChatGPT 账号被休息到窗口重置（"days off"）。
- **`failVerify`**（`:581-583`）：401/403 + 验证挑战
- **`failCut`**（`:587-593`）：流中途断开，**不算任何人的错**，重试且不休息
- **`failQuota`**（`:600-615`）：额度用完（能从文案里读出重置时间）
- **`failCredit`**（`:607-609`）：余额/信用用完
- **`failRate`**（`:616-630`）：限流，几分钟后回来（区分"每分钟限流"与"计划用尽"）

**关键洞察**：同样的 HTTP 状态码，含义可能完全不同。
- 429 + "resets in 1 minutes" → 限流，1 分钟
- 429 + "weekly limit" → 计划用尽，休息到窗口重置（可能几天）
- 502 + 代理错误 → **账号无辜**，不该休息

我们现在其实**已经比初稿设想的更好**：`classifyUpstreamError`
（`internal/relay/upstream_error.go:29-40`）已经分了 10 类
（client/policy/auth/quota/rate/overload/model/transient/compatibility/unknown），
且有可扩展的规则表（`errorRules`，`:54`）。

但仍有**一个真实缺陷**（已核实，不是推测）：

代理/网络类错误被归入 `transient`（规则 `network_transient`，`:112-115`，
tokens 含 "timeout"/"connection refused"/"tls handshake" 等），而 `transient`
在 `internal/relay/handler.go:626-627` 会调用 `RecordChannelModelError`
—— **即代理故障会污染渠道模型的健康分并触发模型级熔断**。

比初稿判断的要轻（不是熔断整个账号 key，只熔断"该渠道的该模型"），
但仍然错：代理是本地线路问题，换代理就好了，不该让一个本来健康的模型
因为走了坏代理而被标记为故障。GLM/DeepSeek 这类渠道同时挂多个出口时，
一个坏出口就能把某个模型的健康分拉下来。

### 2.5 额度数据从哪来（决定性）

`Seat.Used`/`Soon`/`Lapsing` 全部来自 `provider.Allowance`
（`internal/provider/routing.go:348-394`），而 `Allowance` 的来源是：

1. **上游响应头**（被动学习）：`claude_limits.go` 注释
   > "rate_limit_event, from Anthropic's anthropic-ratelimit-unified-* headers"
   即每次请求的响应里就带着"五小时窗口用了多少、什么时候重置"。
2. **主动查询额度接口**（`usageapi` 包）：调厂商的额度查询端点。

**这就是为什么 magpie 能"智能"**：它把这两个来源的数据攒起来，形成对每个账号的
实时额度画像。我们的系统在 `channel_keys` 里**完全没有这些列**
（`internal/model/channel.go:137-162`，只有统计计数与熔断字段）。

---

## 三、我们系统的现状（取证）

### 3.1 选路的实际位置

选路不是集中在一处，而是散在**请求重试循环里**（`internal/relay/handler.go:278-380`）：

```
每轮重试:
  GroupGetByName(model)                    // ← 每轮重新读库，配置热更新
  effectiveGroupForRequest(...)            // ← 用户偏好覆盖
  scoreRetries.filtered(group)             // ← 评分模式过滤
  pickCanaryItem(group) 或 pickGroupItem   // ← 灰度优先，否则策略选路
  SelectChannelKey(channelID, strategy)    // ← 渠道内选 key
```

`SelectChannelKey`（`internal/op/channel.go:1472-1511`）有三档
（`failover`/`random`/`roundrobin`），但**它在转发主路径上是死代码**（已核实，见下方更正）。

#### ⚠️ 更正：渠道内选 key 不是"原始"，是**不可达**

我初稿写的"渠道内选 key 极原始（只有轮询/随机）"**是错的**。核实结论：

1. `ChannelGrant` 的 `ChannelKeyID` 是 `not null` + `unique(channel_model_id, channel_key_id)`
   （`internal/model/channel.go:176-178`）—— **每个 Grant 天生绑定且只绑定一个 key**。
2. `op.ChannelGrantGet`（`op/channel.go:573-581`）每次返回前都回填 `grant.ChannelKey`，
   且 key 已禁用时直接报错返回。所以 `handler.go:368` 的
   `if grant.ChannelKey != nil && grant.ChannelKey.Enabled` **恒为真**。
3. `SelectChannelKey` 的调用点只剩 `op/channel_quota_test.go:81,122`（测试）。
   `channels.key_rotation_strategy` 与 `channel_keys.priority` 在转发中**不生效**。

**那渠道内多 key 怎么办？** 架构上是这样的：同一渠道的 2 个 key
→ 2 个 Grant → 分组里的 **2 个独立成员**，在**分组层**由 7 种模式之一选出。
也就是说：**key 级调度本来就该在分组层做，`SelectChannelKey` 是历史残留。**

**这个更正改变了方案**：原 B1（"给 SelectChannelKey 加 quota 策略"）**做在死代码上，
白做**。正确做法是让额度数据进入**分组层的 score 维度**（原 B2），不需要碰 `SelectChannelKey`。

### 3.2 已有的可复用资产

| 资产 | 位置 | 能否复用 |
|---|---|---|
| 熔断状态机（trips 翻倍、硬熔断、探测） | `op/channel.go:1515+` | 可复用，需扩展失败分类 |
| 分组 score 评分 | `relay/route.go:359-530` | **强烈建议复用**，加一个"额度"维度 |
| 成员亲和 / 冷却 | `GroupRelayConfig` | 可复用 |
| 灰度分流 | `route.go:109` | 可复用 |
| 统计指标（延迟/成功率滑动窗口） | `StatsMetrics` + `metric_window_size` | 可复用 |
| （对照）额度窗口数据 | **不存在**。`StatsMetrics`（`model/stats.go:3-11`）只有累计计数与成本，无任何窗口/重置字段 | **必须新建** |

**结论：我们不需要新造选路框架，只需要给现有 score 加维度 + 给渠道内选 key 加策略。**

---

## 四、方案

### 阶段 A：额度感知（基础，必做）

**目标**：让系统知道"每个订阅账号的额度窗口用了多少、什么时候重置"。

#### A1. 新增额度快照表

```sql
CREATE TABLE channel_key_allowances (
    id            INTEGER PRIMARY KEY,
    channel_key_id INTEGER NOT NULL,
    model_pattern TEXT,          -- 该窗口只统计哪些模型（空=全部）
    used_share    REAL,          -- 0-100
    resets_at     DATETIME,      -- 窗口重置时刻
    span_seconds  INTEGER,       -- 窗口长度
    lapsing_share REAL,          -- 即将作废占比
    balance       REAL,          -- 预付余额
    balance_unit  TEXT,
    source        TEXT,          -- 'header' | 'query'
    observed_at   DATETIME NOT NULL,
    UNIQUE(channel_key_id, model_pattern)
);
```

**为什么单独建表而不是加列到 `channel_keys`**：
额度是**多窗口**的（5小时窗口 + 周窗口 + 预付钱包同时存在，magpie 的
`Allowance []Limit` 就是数组）。加列只能存一个窗口，必然要重做。

#### A2. 采集路径有两条，都要

**被动（响应头）** —— 成本最低，覆盖面最广：
- 在 `sendPassthrough`/`sendConverted` 拿到响应后，解析响应头
- Anthropic: `anthropic-ratelimit-unified-*`
- Codex: `x-codex-*`
- Gemini/Antigravity: 响应体里的额度字段（我们已有 `RetrieveUserQuota`，
  `internal/op/geminicli.go:402`，可直接复用）
- **这条路径几乎所有平台都能覆盖**，且零额外请求

**主动（查询接口）** —— 定时任务，作为兜底：
- 我们已有 `account_refresh` 定时任务的骨架（`op/account_refresh_run.go`）
- 加一个"额度轮询"任务，复用同一个调度器

#### A3. 关键设计：额度数据是「尽力而为」，缺失不能阻塞

magpie 的做法值得照抄：`Seat.Known` 标记额度是否已知，未知的账号在 Smart 里
**排在已知的之后但不排除**（`strategies.go:182-183`）。

理由（`routes.go:36-41` 的注释）：一个额度未知的账号如果被完全排除，它就永远不会被请求，
也就永远拿不到额度数据 —— **死锁**。

### 阶段 B：额度驱动的选路（必做）

#### ~~B1. 扩展渠道内 key 选择~~（**取消，做在死代码上**）

原计划给 `SelectChannelKey` 加 `quota` 策略。**已核实该函数在转发主路径不可达
（见 3.1 的更正）**，改它不会产生任何效果。**这一项取消。**

若确实需要"按 key 调度"，正确入口是分组层：同一渠道的两个 key 是两个 Grant、
两个分组成员，在 `selectGroupItem`（`relay/route.go:271`）里被择优。
所以 B1 的内容全部并入 B2（给 score 加额度维度）。

（下面保留原 B1 的设计，仅作为"如果将来把 Grant 改成可绑多 key"的参考，**当前不执行**。）

#### B1'（参考，不执行）扩展渠道内 key 选择

现在 `SelectChannelKey(channelID, strategy)` 只有 3 种。**新增第 4 种 `quota`**：

```
quota 策略 = magpie 的 Smart，简化为 2 档：
  1. 排除熔断中的（现有逻辑保留）
  2. 按 (额度未知, used_share, resets_at) 排序：
     额度充足 → 额度偏低 → 额度耗尽
     同档内：重置更晚的优先（先花不会作废的）
```

**为什么简化成 2 档**：magpie 的 3 档是配合它的 `First`/`WeekLow` 语义，
我们没有那些开关，2 档已够；`Drain` 那种"专挑快用完的"可以作为独立策略后加。

#### B2. 给分组 score 加「额度」维度（可选，但价值大）

现在 score 是 3 维（价格/延迟/成功率，权重可配）。加第 4 维：
- `ScoreQuotaWeight`（0-100）
- 维度值 = 该成员对应渠道 key 的剩余额度（取均值或最差）

**风险**：加维度会改变所有现有分组的行为。**建议默认权重 0（不生效）**，
让用户显式开启。

#### B3. 失败分类升级（顺手修一个真 bug）

新增一个独立的 `ErrorClassProxy`，把 `network_transient` 里**属于本地线路**的那几条
（"connection refused"、"tls handshake"、"no such host"、代理 502）单独归出来，
不再计入 `RecordChannelModelError`，改为只记一次**出口**失败
（`recordEgressSample` 已有，`handler.go:619`）。

为什么这样归：出口成绩本来就是"这条线路行不行"（该行注释原文），
与模型健康互不替代。现在代理错误两头都记，把两个维度的语义混了。

magpie 为此专门写了 `failProxy` 分类并记录了踩坑（`gateway/routing.go:572-580`：
误判导致 ChatGPT 账号被休息到窗口重置，"days off"）。我们虽然没到那个严重程度，
但性质相同。

**这是一个独立的、可立刻修的缺陷**，不必等额度功能。

（注：初稿里写的"账号被无辜休息"不准确 —— 实际是渠道**模型**级熔断，
不是账号 key 级。已在 2.4 节更正。）

### 阶段 C：补齐纯排序策略（可选，低价值）

`Drain` / `Rotate` / `FreeFirst` / `FreeExpiring` / `Pace`。

**坦白说这部分价值不大**：
- `Rotate` ≈ 现有 `roundrobin`
- `FreeFirst`/`FreeExpiring` 需要"该模型是否免费"的元数据，我们还没有
- `Drain` 需要额度数据才有意义（依赖 A）
- `Cheapest` ≈ 现有 `price` 模式

**建议：A+B 上线并稳定后，再按实际需要挑，不要一次全上。**

---

## 五、不建议照搬的部分（明确说明理由）

### 5.1 magpie 的策略注册机制

`routes.go:151` 的策略注册表 + `Register(name, Strategy)` 是为了**插件运行时**
（注释提到 `docs/subsystems/plugin-runtime.md`）。我们没有插件系统，
硬上会引入一套没人用的抽象。**直接 switch 分支即可。**

### 5.2 `Seat` 结构照搬

`Seat` 有 30+ 字段（`routes.go:22-130`），其中 `Learns`/`Credits`/`First`/`Lead`/
`Unreached`/`WeekLow` 全是为了处理特定厂商的特殊语义（Codex 的信用透支、
Claude 的周窗口、用户手动设的优先级）。

**照搬会导致大量字段永远为零值**，反而增加理解成本。建议只取：
`Used` / `Soon` / `Lapsing` / `Left` / `TTFT` / `TPS` / `Known` —— 7 个够用。

### 5.3 故障分类的完整正则集

`routing.go` 里的正则（`resetsWhen`/`minuteRate`/`creditWords`...）是**针对
各家上游文案逐条打磨出来的**（注释里全是真实案例与用户反馈）。
我们上游渠道杂，但没必要一次全抄 —— **先抄 `failProxy` 与 `failQuota` 两条**，
其余按实际遇到的错文本来加。

---

## 六、实施顺序与工作量

| 阶段 | 内容 | 预估 | 依赖 |
|---|---|---|---|
| 0 | **修代理失败误伤账号**（独立 bug） | 0.5 天 | 无 |
| A1 | 额度快照表 + 迁移 | 0.5 天 | 无 |
| A2 | 被动采集（先做 Gemini/Antigravity，我们有现成 RPC） | 2 天 | A1 |
| A2' | 被动采集（Anthropic/Codex 响应头） | 1.5 天 | A1 |
| A3 | 主动额度轮询定时任务 | 1 天 | A1 |
| ~~B1~~ | ~~`quota` 选 key 策略~~ **取消**（该函数是死代码，见 3.1 更正） | ~~1.5 天~~ 0 | — |
| B2 | score 加额度维度（默认关闭） | 1 天 | A1 |
| B3 | 失败分类扩展 | 1 天 | 无 |
| C | 补齐排序策略 | 按需 | A |

**总计约 8-9 个工作日（不含 C）。**

**建议的最小可用切片（MVP）**：阶段 0 + A1 + A2（只做 Gemini） + B1。
约 4.5 天，就能解决"渠道内多账号不看额度乱轮询"这个最主要的问题。

---

## 七、必须先确认的问题

1. **额度数据可得性（未验证，最关键）**
   我能确认 Antigravity/Gemini 的 `retrieveUserQuota` 可用（上一个功能已验证）。
   但 **Anthropic 的 `anthropic-ratelimit-unified-*` 头在我们当前的上游渠道上
   是否真的出现**、Codex 的 `x-codex-*` 是否出现 —— 需要拿真实渠道打一次看响应头。
   **如果某些渠道拿不到，那些渠道就只能用 `Known=false` 走保守排序。**

2. **「额度」与「账单」的关系**
   我们是中转站，有 `supply_price`（供货价）与用户价。额度感知会不会和结算冲突？
   我的判断是不会（额度只影响选路，不影响计费），但这需要确认。

3. **是否要做 B2（改 score）**
   这会改变现有用户的行为。建议单独确认，且默认关闭。

4. **范围确认**
   你希望做完整 A+B，还是先做 MVP（阶段 0 + A1 + A2 Gemini + B1）？

---

## 八、一句话总结

**magpie 值钱的不是那 11 个排序策略，是它给每个账号维护的额度窗口画像。**
我们的选路框架（7 种模式、score 加权、灰度、亲和、熔断）其实更完整，
缺的是渠道内选 key 的策略（现在只有轮询/随机）和额度这个数据维度。

**移植 = 建额度画像（A） + 把它接进分组层的 score 维度（B2）**，
顺手修掉代理失败误伤账号的 bug。其余可选择性跟进。

---

## 九、按你的现状重排的方案（**这才是要执行的**）

### 9.1 现状复述（实测）

| 事实 | 数据 | 含义 |
|---|---|---|
| 渠道数 | 25（其中启用 20） | 上游资源来源多而杂 |
| key 总数 | 25 | — |
| 每渠道 key 数 | 24 个渠道各 1，1 个渠道（cun.ai）2 | **渠道内选 key 是死代码**（见 3.1 更正），有无多 key 都不跑 |
| key 类型 | 全部是 API Key（account_kind 空） | **没有订阅账号** |
| 上游性质 | 第三方中转站为主 | **读不到官方额度头** |
| 分组 | 7 种模式已实现（score 多维加权） | **跨渠道选路已经很完整** |

### 9.2 所以真正的短板顺序变了

原方案认为短板是"额度感知"。**按实测，短板排序应该是：**

| 排名 | 真实短板 | 为什么 |
|---|---|---|
| **1** | **上游是第三方中转站，质量不可控** | 25 个 key 里绝大多数是中转站。它们的限流、超时、断流、模型缺失、突然跑路——这才是你日常真正遇到的问题。而现在系统对它们的判断依据只有"成功率滑动窗口"。 |
| **2** | **故障分类把不同性质的问题混在一起** | 已核实：代理/网络错误归入 transient，会触发 RecordChannelModelError（handler.go:626-627）。中转站的"模型不存在"、"并发超限"、"余额不足"也被粗分。 |
| **3** | **同一模型在多渠道间缺少"该信谁"的长期记忆** | 现在 score 用滑动窗口（默认 metric_window_size），窗口外的历史被丢弃。中转站的特点恰恰是"长期不稳、偶尔很好"。 |
| 4 | 渠道内多账号额度调度 | 你现在只有 1 个渠道有 2 个 key。**做这件事现在几乎不产生收益。** |

### 9.3 重排后的方案

#### 阶段 1（首选，价值最大）：中转站健康度画像

**目标**：让系统对每个"渠道 × 模型"积累长期行为特征，而不只是滑动窗口成功率。

要记的维度（都比额度更容易拿到，因为**从响应本身就能推断**，不需要额外接口）：

- 失败原因分布（按 ErrorClass 计数，长期累积）：auth / quota / rate / model_missing / overload / transient / proxy
- 各类失败的时间衰减权重（最近的权重高，但旧的不清零）
- 响应特征：平均 TTFT、平均出字速度、断流率
- 能力实测：是否支持流式、是否支持某协议

**为什么这个价值最大**：中转站的问题**不需要问它就能知道** —— 它每次失败都在告诉你。
你现在的系统记了 StatsMetrics 的累计计数（request_success / request_failed），
但**没有记"为什么失败"**。加一张按 ErrorClass 分解的表，成本低、立刻能用。

#### 阶段 2：把画像接入选路（复用现有 score，不改架构）

现有 selectByScore（internal/relay/route.go:359）已经是多维加权。
**加一个维度，不需要新框架**：

- 新增 ScoreHealthWeight（默认 0 = 不生效，用户显式开启）
- 维度值 = 该渠道模型的历史可靠度（阶段 1 的数据算出）
- 关键改进：把"**中转站特有的失败**"（断流、模型缺失、突然 5xx）**加权更重**，
  因为这类失败往往预示它要挂了

#### 阶段 3（可选，等你真的开始用订阅账号时再做）：额度感知

也就是原方案的 A+B。**触发条件很明确**：

- 当你开始往一个渠道里挂**多个订阅账号**（Gemini / Antigravity / Claude / Codex 的 OAuth 账号）
- 且至少 2 个账号挂在同一渠道

在那之前，A 阶段做了也用不上。**现在不要做。**

#### 阶段 0（顺手，独立小修）：代理错误不该污染模型健康分

见第 2.4 节与 B3。**这个与中转站画像无关，可以立刻做**。
原因：你有一批渠道配置了日本代理（之前实测有 7 条相同 egress 记录），
代理线路抖动会误伤模型健康分。

### 9.4 magpie 里真正值得抄的三件事（去掉了不适用的）

| 值得抄 | 出处 | 为什么 |
|---|---|---|
| **失败语义分类的思路** | gateway/routing.go:570 failure() | 同状态码不同含义。这个思路我们已经部分有了（10 类），只需继续细化 |
| **"未知信息"排后但不排除** | strategies.go:182-183 | 避免死锁。任何新维度都要遵守这条 |
| **时间按小时截断防抖动** | strategies.go:172-176 | 选路稳定性细节，成本极低 |

**不值得抄**：11 个排序策略（我们 7 种模式更完整）、Seat 30+ 字段（大量会永远为零）、
策略注册表（没有插件系统）、额度窗口模型（你暂时没有订阅账号池）。

### 9.5 一句话

**原判断"照搬 magpie 的智能调度"不成立 —— 因为你上游主要是第三方中转站，
没有订阅账号池，magpie 的核心机制（额度窗口调度）在这里没有输入。**

**真正该做的是：给中转站建健康度画像（记"为什么失败"），接进已有的 score 选路。**
这比移植 magpie 更贴你的问题，成本也更低。

### 9.6 需要你确认

1. 你的上游**是否打算继续以中转站为主**？如果打算加官方订阅账号，那 A 阶段要回来。
2. 现阶段你最痛的是"某个中转站经常抽风但我不知道是哪家/哪个模型"吗？
   如果是，阶段 1+2 就是对的；如果痛点是别的（比如成本、速度），方案要改。
3. 是否先做阶段 0（代理错误归因修复）这个独立小修？
---

## 十、补充取证（另一路独立复核的结论，已核对）

### 10.1 选路只有一个决策点（这是扩展点的准确位置）

`selectGroupItem`（`internal/relay/route.go:271-309`）是"模式 → 算法"的唯一分发表，
输入是 `pickGroupItem`（`route.go:130`）已过滤好的 `[]model.GroupItem`，输出**恰好一个成员**。

新增一个策略 = 三处改动，没有第四处：

1. `internal/model/group.go:11-19` 加 `GroupMode` 常量
2. 同文件 `:57` 的 `IsValidGroupMode` 与 `:272` 的 `binding:oneof` 清单加进去
3. `route.go:271` 加一个 `case`

### 10.2 可直接复用的现成打分件（做 B2 时不要重写）

| 件 | 位置 | 用途 |
|---|---|---|
| `memberPrice` | `route.go:312` | 用户价（含 FX 与上浮） |
| `memberScopeMetric` | `route.go:348` | 延迟/成功率 + 样本数，**按渠道模型全局共享** |
| `normalizedDim` | `route.go:515` | 候选集内 min-max 归一化（新维度直接套用） |
| `reliabilityPenalty` | `route.go:446` | 成功率置信度软惩罚（样本少时惩罚轻） |
| `observationPenalty` | `route.go:415` | 观察期折减 0.85/0.70/0.50/1.0 |
| `effectiveScoreWeights` | `route.go:475` | 三级权重解析（分组 > 系统设置 > 内置） |

**这是 B2 的实际工作量来源**：加一个维度 ≈ 新增一个 `*Weight` 配置 + 一次 `normalizedDim` 调用 +
乘性惩罚，其余链路不动。比我原先估的 1 天还要少。

### 10.3 三处"有表但选路不读"的半成品（做方案时不要重复造）

| 字段/表 | 现状 | 对方案的意义 |
|---|---|---|
| `quality_weight_success/latency/uptime` | 只被 `QualityGradeOf` 读，结果只进快照与展示 | **别复用它做选路权重**，它是展示口径 |
| `channel_quality_snapshots.Grade` | 展示/市场页，选路零引用 | 同上 |
| `channel_keys.priority` / `channels.key_rotation_strategy` | UI 与 OpenAPI 有，转发不可达 | 见 3.1，**不是可用扩展点** |
| 在途并发 `Limiter.active` | 有真实计数，但选路只用到布尔 `Blocked` | 想做"负载感知"要先暴露余量 |

### 10.4 两条独立取证交叉验证一致的关键事实

两路取证（我自己读源码 + 独立复核代理）在下面三点上完全一致，可以当作结论使用：

1. 现有"智能"只有三维：**用户价 + 延迟 EMA + 成功率 EMA**（不是成本，不是额度）
2. key 级调度不可达（死代码）
3. magpie 的核心输入（`Used`/`Soon`/`Lapsing`/`Pace`/`Left`）在我们的表里**一个都没有**
