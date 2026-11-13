# 模型广场(用户侧模型选路入口)设计方案

> 状态: **P1 已发版(v1.5.41, 成绩口径门槛随 v1.5.42 修正); P2 已发版(v1.6.0)**; P3(品牌人工覆盖表 / 日级口径回落 / 质量带)仍为规划稿, 见 §10.3 与 §10.4。
> P1 的代码落点见 §3 各小节末尾的「落地」行; 与规划稿的偏差见 §10「实施记录」。
> 目标: 把「哪些模型现在真的在卖、多少钱、跑得怎么样」一次性摆到用户面前, 并允许用户从广场直接把模型加进自己已经配好的模型配置(分组路由)。
> 基于现有渠道授权 / 定价折算 / 路由指标 / 分组写入的代码事实设计, 按 P1→P3 给出数据模型、API、门禁、前端落点与验收标准。
> 姊妹篇: 《渠道准入审核 + 观察期 + 服务质量展示》(v0.41.0 已上线) 解决供给侧「渠道从哪里来、能不能信任」; 本篇解决需求侧「买家用哪个模型、加进哪条路由」。
> 口径来源文档: `docs/channel-pricing.md`(价格折算与上浮)、`docs/score-routing.md`(评分与选路口径)。
> 落地约定: 开工时在 `docs/backlog-unimplemented.md` §1 登记「有设计稿, 未实施」, 交付后在 §4 交付索引补版本行, 并把本节头部状态改为"已实现并上线 (vX.Y.Z)"。

## 0. 结论先行

| 需求 | 技术上可行? | 结论 |
|---|---|---|
| 列出「所有已发布在用」的模型 | **可行, 零新数据** | 供给面的可售集合早已有唯一真源: `channel.Shared && ReviewStatus==approved && channelModel.Listed`(+`channel_grants` 一条授权)。生产快照实测: 19 个已发布渠道 / 116 条上架行 / 110 条有授权 / 折叠成 **46 个模型名** |
| 展示价格、渠道代码、运行状态 | **可行, 全部现成** | 价格走 `ledgerModelPrices`+ `ChannelMarkupFor` 的既有口径(与计费/选路同一个数); 渠道代码就是 `#1001` 形式的 `share_code`; 运行状态 = 候选里那条 `Available` 表达式 + relay 的 EMA 指标 |
| 用户把模型加进「已配置的模型配置路由」 | **可行, 复用写路径** | 加成员本质是 `GroupUpdate` 的 `items` 整体替换。广场只需读出现有成员、按授权主键去重追加, 再调既有 `op.GroupUpdate` —— 校验、事务、审计、SSE 事件全部复用, 不新增写语义 |
| 按厂商品牌 / 模型名称筛选 | **部分可行, 需定口径** | 后端**没有**品牌字段。`model_capabilities.family` 只能解析出 34/46 个模型, 名称关键词再兜底 1 个, 剩 **11 个无任何依据**(实测品牌分布: anthropic 8 / deepseek 8 / openai 7 / xiaomi 3 / minimax 2 / google 2 / zhipu 2 / alibaba 2 / xai 1 / other 11)。P1 先用「family → 名称关键词 → 其他」三段解析, P3 再补人工覆盖表 |
| 按价格 / 最近成功率 / 延迟排序 | **可行, 但部分模型无数据** | 价格 100% 可排; 成功率与延迟来自 relay 进程内 EMA, **重启即清零**; 持久化的 `stats_channel_model_dailies` 近 7 天只覆盖 **12/46** 个模型名。故 P1 只对有样本的模型排序, 冷启动模型一律置底并显示 `—` |

一句话: **给已发布在用的模型做一张"挂牌表"(价格/渠道代码/状态/近期成绩), 用户点一下就能把它塞进自己已有的路由配置里 —— 全部复用既有口径与既有写路径, 不新增任何供给面暴露。**

设计铁律(与既有资金/运营/路由改造一致):
1. **口径单点**: 广场的「可售」「可用」「价格」三件事一律复用既有函数, 不另写一套判定。发现两处口径分叉时, **先把口径抽成一处再动广场**, 不复制表达式;
2. **不新增暴露面**: 广场字段集必须是 `ChannelGrantCandidates` 的子集 —— 上游渠道真名、凭据名、渠道归属一律不出广场; 已发布渠道只出 `#code`;
3. **只读不落库**: P1 不新建表、不写审计、不改任何计费不变量; 加分组(P2)复用 `GroupUpdate` 的既有事务与事件, 不自建写路径;
4. **指标可解释**: 成功率/延迟必须带样本数与「冷启动」标注, 无样本不给数字只给 `—`, 与 `model.QualityMinSamples=30` 的"样本不足不评级"同源;
5. **零价不霸榜**: 全零价模型(`step-3.7-flash` / `step-5`)在价格排序里置底并标「未定价」, 否则价格优先的排序会被免费模型占满 —— 与牌价缺失时"价格清零必标 `fx_missing` 且按不可用处理"是同一个教训。

---

## 1. 现状盘点(代码事实)

### 1.1 「已发布在用」的判定链(唯一真源)
| 环节 | 位置 | 事实 |
|---|---|---|
| 渠道发布 | `op.ChannelPublish`(`internal/op/channel.go:656`) | 置 `shared` + 生成 4 位 `share_code`(界面固定展示 `#1001`) |
| 审核闸口 | `channel.ReviewStatus == model.ChannelReviewApproved` | v0.41.0 起强制 |
| 模型上架 | `channelModel.Listed` | 逐模型开关 |
| 可加入路由的单位 | `model.ChannelGrant`(`internal/model/channel.go:160`) | 生产实测 **每条上架模型恰好 1 条授权**(486 条授权 vs 504 条渠道模型) |
| 四处一致性 | `op.ChannelGrantCandidates`(L589) / `op.ChannelModelBriefs`(L1481) / `op.validateGroupItemAccess`(L145) / `op.groupSnapshot`(L355) | 前三处要求 `shared && approved && listed`(`validateGroupItemAccess` 少查审核位, 见 §8 风险 1); `groupSnapshot` 只查 `shared && listed` |

### 1.2 生产快照实测(build/prod-snap-latest.db, 2026-11-08)
| 指标 | 值 | 说明 |
|---|---|---|
| 渠道总数 / 已发布过审 | 25 / **19** | 编码 `1001,1002,1004–1008,1010–1012,1014,1024–1027,1029,1030,1034,1036` |
| 上架行(`shared+approved+listed`) | **116** | 折叠大小写后 48 个模型名 |
| 有授权的上架行 | **110** | 折叠后 **46** 个模型名; 19 个名字跨 ≥2 渠道(最多 8 个渠道) |
| 上架但无授权 | 6 | `gpt-5.6-luna`/`gpt-6-luna`@#1006, `gpt-6-astra`/`grok-4.6`/`grok-4.7`/`minimax-m3`@#1025 —— 有牌无位, 不可加入路由 |
| 可用授权(`Available`) | **106 / 110** | 4 条不可用全部在 `#1005`(整渠道 `enabled=0`) |
| 全零价行 | 2 | `step-3.7-flash` / `step-5`@`#1011`(media 价也全零, `kind` 为空) |
| 非 USD 报价 | 2 | `deepseek-v4.1-flash` / `deepseek-v4.1-flash-eco`@`#1036`(CNY, 已由既有折算转 USD) |
| 大小写重名 | 2 组 | `deepseek-v4.1-flash`/`DeepSeek-V4.1-Flash`、`minimax-m3`/`MiniMax-M3` |
| `model_capabilities` | 3951 行 | 2274 条带厂商前缀; `family` 覆盖 34/46 个在售模型名 |

### 1.3 价格口径(现成, 不重算)
`channel_models.supply_price`(JSON `LLMPrice{input,output,cache_read,cache_write}`, 每百万 token)+ `price_currency`
→ `op.ledgerModelPrices`(`internal/op/pricing_fx.go:66`) 折成账面币种 USD(USD 命中零折算、不查表; 非 USD 走内存牌价快照 `fxCache`)
→ `.Scale(op.ChannelMarkupFor(channelID))`(`internal/op/channel_markup.go:13`, 渠道 `MarkupRatio` 优先、否则全局 `SettingKeyMarkupRatio`)
→ `UserPrice`。媒体模型走 `MediaPrice{per_image,per_video,per_second,resolution}`。
**结论: 广场展示的每一个价格都是分组页与计费用的同一个数**, 不存在"广场便宜、选中变贵"的缝。

### 1.4 运行状态与指标(分层, 各有各的时效)
| 层 | 来源 | 时效 | 广场用途 |
|---|---|---|---|
| 可用性 | `ChannelGrantCandidate.Available` = `fxOK && channel.Enabled && !isCircuitBroken(channel.DisabledUntil) && key.Enabled && !isCircuitBroken(key.DisabledUntil) && !isCircuitBroken(model.DisabledUntil)`(`isCircuitBroken` at L1747, `ChannelQuotaPaused`(`internal/op/channel_quota.go:64`) 折进渠道级 `DisabledUntil`) | 缓存实时 | 状态徽章 |
| 实时成绩 | relay `modelMetrics` EMA → `relay.ModelScoreView{ChannelModelID,WaitMs,Success,Samples}`(`internal/relay/metric.go:225`) → `relay.ModelScoreBriefs`(L266, 同名按样本加权归并) | **进程内, 重启清零**; 重启后仅由 `RouteMetricRecord`(生产 158 行, 覆盖 21/46 个在售模型名)恢复成员级指标 | P1 成功率/延迟 |
| 日成绩 | `stats_channel_model_dailies`(`StatsChannelModelDaily`, 92 天保留) → `op.ChannelDailyStatsGet`(`internal/op/stats.go:455`) 的写法可照抄 | 持久, 每日落库 | P3 冷启动回落(实测近 7 天覆盖 26/116 行、12/46 名) |
| 质量等级 | `ChannelQualitySnapshot`(417 行 / 30 渠道 / 20261021–20261107) + `model.QualityGradeOf` + `op.ShareQualityOf`(7 天内才给) | 日粒度 | P3 展示带 |
| 请求明细 | `relay_requests`(11961 行, success 11881/failed 74/canceled 6) | 按需查 | **不用**: 只覆盖 10/46 个在售名, 且 `round` 0–5 含重试, 口径要额外去重 |

### 1.5 加成员的写路径(现成, 必须复用)
- 请求形状 `model.GroupItemInput{ChannelGrantID}`(`internal/model/group.go:327`); `GroupUpdateRequest.Items *[]GroupItemInput` 是**整体替换**, 按授权主键匹配 —— 已有成员的 PK 与统计被保留(`syncGroupItems` L258), 被移除的成员顺带清 `active_item_id` 与悬空 `canary_item_id`;
- `op.GroupUpdate`(L165) 在**一个事务**里刷新成员, 每个成员过 `validateGroupItemAccess`(要求 `shared && listed`);
- 归属: `GroupUpdate` 首行 `oldGroup.UserID != scope.ID → "group not found"` —— **分组严格按用户私有, 管理员也改不了别人的分组**;
- 事件: handler `updateGroup`(`internal/server/handlers/group.go:237`) 成功后 `publishGroupEvent(groupEvent{Name:"changed", ...})` 并(仅模式变更时)`relay.ResetRouteState`。
- 前端已有: `useGroupList()`(`web/src/api/group.ts:167`, 带 SSE)、`useUpdateGroup()`(L245)、`writeGroupCache`(L145)、`useChannelGrantList()`(`web/src/api/channel.ts:315` → `/api/v1/channel/grants`)。

### 1.6 前端注册面(新页面必须同步改的四处 + i18n)
| 文件 | 需要改什么 |
|---|---|
| `web/src/stores/app.ts` | `Page` 联合类型、`NAV_ITEMS`(顺序=导航顺序)、`NAV_PAGES_BY_ROLE`(admin/reseller/user 三份都要加) |
| `web/src/lib/page-preload.ts` | `pageImports.market` 懒加载项 |
| `web/src/components/app.tsx` | 三处: `const Market = lazy(...)` / `const MarketActions = lazy(...)`(L23–42 区); 顶栏 `{visibleItem === 'market' && <MarketActions />}`(L153–156 区); 正文 `{visibleItem === 'market' && <Market />}`(L184–198 区) |
| `web/src/locales/{en,zh_hans,zh_hant}.json` | **`navbar.market`**(导航文案取自 `navbar.<pageId>`, 不是 `NAV_ITEMS.label`, 见 `web/src/components/app-shell.tsx` 的 `useTranslations('navbar')`)+ `market.*` 整段 |
| `web/src/components/common/PageActions.tsx` | 若要复用顶栏筛选: `PageActionPage` 加 `'market'` 与新的排序维度字段(见 §3.6) |
| `web/src/stores/app.test.ts` | 现成断言会强制 NAV↔pageImports 一一对应、admin 长度 == `NAV_ITEMS.length`; user 不得含 `channel`/`ops`/`withdrawals` |

---

## 2. 目标与非目标

- **目标**:
  1. 一屏看全「已发布在用」的模型: 名称、厂商品牌、用户价、可售渠道(`#code`)、状态、近期成功率与延迟;
  2. 按品牌与名称筛选, 按价格/成功率/延迟/名称排序(服务端排, 保证翻页与前端一致);
  3. 用户可在广场把某个模型(某条渠道授权)加进**自己已有的**模型配置(分组), 重复添加幂等, 不打断该分组正在跑的流量。
- **非目标**:
  - 不做新建分组/改模式/改 Relay 配置 —— 那是分组页的活(P2 只做"加成员"这一件事);
  - 不碰计费、钱包、结算、预扣任何口径; 广场只展示价格, 不下单;
  - 不暴露上游渠道真名、凭据名、渠道归属、供货价的额外字段(字段集是 `ChannelGrantCandidates` 的子集);
  - 不做用户评价/评分(P3 只复用 `QualityGradeOf` 的客观指标);
  - 不给「上架但无授权」的 6 行自动补授权 —— 补授权属于渠道授权矩阵, 广场按"有授权才能加入路由"的口径直接不列;
  - 不改 `selectByScore` 等 7 种选路算法。

---

## 3. P1 模型广场(只读挂牌表)

### 3.1 第一步: 先把口径抽成单点(前置改造, 不改行为)
`Available` 那条表达式目前抄在 `ChannelGrantCandidates` 与 `groupSnapshot` 两处, 广场会是第三处。P1 的第一件事是抽成:

```go
// internal/op/channel.go — 三处(候选/快照/广场)共用同一份可用性判定与展示名脱敏。
// 抽出来不只为去重: 广场一旦与分组页口径分叉, 用户会看到"广场可用、加进分组却不可用"。
func grantAvailability(channel model.Channel, channelKey model.ChannelKey, channelModel model.ChannelModel, fxOK bool) bool
func grantDisplayName(channel model.Channel) string // 已发布 → "#"+ShareCode; 未发布 → 真名
func grantCandidateOf(grant model.ChannelGrant, scope model.Scope) (model.ChannelGrantCandidate, bool) // 唯一构造点
```

`ChannelGrantCandidates`、`groupSnapshot`、`MarketCatalog` 三者改调它。**验收**: 现有 op 测试全绿且快照 JSON 字节级不变。

### 3.2 领域事实: `op.MarketCatalog`
`internal/op` **不 import `internal/relay`**(既有架构事实, 只有 `relay/xfyun` 子包被引), 所以成功率/延迟不能在这里取。P1 的分工: **op 出领域事实, handler 合运行时指标**, 合并后的对外形状在 handler 侧定义(模型包只描述供给事实, 不描述观测)。

```go
// internal/model/market.go — 广场的只读视图; 不含渠道真名/凭据名等内部真相。
type MarketChannel struct {
    ChannelGrantID int           `json:"channel_grant_id"` // 加进分组时提交的就是它。
    ChannelModelID int           `json:"channel_model_id"` // 供 handler 合并 relay 指标, 不是展示字段。
    ChannelCode    string        `json:"channel_code"`     // "#1001"。
    Available      bool          `json:"available"`
    StatusReason   string        `json:"status_reason,omitempty"` // disabled|breaker|fx_missing|unpriced, 空表示可用。
    UserPrice      model.LLMPrice  `json:"user_price"`      // 折算 + 上浮后的用户价。
    UserMedia      model.MediaPrice `json:"user_media,omitempty"`
    Kind           model.MediaKind  `json:"kind,omitempty"`
    PriceCurrency  string        `json:"price_currency,omitempty"` // 非 USD 报价时提示"已折算"
    FxMissing      bool          `json:"fx_missing,omitempty"`
    Unpriced       bool          `json:"unpriced,omitempty"`      // 文本价与媒体价全零。
}

type MarketModel struct {
    Name      string         `json:"name"`       // 展示名: 同名多写法时取出现次数最多的原写法。
    Brand     string         `json:"brand"`      // 品牌键(见 §3.3); 解析不出为 "other"。
    Channels  []MarketChannel `json:"channels"`  // 定序: 可用优先 → 用户价 blended 升 → 编码升。
    InGroups  []int          `json:"in_groups"`  // 我已把它加进哪些自有分组(供"已加入"角标与重复提示)。
}
// 模型级指标不进 model 包(数据来自 relay): handler 在 handlers/market.go 里组装,
// 形状如下 —— 指标与来源标注必须成对出现, 免得界面把"没数据"当"很好"。
type MarketModelView struct {
    model.MarketModel
    WaitMs        float64 `json:"wait_ms,omitempty"`   // 样本加权后的模型级耗时。
    Success       float64 `json:"success,omitempty"`   // 样本加权后的成功率(0..1)。
    Samples       int     `json:"samples"`             // 归并后的样本数; 0 表示冷启动。
    MetricsSource string  `json:"metrics_source"`      // live|daily|none(P3 起才有 daily)。
}
type MarketCatalogResponse struct {
    Models    []MarketModelView `json:"models"`
    Brands    []string          `json:"brands"`      // 响应内出现过的品牌键, 供前端品牌下拉, 不写死。
    UpdatedAt time.Time         `json:"updated_at"`  // 进程内指标的最后更新时间。
}
```

`op.MarketCatalog(scope model.Scope) []model.MarketModel` 的遍历口径**逐条对齐 `ChannelGrantCandidates`**: 走 `channelGrantCache` → `channelModelCache` → `channelKeyCache` → `channelCache`, 要求 `shared && approved && listed`, 价格走 `ledgerModelPrices` + `ChannelMarkupFor`, 按小写名字折叠。`InGroups` 由 `op.GroupList(scope)` 的成员授权主键算出(只含自有分组)。

**为什么以「授权」而不是「渠道模型」为最小单元**: `group_items` 只认 `channel_grant_id`(`validateGroupItemAccess` 也按授权校验), 广场若按"渠道模型"展示, 用户点"加入"时还得再翻译一次 —— 直接按授权列出, 点击即刻可提交。

### 3.3 品牌解析(三段式, P1 先不用新表)
```go
// internal/op/market_brand.go
// 1) model_capabilities.family 的首段映射(官方登记, 最可信): deepseek-flash→deepseek, gpt-luna→openai, claude-opus→anthropic, glm-flash→zhipu, mimo→xiaomi ...
// 2) 名称关键词兜底(与前端 web/src/lib/model-icons.tsx 的 MODEL_ICON_PATTERNS 同表, 但**真源放后端**)
// 3) 都不中 → "other"(界面显示"其他")
func MarketBrandOf(modelName string) string
```
生产实测(46 个在售模型名): **family 解析出 34 个**(anthropic 8 / deepseek 7 / openai 7 / xiaomi 3 / google 2 / zhipu 2 / minimax 2 / alibaba 2 / xai 1), **关键词兜底仅命中 1 个**(deepseek), 剩 **11 个落 `other`**: `g1` `mini` `n1` `n1proflash` `n1promax` `wuwen` `global:hy3` `muse-chat` `spark-x2.6` `step-3.7-flash` `step-5` —— 这 11 个正好是 §5.1 人工覆盖要处理的对象。
**口径决策**: 关键词表在后端维护一份, 前端只按品牌键选图标/颜色, **不再自带关键词表** —— 否则同一个模型会出现"后端说 openai、前端画 deepseek 图标"的分叉。P1 落地时把 `web/src/lib/model-icons.tsx` 的 `MODEL_ICON_PATTERNS` 改成「品牌键 → 图标」的纯映射。

### 3.4 API(新增一个只读端点)
```
GET /api/v1/market/models
  ?brand=<品牌键>          // 可多值: brand=openai&brand=anthropic
  &q=<名称关键词>          // 大小写不敏感子串
  &only_available=1        // 只要至少一条可用渠道
  &kind=text|image|video   // 形态
  &group_id=<id>           // 可选: 已在哪些自有分组
  &sort=price|success|latency|name
  &order=asc|desc
```
- 鉴权: `middleware.Auth()`, **无 `Allow(...)`** —— 全部登录角色可读(普通用户是主要受众);
- 响应: `{models: [...], updated_at: <RFC3339>}`; 不分页(实测 48 个名字的完整 JSON 仅 **18.5 KB**);
- 排序与筛选**在服务端**: 先按 `sort` 生成稳定全序, 再切给前端; 前端不重排, 只为局部筛选(已加入/未加入)做展示层过滤;
- 指标合并位置: handler 里 `views := relay.ModelScoreViews()` → `map[channelModelID]ModelScoreView` → 填每个渠道行 → 模型级按 `v.Samples` 加权(与 `relay.ModelScoreBriefs` 同一算法, 直接复用它对 `ModelScoreChannel` 的加权口径, 不手写第二套)。**这是唯一允许 relay 与 op 在同一处汇合的地方**(既有 `GET /api/v1/model/scores` 已是此模式)。
- 后端不需要新缓存: `ChannelGrantCandidates` 已走四个进程内缓存, USD 价零折算不查表, CNY 走内存牌价快照; 一次请求是纯内存计算。
- OpenAPI: `internal/server/handlers/openapi_admin_paths.go` 的「登录即可」区补 `/api/v1/market/models`(该表是文档侧唯一真源); 无 step-up(只读且不含任何写操作)。
- 刷新频率: 价格与可售集合变动少, 指标在变 —— 前端按 `web/src/lib/polling.ts` 的 `POLL_NORMAL(20000)` 轮询即可, **不用 `POLL_FAST`**: 广场不是看板, 每 5 秒重拉 18.5 KB 收益很低。分组列表仍由既有 SSE(`/api/v1/group/events`)推动, 与广场各管各的。
- 缓存头: 不设 `Cache-Control` 之外的强缓存, 读取侧一律走内存缓存 + 短轮询, 与 `/api/v1/channel/grants` 现状一致。

### 3.5 状态徽章口径(多因并存时的优先级)
| 徽章 | 判定 | 展示 |
|---|---|---|
| 未定价 | 文本价与媒体价全零 | 灰 · 「未定价」, 价格位显示 `—` |
| 牌价缺失 | `fx_missing` | 红 · 「牌价缺失」, 且 `available=false` |
| 熔断中 | `isCircuitBroken(channel/channelKey/channelModel.DisabledUntil)` | 黄 · 「熔断中」+ 剩余分钟 |
| 渠道停用 | `!channel.Enabled` | 灰 · 「渠道停用」(`#1005` 的 4 条即此) |
| 凭据停用 | `!channelKey.Enabled` | 灰 · 「凭据停用」 |
| 冷启动 | `Samples == 0` | 蓝 · 「暂无成绩」, 成功率/延迟显示 `—` |
| 可用 | 以上皆否 | 绿 · 「可用」 |

**"最近成功率"口径**: 近 window 内 EMA 成功率(样本加权), 样本 `< QualityMinSamples(30)` 时**只显示`—`不给数**(与小样本刷分同一防线); 显示为一位小数的百分比, tooltip 里给「样本数 / 数据来自进程内指标, 重启后重新累计」。

### 3.6 前端落点
| 区域 | 内容 | 复用 |
|---|---|---|
| 新页面 `market`(模型广场) | 卡片/列表两种布局: 卡片首行「品牌图标 + 模型名 + 状态点」, 次行「最低价 + 渠道数 + 成功率/延迟」, 展开行列出各 `#code` 与各自价格/状态 | `getModelIcon`(改按品牌键)、`formatLatency`(`web/src/lib/format.ts:57`)、`formatPlainMoney`(`web/src/lib/money.ts:4`)、`VirtualizedGrid` |
| 顶栏 `MarketActions` | 搜索(名称)、排序维度(价格/成功率/延迟/名称 —— **每个维度自带最优方向**: 价格升、成功率降、延迟升、名称升)、形态筛选、布局切换 | `PageActions`(需扩 `PageActionPage` 加 `'market'` 与 `marketSortBy` 持久化字段; `sortOptions` 本就是 props 数组, 直接传四项, 不改组件签名) |
| 品牌筛选 | 品牌下拉(动态来自响应的 `brands` 汇总, 不写死), 支持多选 | 新 `market.brand.*` 文案 |
| 加入分组 | 展开行尾「加入分组 ▾」→ 自有分组列表(勾选, 已含的置灰标注"已在其中")→ 确认 | `useGroupList()`(已带 SSE, 加入后事件自动刷新)、`useUpdateGroup()`、`writeGroupCache` |
| 导航 | 位置放在 `home` 之后、`group` 之前(语义: 先挑模型, 再进"模型配置"编排路由); 三种角色全可见 | `web/src/stores/app.ts` 三处 + `web/src/lib/page-preload.ts` + `web/src/components/app.tsx` + `navbar.market` |
| i18n | `navbar.market` + `market.*`(title/card/sort/brand/status/price/addToGroup/noGroup/cold/unpriced/fxMissing/tooltip/sampleNote) | 三语键集合必须完全一致(`web/src/locales/locales.test.ts`) |

**为什么排序维度不含"方向"开关**: 四个维度的最优方向是固定的(越便宜越靠前、成功率越高越靠前、延迟越低越靠前、名称升序), 给用户一个无意义的"反转"只会让人把最差模型排到第一屏。与模型页把 `nameAsc`/`nameDesc` 当两个选项同构。

### 3.7 验收标准(P1)
- [ ] 广场列出的模型名集合 == `SELECT lower(name) ... WHERE shared && approved && listed AND 有授权` 的结果(生产实测 46);
- [ ] 每条渠道行的 `user_price` 与分组页同一授权显示的价格**完全一致**(含 CNY 两条已折算、零价两条显示"未定价");
- [ ] 未登录 401、任意登录角色 200; 响应里 grep 不到任何上游渠道真名、凭据名、渠道 `user_id`;
- [ ] 4 条不可用(`#1005`)在 `only_available=1` 时被过滤, 默认可见且状态为"渠道停用";
- [ ] 无样本模型成功率/延迟显示 `—` 而非 `0`/`100%`, 且排序置底;
- [ ] 三语齐全, `web/src/stores/app.test.ts` 与 `web/src/locales/locales.test.ts` 全绿。

---

## 4. P2 加入自有模型配置(分组)

### 4.1 语义(只做"加成员", 复用整体替换)
```
POST /api/v1/market/add-to-group    {group_id, channel_grant_ids: [int]}
```
服务端步骤(全部复用既有实现, 不新增写语义):
1. `op.GroupGet(group_id, scope)` —— 非自有分组直接 404(与 `GroupUpdate` 首行同口径, 不暴露存在性);
2. 读出现有成员的 `channel_grant_id` 有序集合;
3. 新集合 = 现有 + 本次提交(**按授权主键去重**, 已存在的不重复加、**不改变既有成员的相对顺序**);
4. `op.GroupUpdate(group_id, &GroupUpdateRequest{Items: &newItems}, scope, ctx)` —— 事务、`validateGroupItemAccess`、`syncGroupItems` 的 PK 保留、`active_item_id`/`canary_item_id` 对齐全部照旧;
5. handler 组装 `groupResponse`(含 `runtime`)→ `publishGroupEvent("changed")` → 返回更新后的分组。

**为什么必须走 `GroupUpdate` 而不是自己写一张 `group_items`**: 成员的优先级、当前成员、灰度成员三者的一致性只在这一个事务里成立; 绕过它会留下"当前成员指向已删除成员"之类的悬空引用(该缺陷在 `syncGroupItems` 的注释里已记录过一次教训)。

### 4.2 幂等与并发
- 同一个 `(group_id, grant_id)` 重复提交: 服务端判重后**返回成功且成员数不变**(用户重复点两次不该报错);
- 并发两次加入: 走 `GroupUpdate` 的"整体替换"语义 —— 后到的请求把自己看到的集合与自己要加的合并写回, 极端并发下可能覆盖掉对方刚加的成员;`group_id` 粒度加一把进程内互斥(`sync.Map` 或 `op` 里既有的锁风格)即可消掉窗口, **不引入数据库级锁**;
- 分组正在跑流量时新增成员: 成员集合变化不重置路由状态(只有 `Mode` 变化才 `relay.ResetRouteState`), 因此**不打断在途流量**; 新成员在下次选路时即可参与(冷成员按 `memberWait`/评分的中性值处理, 既有逻辑已覆盖)。

### 4.3 前端
- 展开行「加入分组」→ 弹层: 自有分组列表(名称 + 当前成员数 + 模式), **已在其中的分组置灰并标注「已在其中」**;
- 提交后: `useUpdateGroup` 的同款缓存处理(`writeGroupCache`)+ `invalidateQueries(groupListQueryOptions)`; 分组页通过既有的 `/api/v1/group/events` SSE 自动收到 `changed`;
- 广场行的「已加入」角标由 `InGroups` 直接渲染, 无需二次请求;
- 一个用户都没有分组时: 引导文案"还没有模型配置, 先去模型配置页新建"(**不跳转自动建**, 见非目标)。

### 4.4 验收标准(P2)
- [ ] 加入后再看分组页: 新成员在末尾、既有成员顺序与 PK 未变、该分组 `runtime` 未被重置;
- [ ] 重复加入同一授权: 200 且成员数不变;
- [ ] 加入不存在的分组 / 别人的分组 / 未发布渠道的授权: 分别 404 / 404 / 400+`channel grant not found`, 且**错误文案不泄漏资源是否存在**;
- [ ] 加入后 SSE 事件到达分组页, 页面无需手动刷新;
- [ ] 加成员失败时分组完全不被改写(事务回滚)。

---

## 5. P3 品牌人工覆盖 + 冷启动指标回落 + 质量带

### 5.1 品牌人工覆盖(唯一需要新表的一段)
```go
// internal/model/market.go — 迁移 045_market_brand_override.go(当前最高版本是 044)
type MarketBrandOverride struct {
    NameKey   string `json:"name_key" gorm:"primaryKey;size:128"` // 小写模型名(与折叠键同一口径)
    Brand     string `json:"brand" gorm:"size:32;not null"`
    Note      string `json:"note" gorm:"size:256;not null;default:''"`
    UpdatedAt time.Time `json:"updated_at"`
}
```
- 解析顺序升级为 **人工覆盖 → family → 关键词 → other**;
- 管理入口: 不新建页面, 挂到既有「价格」页的模型卡片(那里已有 `ModelCapability` 的编辑入口与 `CapabilitySaveManual` 先例, 审计动作 `model.capability-save` 可照抄);
- 目的: 把 `g1/mini/n1/n1proflash/n1promax/wuwen/global:hy3/muse-chat` 这类内部命名归到真实厂商, `other` 不应长期占 11/46。

### 5.2 冷启动指标回落(relay 重启后不留 `—`)
- 现状: 进程内 EMA 重启清零; 持久化的 `stats_channel_model_dailies` 近 7 天只覆盖 12/46 个名字 —— 所以回落**必须先扩数据面**:
  1. 先确认 `persistDailyStats`(`internal/op/stats.go:151`) 为何有 45/116 条在售行**从未落过库**(生产: 全期 339 行 / 109 个渠道模型 / 20261007–20261108, 但近 7 天只落在 33 个渠道模型上)。若它们确实没有流量, 那是"冷"不是"漏", 广场显示 `—` 是正确的;
  2. 加只读助手 `op.ChannelModelRecentMetrics(ids []int, days int) map[int]model.StatsMetrics`(照抄 `ChannelDailyStatsGet` 的 `date >= since` + `Order("date ASC")`, 只回模型级聚合);
- 展示优先级: 进程内 EMA(samples>0) → 近 7 天日均(成功率 = `RequestSuccess/(RequestSuccess+RequestFailed)`, 延迟 = `WaitTime/(成功+失败)`) → `—`。响应里带 `metrics_source: "live"|"daily"|"none"` 让界面能如实标注"来自近 7 天统计"。
- **口径警告**: `WaitTime` 是"首帧等待"的累计值(`StatsMetrics` 语义), 与 EMA 的口径不同源, 二者不可混算; 回落时必须整段换源而不是逐字段拼接。

### 5.3 质量带(复用既有等级, 不给第二套分数)
- 渠道行加 `quality_band`: 直接复用 `model.QualityBandSuccess/Latency/Uptime`(`internal/model/quality.go:128/142/154`)的区间口径, 数据取 `channel_quality_snapshots`(7 天内才给, 与 `op.ShareQualityOf` 的时效门槛一致);
- 三个 band 都只在样本 ≥ `QualityMinSamples` 时给, 否则整段省略(不输出空壳);
- **不出精确值**: 广场面向所有登录用户, 精确成功率/延迟/请求量属于运营情报, 与分享页脱敏口径保持同一防线。

### 5.4 验收标准(P3)
- [ ] 人工覆盖写一条后, 广场立即按新品牌归类(缓存刷新同 `CapabilitySaveManual` 的既有做法), 审计有记录;
- [ ] 杀掉进程重启后, 有近 7 天数据的模型仍显示数字且 `metrics_source="daily"`; 无数据的仍显示 `—`;
- [ ] `other` 桶的模型数从此前 11 降到可解释的水平(每一家都有明确归属或明确记 `other` 的理由);
- [ ] band 只在样本达标时出现, 且不给精确值。

---

## 6. 权限矩阵

| 动作 | 普通用户 | 渠道商 | 管理员 |
|---|---|---|---|
| 浏览广场 | ✓ | ✓ | ✓ |
| 看到渠道编码 `#1001` | ✓ | ✓ | ✓ |
| 看到用户价(折算+上浮后) | ✓ | ✓ | ✓ |
| 看到上游渠道真名 / 凭据名 | — | 仅自有渠道 | 仅自有渠道(`scope.Owns`, 管理员对他人的渠道同样脱敏) |
| 看到供货价 | ✓(既有口径) | ✓ | ✓ |
| 把模型加进分组 | 仅自有分组 | 仅自有分组 | 仅自有分组(**管理员改不了别人的分组**) |
| 人工覆盖品牌 | — | — | ✓(P3) |
| 看到质量带 | ✓(P3) | ✓ | ✓ |

非归属访问一律 404 语义(`scope.Owns` / `UserID != scope.ID`), 不暴露资源存在性 —— 与 `GroupGet`、`ChannelPublish` 同规。

---

## 7. 实施顺序与验收

### P1 挂牌表(先行, 1 个版本) — 已实现
- [x] 抽 `grantAvailability`/`grantDisplayName`/`grantCandidateOf` 单点, 三处收口(行为不变);
      实际还多收了两处: `validateGroupItemAccess`(补上了审核闸)与广场本身, 详见 §10.1;
- [x] `model.MarketModel` + `op.MarketCatalog` + `op.MarketBrandOf`;
- [x] `GET /api/v1/market/models`(handler 侧合并 relay 指标 + 服务端筛选/排序) + OpenAPI「登录即可」区补条目;
- [x] 前端: 新页面 + `MarketActions` + 导航四处 + 三语 + `web/src/lib/model-icons.tsx` 改为品牌键映射;
- **验收**: §3.7 全部; 价格与分组页逐条比对一致; 零价/CNY/停用渠道/冷启动四类边界都显示正确。

验收记录(与 §3.7 逐条对应, 均有测试锁定):

| §3.7 验收项 | 锁定方式 |
|---|---|
| 模型名集合 == `shared && approved && listed && 有授权` | `op.TestMarketCatalogSupplyGate`(三种被排除情形逐个造) |
| 渠道行价格与分组页同一授权一致 | `op.TestMarketCatalogPriceMatchesGroup`(同一夹具下两处取数逐一比对) |
| 未登录 401 / 任意登录角色 200; 响应不含上游真名与凭据名 | `handlers.TestMarketRouteRequiresLogin`、`handlers.TestMarketResponseHidesInternals` |
| 4 条不可用在 `only_available=1` 时被过滤, 默认可见且状态为渠道停用 | `handlers.TestMarketFilterByBrandNameKind`(含停用渠道夹具) |
| 无样本显示 `—` 而非 `0`/`100%` 且置底 | `handlers.TestMarketSortHidesColdStartAtBottom` |
| 三语齐全、导航一致性测试全绿 | `web/src/stores/app.test.ts`(4)、`web/src/locales/locales.test.ts`(4) |

补充锁定:`op.TestMarketCatalogStatusReason`(徽章优先级)、`op.TestMarketCatalogFoldsNamesAndGroups`(大小写折叠与 `in_groups`)、`op.TestMarketBrandOf`(三段解析)、`op.TestValidateGroupItemAccessRequiresApproval`(审核闸)、`handlers.TestMarketPriceSortPutsUnpricedLast`(零价置底)、`handlers.TestMarketUpdatedAtIsRecent`(时间戳)。

### P2 加入分组(第二个版本)
- [ ] `POST /api/v1/market/add-to-group`(复用 `GroupUpdate` + 幂等 + 分组粒度互斥);
- [ ] 前端加入弹层 + `InGroups` 角标 + 事件联动;
- **验收**: §4.4 全部; 分组页 SSE 自动刷新; 失败时分组零改写。

### P3 品牌覆盖与指标回落(第三个版本)
- [ ] 迁移 `045_market_brand_override.go` + 覆盖管理入口 + 解析顺序升级;
- [ ] `op.ChannelModelRecentMetrics` + `metrics_source` 回落;
- [ ] 渠道行质量带(三个 band, 样本达标才给);
- **验收**: §5.4 全部。

---

## 8. 风险与边界

1. **口径漂移(最大风险)**: 可售集合与可用性判定目前已有 4 处近似实现(候选/快照/分组校验/briefs), 广场是第 5 处。**P1 第一步必须先收口**(§3.1), 否则"广场可用、加进分组却报 `channel grant not found`"这类矛盾必然出现 —— `validateGroupItemAccess` 只查 `shared && listed`, **不查 `ReviewStatus`**, 这是现存的一处已知不对称, 广场落地时应一并修正并加测试锁定。
2. **进程内 EMA 不可靠**: 重启清零, 重启后只能从 `RouteMetricRecord` 恢复到 21/46 个在售模型名的成员级指标; 界面必须标 `metrics_source`, 绝不把"没数据"渲染成"100% 成功"。
3. **小样本刷分**: `Samples < 30` 不给数字; 排序里无样本一律置底, 不允许"样本 1 次成功 → 成功率 100% 排第一"。
4. **零价霸榜**: `#1011` 两条全零价必须在价格排序里置底并标「未定价」; 与牌价缺失的"清零 + 标记"是同一类陷阱, 前者是供给没配价, 后者是汇率没取到。
5. **6 条"上架但无授权"**: 广场不列(不可加入路由)。这是运营侧待办(补授权), 记入 backlog, 不在广场兜底自动补 —— 自动补授权会绕过渠道授权矩阵的显式决策。
6. **大小写重名**: `minimax-m3/MiniMax-M3`、`deepseek-v4.1-flash/DeepSeek-V4.1-Flash` 必须按小写折叠成一屏一行(与 `ModelScoreBriefs` 的 `strings.ToLower` 归并口径一致), 否则同名模型会拆成两条、用户以为有四个选择。
7. **加分组的并发覆盖**: `items` 是整体替换语义, 纯前端轮询下的"读-改-写"有窗口; P2 用 `group_id` 粒度互斥挡住, 不升级为数据库锁。
8. **不做的事**: 不新建分组、不改分组模式、不做模型详情页的独立统计接口、不做评价体系、不碰计费。

---

## 9. 一句话路线图

**P1 把"已发布在用"的模型做成一张带价格/渠道代码/状态/近期成绩的挂牌表(先收口口径, 再开页面)→ P2 让用户点一下就把这条渠道加进自己已有的模型配置(复用 GroupUpdate 的整体替换语义, 幂等且不打断流量)→ P3 用人工覆盖补齐品牌、用近 7 天统计补上重启后的指标、用质量带给"跑得怎么样"一个可解释的区间。**

## 10. 实施记录(P1)

### 10.1 代码落点

| 规划位置 | 实际落地 | 说明 |
|---|---|---|
| §3.1 口径单点 | `internal/op/grant_view.go`(新文件) | 抽出 `grantPartsOf`(解析缓存三侧)、`grantAvailability`(可用性唯一表达式)、`grantDisplayName`(脱敏展示名)、`grantLedgerPrices`(价格+上浮+折算)、`grantCandidateOf`(候选项唯一构造器)。`ChannelGrantCandidates`、`groupSnapshot`、`validateGroupItemAccess`、广场四处改调它 |
| §3.2 数据模型 | `internal/model/market.go` + `internal/op/market.go` | `MarketChannel`/`MarketModel` 只描述供给事实; `op.MarketCatalog(scope)` 折叠同名并定序 |
| §3.3 品牌解析 | `internal/op/market_brand.go` | `MarketBrandOf`: `model_capabilities.family` 头段 → 家族对照表 → 名称关键词 → `other`。32 个品牌常量在 `internal/model/market.go` |
| §3.4 接口 | `internal/server/handlers/market.go` | `GET /api/v1/market/models`, 仅 `middleware.Auth()`(无角色白名单) |
| §3.5 徽章优先级 | `marketStatusReason` | `fx_missing > disabled > key_disabled > breaker > unpriced`, 与设计稿一致 |
| §3.6 前端 | `web/src/components/modules/market/`、`web/src/api/market.ts` | 新页 `market`, 导航位于 `home` 之后, 三种角色均可见; 顶栏 `MarketActions` 复用 `PageActions`(新增 `hideCreate`/`extraFilter` 两个可选 props) |
| §3.7 验收 | `internal/op/market_test.go`、`internal/server/handlers/market_test.go` | 供给侧闸口、徽章优先级、价格与分组页一致、折叠与分组归属、脱敏、品牌解析、`validateGroupItemAccess` 补闸; 接口侧鉴权、脱敏、排序置底、筛选、价格排序、时间戳 |

### 10.2 与规划稿的偏差(及原因)

1. **筛选与排序全部走服务端, 前端不复算。** 规划稿写的是"服务端排, 保证翻页与前端一致"。实现上 `filters`/`sort` 都进 `queryKey`, 换条件是换一份查询而非在本地排一遍: 否则前端筛掉两行后仍需重排才能保持"这个维度上的最优", 那等于把排序规则抄成两份。
2. **品牌清单在筛选之前汇总。** 响应里的 `brands` 是全量而不是"本次结果里出现的": 否则选中 `openai` 之后下拉只剩 `openai`, 用户没法再选第二个品牌。
3. **广场不再内置品牌关键词表。** 规划稿 §3.6 只写"`getModelIcon` 改按品牌键"。实现时把 `web/src/lib/model-icons.tsx` 拆成两层: `BRAND_ICONS`(品牌键 → 图标, 与后端常量一一对应)与 `getModelBrand`(仅按名称猜品牌的本地兜底)。既有 6 个只有模型名的调用点签名不变, 图标口径因此只有一处。
4. **广场不用 `VirtualizedGrid`。** 卡片可展开, 展开后行高变化会让虚拟化在挂载时测得的行高失效, 下一行会盖住展开内容。P1 一屏约 48 行, 直接渲染的代价远小于这个坑; 列表布局仍是单列。
5. **`validateGroupItemAccess` 补上了审核闸。** 该函数此前只查 `shared && listed`, 与候选列表的 `shared && approved && listed` 存在不对称(已发布但被重新审核打回的渠道, 旧成员仍可被提交进来)。生产快照里 `shared=1 && review_status != approved` 为 **0 行**, 故此改动不影响存量数据。
6. **GORM `default:true` 的坑(测试侧)。** `channels.enabled`/`channel_keys.enabled` 带 `gorm:"default:true"`, 零值 `false` 在 `Create` 时会被默认值回填成 `true`; 造"停用"反例必须在写库后显式 `Update("enabled", false)` 一次, 且判定要在 `Create` 之前取样。已在两处测试夹具里注明。

6. **成绩口径是三档而不是两档(`live` / `insufficient` / `none`), 门槛取 `model.QualityMinSamples`。** 规划稿只写"无样本显示 `—`", 实测发现只判 `samples > 0` 不够: 生产上一个仅 1 次调用的模型以 43ms 排到了"延迟最优"第一屏, 恰好它只成功过那一次。"没跑几次"和"跑得最快"是同一类错误的两端, 都是把未知读成结论。模型级门槛按**合计**样本数(跨渠道各 5 次、合计 40 次足以说明整体稳定性), 渠道级门槛按**单行**样本数; 两处共用一个 `marketMetricsSourceOf`, 阈值不各写一份。样本不足仍报 `samples` 且不给数字 —— 界面要能区分"还没跑几次"与"还没跑过"。
7. **`brands` 与 `metrics_source` 都放在响应里, 前端不硬编码。** 与 §10.2-2 同源的理由: 品牌键集合、成绩可信门槛若各在前端写一份, 后端调阈值的那天前端就开始撒谎。

### 10.3 P2 实施记录(v1.6.0): 只追加语义

规划稿的 P2 写的是"勾选自有分组 → 按 `items` 整体替换追加成员"。落地时这一条**被推翻**, 因为它在并发下是错的:

- **原方案的缺陷**: `GroupUpdateRequest.items` 是整体替换, 语义要求调用方持有**准确的当前集合**。广场卡片数据来自 20 秒轮询, 用户在弹层里停留期间另一个会话(另一标签页/另一台设备)可能刚改过同一个分组。前端按手里的旧快照算出"现有成员 + 本次新增", 提交后把对方那次新增整段盖掉 —— **不报错, 成员凭空消失**。
- **改法**: 新增 `POST /api/v1/group/add-items`(`op.GroupAddItems`)。调用方只声明"要加哪些授权", 服务端在事务内 `SELECT` 当前成员再追加。差集不再依赖调用方快照, 并发写从根上不互相覆盖。
- **三条语义取舍**: 只追加不重排(既有成员优先级不动, 新成员接末尾; 重排是分组页的编辑动作) / 已存在即跳过而非 409(重复点击与双标签页提交都是正常操作) / 归属与准入复用 `validateGroupItemAccess` 与 owned 校验, 与 create/update 同口径。
- **验证**: 后端 6 例, 其中"过期快照不丢他人成员"一条做了**变异校验** —— 把实现改回"按缓存快照整体替换"后该用例确实失败(`lost the other session's member: [2]`), 证明它真能抓住这个缺陷而不是空过。真实实例上用两个并发 HTTP 请求验证: 各自 `added=[626]`/`added=[453]`, 最终 `count=2`; 再提交一次返回 `added=[], skipped=[626,453]`(幂等)。
- **界面**: 分组与渠道都可多选; 提交前逐格预览新增/跳过条数(`addPreview` 抽成纯函数以便直接测试, 因为面板在弹层里关闭时不渲染); 全部已存在时提示"无需新增"而非失败; 无任何配置时直接给创建入口。
- **默认勾选策略的取舍**: 起初默认勾"还没配过的全部分组", 生产实测一次会把模型加进 22 个配置 —— 误操作代价远大于省下的点击(这些配置在跑线上流量, 加成员会立刻改变选路)。改为**默认不勾分组**, 另给"一键选上未配置的 N 个", 两个极端都不取。

### 10.4 仍待做(P3)

品牌人工覆盖表、`stats_channel_model_dailies` 冷启动日级回落、`QualityGradeOf` 质量带 —— 三项均未实施。

### 10.5 生产口径核对(快照 `build/prod-snap-latest.db`, 2026-11-08)

- 供给侧: 25 个渠道 → 19 个 `shared && approved`; 504 条 `channel_models` → 116 条 `listed`; 486 条授权(每条渠道模型恰 1 条)→ 110 条挂牌; 折叠成 **46 个模型名**。
- 被丢弃: 6 条 `listed` 但有授权的行缺失(广场按"有授权才能加入路由"不列)。
- 不可用 4 条: `gpt-5.6-luna`/`gpt-6-luna`/`gpt-6-sol`/`gpt-6.1-sol` 全在 `#1005`(渠道 `enabled=0`)。
- 未定价 2 条(`step-3.7-flash`、`step-5` @`#1011`); 非 USD 报价 2 条(`deepseek-v4.1-flash`/`deepseek-v4.1-flash-eco` @`#1036`, CNY, 已按牌价折算)。
- 品牌(在 46 个在售模型名上实测): alibaba 2 / anthropic 8 / deepseek 8 / google 2 / iflytek 1 / minimax 2 / openai 7 / stepfun 2 / xai 1 / xiaomi 3 / zhipu 2 / **other 8**。落到 `other` 的 8 个:`g1` `mini` `n1` `n1proflash` `n1promax` `global:hy3` `muse-chat` `wuwen` —— P3 的品牌覆盖表就是为这批留的。(规划稿写的是 11 个, 那是按旧的两段解析估的: `step-3.7-flash`/`step-5` 由 `stepfun` 关键词接住、`spark-x2.6` 由 `iflytek` 接住, 故实测降到 8。)

### 10.6 P1 时的遗留(记录历史, P2 已处理)

- ~~P2: 批量加入、加入后的幂等重排、跨页一致性验收~~ —— **v1.6.0 已处理**: 批量加入与幂等由 `add-items` 的只追加语义覆盖; 跨页一致性指"加入后广场卡片角标与分组页同时跟上", 现在写入成功即失效广场缓存并推 SSE 事件, 无需等下一轮轮询。
- P3: 品牌人工覆盖表、`stats_channel_model_dailies` 作为冷启动回落(`metrics_source` 已预留 `daily` 取值, 现只产出 `live`/`insufficient`/`none`)、质量带 —— 仍未做。
