# 订阅渠道 + 插件式订阅/模型导入 —— 方案

> 状态：**方案稿，未实现**。所有结论来自当前仓库取证（文件:行号）与生产库实测。
> 未验证处明确标注。

---

## 一、先把你的需求翻译成工程语言

你的原话拆成三件事：

| # | 你要的 | 翻译 |
|---|---|---|
| 1 | 「有一个**专门的渠道**」 | 新的一种渠道类型，区别于现有的 API-Key 中转站渠道 |
| 2 | 「**不用配置调用信息**」 | 建渠道时不填 `base_url` / path / key，这些由插件给 |
| 3 | 「调用信息**通过不同插件**把**订阅**和**可用模型**读进来」 | 插件提供：登录（订阅）+ 模型清单 + 额度；系统据此自动建 key 和模型 |
| 4 | 「然后**通过模型配置**配置调用」 | 模型进去之后，沿用现有 `channel_models` 那套（价格、协议、映射） |

**一句话**：你要一个「**订阅渠道**」类型——建的时候只选「哪个平台」，其余全自动。

---

## 二、好消息：这件事你已经做完了 70%

这是本次调研最重要的发现。支撑你要的四件事，后端**大部分已存在**：

| 能力 | 位置 | 状态 |
|---|---|---|
| **拉取上游模型清单** | `op.ProbeChannelModels`（`op/channel_probe.go:40`） | ✅ 已有，返回 `[]ChannelFetchModel{Name, Protocols, Capability, Source}` |
| **推断每个模型的协议** | 同上（探测时判定） | ✅ 已有 |
| **自动入库为 channel_models** | `op.AutoAddChannelModels`（`op/channel_autoadd.go:258`） | ✅ 已有，含去重、上限 200、按基准兜底定价 |
| **后台定期复查新增模型** | `op.AutoAddChannelsRun`（`:493`） | ✅ 已有 |
| **账号类型（订阅）是一等公民** | `channel_keys.account_kind = oauth/cookie`（`model/channel.go:129`） | ✅ 已有 |
| **OAuth 平台注册表** | `accountOAuthSpecs`（`model/account_oauth.go:113`） | ✅ 已有，14 个平台 |
| **登录流程** | `signin/providers.go` | ✅ 已有，但参数写死为常量 |
| **前端「拉取模型」按钮** | `handlers/channel.go:568` `fetchModel` | ✅ 接口已有 |

**缺的只有三处**（第三处是我写方案过程中新核实的）：

| 缺口 | 具体阻碍 | 位置 |
|---|---|---|
| **A. 建渠道时 `base_url` 和 key 必须填** | `Form.tsx:92` `saveBlockedBaseUrl`、`:95` `saveBlockedNoKeys` 都是硬阻塞 | 前端 + 后端校验 |
| **B. 插件机制本身** | 目前平台是编译期常量，没有"运行时给一个平台"的入口 | `model/account_oauth.go` |
| **C. 探测函数要求 base_url 非空** | `ProbeChannelModels` 第 50-52 行：`if target.BaseURL == "" { return nil, ErrProbeBaseURLRequired }` 是**硬检查**。而 `probeGeminiCLIModels` 内部其实**不读** `target.BaseURL`（它用 `GeminiCLIBases(target)` 的内置默认值）—— 也就是说这条检查对 Gemini 分支是**多余的**，但会挡住"base_url 留空" | `op/channel_probe.go:50` |

**所以这不是从零造，是给已有流水线加一个「订阅渠道」入口。**

---

## 三、方案：三层结构

```
┌─────────────────────────────────────────────────────────┐
│ 1. 订阅平台插件层 (Platform Plugin)                      │
│    一个 YAML/JSON 描述"某个订阅平台怎么登录、怎么列模型" │
│    落点: /opt/switchyard/platforms/*.yaml                │
├─────────────────────────────────────────────────────────┤
│ 2. 订阅渠道类型 (ChannelDialect = subscription)          │
│    新渠道只选平台, 不填 base_url / key                   │
│    建渠道时自动: 登录 → 读订阅 → 读模型 → 建 key+models  │
├─────────────────────────────────────────────────────────┤
│ 3. 既有模型配置层 (channel_models)                       │
│    模型进来后, 价格/协议/映射 沿用现有 UI 与表           │
└─────────────────────────────────────────────────────────┘
```

关键设计判断：**不要引入 JS 运行时**。
插件在你这套里只需要表达「端点 + 参数 + 取模型的方式」，本质是**数据**，不是代码。
（第一节那句结论仍然成立：magpie 的插件是 JS，而你只需要它的**声明式部分**。）
---

## 四、第一层：订阅平台插件（声明式）

### 4.1 一个平台描述需要哪些字段

从我们已经实现的 gemini_cli / codex / claude / antigravity 反推，最小集合是：

```yaml
# /opt/switchyard/platforms/gemini_cli.yaml
id: gemini_cli
label: Gemini CLI
icon: gemini

# --- 登录 ---
# 三种可选登录方式, 与 magpie 的 auth.methods 对应
login:
  kind: oauth_redirect      # oauth_redirect | oauth_device | api_key | import
  authorize_url: https://accounts.google.com/o/oauth2/v2/auth
  token_url: https://oauth2.googleapis.com/token
  client_id: ${env:GEMINI_CLI_CLIENT_ID}   # 支持从环境变量取, 不落库明文
  scopes: [cloud-platform, userinfo.email]
  redirect_uri: http://localhost:8088/api/v1/account/oauth/callback

# --- 订阅信息 (读额度/套餐) ---
# 不是所有平台都有; 没有就省掉这段
quota:
  rpc: loadCodeAssist          # 复用现有 RPC 调用器
  base: https://cloudcode-pa.googleapis.com

# --- 模型发现 ---
discovery:
  kind: code_assist_rpc        # 复用 probeGeminiCLIModels 那套
  method: retrieveUserQuota

# --- 调用 ---
protocol: gemini_cli          # 对应现有 Protocol 位掩码
default_headers:
  User-Agent: antigravity/1.1.0 google-api-nodejs-client/10.3.0
  X-Goog-Api-Client: gl-node/22.21.1
```

### 4.2 为什么用 YAML 而不是数据库表

| | YAML 文件 | 数据库表 + 前端 CRUD |
|---|---|---|
| 工作量 | ~1.5 天 | ~3.5 天 |
| 谁来改 | 你（会 ssh） | 任何人都能点 |
| 密钥 | 可用 `${env:...}` 引用环境变量，**不落库** | 得落库，要信封加密 |
| 版本控制 | 可以进 git | 不行 |

**建议 YAML。** 密钥不落库是这里的实质性好处 —— 我们的 antigravity 密钥已经走环境变量，
如果引入数据库表反而退步。

### 4.3 加载与校验

```go
// 新增 internal/subplatform/ 包
func Load(dir string) (Registry, error)   // 扫描 *.yaml, 解析, 校验
func (r Registry) For(id string) (Spec, bool)
func (r Registry) All() []Spec
```

校验规则（**这几点必须硬性**，与我们 `account_oauth.go:111` 那条注释同一精神）：

1. `id` 必填、唯一、且**不得与内置平台重名**（防止覆盖 gemini/codex）
2. `protocol` 必须是现有 `Protocol` 枚举里的值 —— **不接受未知协议**
3. `login.kind` 必须是四种之一
4. `token_url` / `authorize_url` 必须是 https（本地测试可放开）
5. `${env:VAR}` 引用的变量必须存在，缺失则**该平台整体不加载并记错误**，而不是带着空值跑
6. 解析失败的**单个**平台只跳过它自己，不影响其他平台启动

第 5 条是重点：**宁可少一个平台，也不要用空 client_id 去打上游** —— 这与我们已有的设计取舍一致。
---

## 五、第二层：订阅渠道类型

### 5.1 新增一个渠道 dialect

现有 `channel.Dialect` 有 generic / xunfei / gemini_cli 等。新增：

```go
DialectSubscription = "subscription"   // 订阅渠道: 调用信息由平台插件提供
```

**建渠道时前端表现的变化**（这是用户能感知的核心）：

| 字段 | 普通渠道 | 订阅渠道 |
|---|---|---|
| `base_url` | **必填** | **隐藏/只读**（由平台给） |
| `keys` | **必填**（至少 1 个） | **隐藏**（由登录产生） |
| 平台选择 | 无 | **必填**（下拉，来自 Registry） |
| 模型列表 | 手动加 | **自动填充**（见 5.3） |
| path 三件套 | 可填 | 隐藏 |

### 5.2 保存流程（新建订阅渠道时）

```
用户在界面上:
  1. 选渠道类型 = 订阅
  2. 选平台 (如 Gemini CLI / Claude / Codex / 自定义)
  3. 点"登录"  →  走 signin.Provider 拿到 refresh_token
  4. 系统自动:
       a. 用平台 spec 的 base_url/protocol/headers 填进 channel
       b. 建一条 channel_key, account_kind=oauth, 存凭据
       c. 调 ProbeChannelModels 拉模型
       d. 调 AutoAddChannelModels 入库为 channel_models
  5. 用户只做一件事: 给模型配价格(可留空用基准价)
```

**关键**：第 4 步的四件事**全部已有实现**，只是现在没有被串成一条自动流程。

| 步骤 | 现有函数 | 需要改 |
|---|---|---|
| a. 填 base_url/protocol | 无（现在手填） | **新增**：从 spec 拷字段 |
| b. 建 key 存凭据 | `signin` 已有 | 复用 |
| c. 拉模型 | `ProbeChannelModels` | 复用 |
| d. 入库 | `AutoAddChannelModels` | 复用 |

所以**工作量主要在前面 a 和 UI，后面三项是接线**。

### 5.3 已有的自动模型导入（不用重做）

`op.AutoAddChannelModels`（`op/channel_autoadd.go:258`）已经做了这些：

- 单次最多并入 200 个模型（`autoAddModelLimit`，`:21`），防上游超长列表
- 按过滤表达式筛选（`AutoAddFilter`，`:89`）
- **按参考价兜底填供货价**（`fillSupplyPriceFromReference`，`:380`）
- 去重合并（`mergeFetchedForKey`，`:419`）
- 后台定期复查（`AutoAddChannelsRun`，`:493`）

**这一段完全不用动。** "拉模型入库"已经是成熟流程，不是要新写的东西。

### 5.4 额度/订阅信息（可选，第二阶段）

你要的"订阅"有两层含义，需要区分：

| 含义 | 是什么 | 现状 |
|---|---|---|
| 订阅 = **凭证** | OAuth refresh_token | ✅ 已有 |
| 订阅 = **套餐额度** | 这个账号还剩多少、何时重置 | ❌ 没有（这是上一份路由方案里 A 阶段的事） |

**第一阶段只需要第一层。** 第二层可以后做，且与本方案解耦。
---

## 六、"插件"这个词要定死：你不能写代码，那插件是什么

这是本方案最需要和你确认的一点。

### 6.1 三种可能的"插件"，成本差一个数量级

| 形态 | 你能做什么 | 能否引入新协议 | 成本 | 风险 |
|---|---|---|---|---|
| **A. 声明式配置**（YAML） | 用已有协议形状加平台 | ❌ 不能 | 1.5 天 | 极低 |
| **B. Go 插件（编译进本项目）** | 新写一个 `internal/subplatform/builtin_xxx.go` | ✅ 能 | 0.5 天/个 | 低（你自己审） |
| **C. 外部 JS 插件（magpie 那套）** | 第三方 npm 包定义平台 | 有限（仅 4 种协议） | 19 天 | **高**（无沙箱、能读凭据） |

**注意 B 这一行**：在 Go 里"加一个平台"本来就有成本，但**它其实就是你我现在做的事** ——
加 `Protocol` 常量 + probe 函数 + spec 条目。它不叫"插件"，但效果等同，且**永远能表达新协议**。

### 6.2 我的建议：A + B 混合，把 C 排除

| 你想要的平台 | 走哪条 | 为什么 |
|---|---|---|
| Gemini CLI / Claude / Codex / Kiro | **内置（已有代码）** | 已经实现了，直接接进订阅渠道 |
| 某个新的、OAuth + 标准协议的平台 | **A. YAML** | 不用改代码，填表即可 |
| 需要特殊信封/签名/WebSocket 的平台 | **B. 写一个 Go 文件** | 配置表达不了，但这是你自己写，安全可控 |
| 第三方给你写的任意转发逻辑 | **C. 不做** | 你的需求里没有这一项；它带来无沙箱代码执行 |

**关键判断：你的需求「订阅 + 模型读进来 + 模型配置配调用」里，
没有任何一处需要第三方代码执行。** 你要的是**自动化**，不是**可扩展性外包**。

---

## 七、实施顺序与工作量

### 7.1 阶段划分

| 阶段 | 内容 | 预估 | 依赖 |
|---|---|---|---|
| **S0** | **能力验证**：确认 `ProbeChannelModels` 对 gemini_cli 能返回模型（不走 UI） | 0.5 天 | 无 |
| **S1** | `DialectSubscription` + 后端校验放宽（允许 base_url/key 由系统填） | 1 天 | S0 |
| **S2** | `internal/subplatform`：YAML 加载器 + 校验 + 与内置平台合并 | 1 天 | 无 |
| **S3** | 建渠道流程接线：spec → channel 字段 → 建 key → 拉模型 → 入库 | 1.5 天 | S1+S2 |
| **S4** | 前端：渠道类型选「订阅」→ 平台下拉 → 登录按钮 → 自动填充模型 | 2 天 | S3 |
| **S5** | 现有 gemini_cli/codex/claude 三条内置平台接进订阅渠道 | 1 天 | S3 |
| | **合计** | **7 天** | |

### 7.2 最小可用切片（MVP，3 天）

只做「Gemini CLI 作为订阅渠道」这一条端到端：

| 步骤 | 内容 |
|---|---|
| 1 | `DialectSubscription` + 后端放宽校验（0.5 天） |
| 2 | 前端「订阅」类型 + 平台下拉（只放 Gemini CLI 一个）+ 登录（1.5 天） |
| 3 | 自动填 base_url + 拉模型 + 入库（1 天） |

**跑通之后再扩到其他平台** —— 那时候 YAML 加载器才真正有价值。

### 7.3 为什么不建议先做 YAML

YAML 加载器的价值 = 「**能加多少个平台**」。如果你只加 1-2 个，
它带来的抽象反而多一层需要维护的东西。

**建议顺序：先跑通 1 个（MVP），再抽象成 YAML。** 这与我们一贯的做法一致。
---

## 八、必须承认的风险与未验证项

| # | 风险/未验证 | 严重度 | 应对 |
|---|---|---|---|
| 1 | **未验证**：`ProbeChannelModels` 对 gemini_cli 是否真能返回模型（它走 `retrieveUserQuota`，我们之前实测能拿到）。**这是 S0 的全部目的** | 高 | 开工前先跑 S0 |
| 2 | **未验证**：auto-add 的过滤/定价逻辑对订阅渠道是否合适（它现在按"中转站基准价"兜底） | 中 | S0 里一并观察 |
| 3 | 「订阅渠道」与现有「渠道发布/上架」流程的兼容性未验证 | 中 | S1 时确认 |
| 4 | 一个订阅平台可能有**多种**登录方式（oauth / 设备码 / 粘贴 token），需不需要全支持 | 中 | 先只做 oauth_redirect |
| 5 | 同一平台的**多个账号**如何在一个渠道里管理（现在每渠道 key 是独立的，但 key 级轮换是死代码，见另一份方案） | 中 | 先用「一个账号一个渠道」简单模型 |

**第 5 条值得单独说**：上一份路由方案里我核实过，`SelectChannelKey` 在转发主路径是死代码。
这意味着：**一个订阅渠道挂多个账号时，系统只会用 Grant 绑定的那一个。**

所以 MVP 阶段应当**一个账号 = 一个渠道**。真想多账号共用一个渠道，需要先修那个死代码问题，
那是另一件事（约 1 天）。

---

## 九、和上一份方案的关系

| 方案 | 解决什么 | 关系 |
|---|---|---|
| `design-magpie-smart-routing.md` | 给中转站建健康度画像 / 额度感知选路 | **独立**。那是"怎么选"，本方案是"怎么把账号接进来" |
| `design-magpie-plugin-system.md` | 是否移植 magpie 插件系统 | **本方案取代它的结论**：不移植 JS 插件，改做订阅渠道 + 声明式平台 |
| **本文** | 订阅渠道 + 自动导入订阅与模型 | **执行这份** |

---

## 十、需要你确认

1. **第一个要接的平台是哪个？** 我建议 Gemini CLI（因为代码已存在，能最快跑通端到端）。
2. **「插件」你接受是 YAML（A），还是你能接受"要新增特殊协议时我写个 Go 文件"（B）？**
   - 只要你接受 B，"永远能加新平台"就成立，不需要 C。
3. **一个渠道挂几个账号？** 建议先一个账号一个渠道（受死代码限制）。
4. **模型自动导入后，价格你想怎么定？**
   - 留空用基准价（现有 `fillSupplyPriceFromReference` 行为）
   - 还是想让订阅渠道的模型价格一律填 0（免费订阅）？

---

## 十一、一句话总结

**你要的东西不用插件系统，用一个「订阅渠道」类型 + 一条自动流水线就够了。**

**而且这条流水线的四段（拉模型 / 推协议 / 自动入库 / 定期复查）你已经全部写好了，
缺的只是"建渠道时不填 base_url 和 key"这个入口，以及把四段串起来的那根线。**

**工作量 MVP 3 天、完整 7 天，而不是移植插件的 19 天。**

---

## 附：本次取证的关键证据（可复核）

| 结论 | 文件:行号 |
|---|---|
| 拉模型已有 | `internal/op/channel_probe.go:40` `ProbeChannelModels` |
| 返回结构含协议推断 | `internal/model/channel.go:305-310` `ChannelFetchModel{Name,Protocols,Capability,Source}` |
| 自动入库已有 | `internal/op/channel_autoadd.go:258` `AutoAddChannelModels` |
| 自动入库去重上限 | 同文件 `:21` `autoAddModelLimit = 200` |
| 按参考价兜底 | 同文件 `:380` `fillSupplyPriceFromReference` |
| 后台复查 | 同文件 `:493` `AutoAddChannelsRun` |
| 前端拉模型接口 | `internal/server/handlers/channel.go:568` `fetchModel` |
| base_url 硬阻塞 | `web/src/components/modules/channel/Form.tsx:92` `saveBlockedBaseUrl` |
| key 硬阻塞 | 同文件 `:95` `saveBlockedNoKeys` |
| 账号类型已是一等公民 | `internal/model/channel.go:129` `AccountKind` |
| OAuth 平台注册表 | `internal/model/account_oauth.go:113` `accountOAuthSpecs` |
| 登录流程 | `internal/signin/providers.go`（分发仅 1 处 case，`:142`） |

---

## 十二、你的四条决定（已定稿，方案据此收敛）

| # | 你的决定 | 对方案的影响 |
|---|---|---|
| 1 | **先做 Gemini** | 只做一条端到端，YAML 加载器**推迟**；Gemini 走内置 Go 路径（B 形态） |
| 2 | **插件接受后者**（要新协议时写 Go 文件） | **不做外部 JS 运行时**；"永远能加平台"成立 |
| 3 | **一个渠道可多账号** | **必须先修 `SelectChannelKey` 死代码**（新增工作量，见 13） |
| 4 | **模型导入后按渠道定价机制对外使用** | 复用 `MarkupRatio` + `SupplyPrice → UserPriceLLM`，**不新造定价** |

### 12.1 决定 2 的落地：Gemini 不需要 YAML

你说"先做 gemini" + "插件接受写 Go 文件"，这两条合起来意味着：

**Gemini 平台描述直接写在 Go 里（`internal/subplatform/gemini.go`），不需要 YAML 那一层。**

| | YAML 方案 | 内置 Go 方案（本次） |
|---|---|---|
| 加 Gemini | 写 yaml + 写加载器 | **写一个 Go 文件** |
| 工作量 | 1.5 天（加载器）+ 0.5 天（yaml） | **0.5 天** |
| 将来加第 2 个平台 | 加 yaml 即可 | 再写一个 Go 文件 |

**所以 YAML 加载器从 S2 里移出**，等真有第 2、3 个平台时再抽。这符合"先跑通再抽象"。

### 12.2 决定 4 的落地：定价直接复用现有机制

你已经有完整的渠道定价链路，**不需要为新渠道造定价逻辑**：

| 件 | 位置 | 作用 |
|---|---|---|
| `channels.MarkupRatio` | `model/channel.go:42` | 渠道级上浮比例；`<0` = 跟随全局 `markup_ratio` |
| `channel_models.SupplyPrice` | `model/channel.go:150` | 供货价（读/写/缓存读/缓存写，每百万 token） |
| `ChannelModel.UserPriceLLM(markup)` | `model/channel.go:165` | 用户价 = 供货价 × 上浮比例 |

**订阅渠道导入模型后，行为与普通渠道完全一致**：

1. `AutoAddChannelModels` 里已有 `fillSupplyPriceFromReference`（`op/channel_autoadd.go:380`）按参考价兜底填供货价
2. 渠道 `MarkupRatio` 决定上浮
3. 对外报价走 `UserPriceLLM`

**唯一要决定的是**：订阅渠道的供货价算 0（免费订阅，只收上浮）还是填参考价（当作有成本）。
这留到 S5 时按你的实际用途定，现在不阻塞。

### 12.3 决定 3 的落地：多账号**必须先修死代码**（新增工作）

这一条是本次四条决定里**唯一新增的工作量**，必须做在前面。

#### 问题（已核实）

`op.ChannelGrantGet`（`op/channel.go:573-581`）每次返回前都回填 `grant.ChannelKey`，
且 key 被禁用时直接报错返回。于是 `handler.go:368` 的
`if grant.ChannelKey != nil && grant.ChannelKey.Enabled` **恒为真**，
`SelectChannelKey`（`op/channel.go:1472`）在转发主路径**永不执行**。

**后果**：一个 Grant 对应一个 key，所以同一渠道挂 5 个账号时，
**每个账号都必须各自成为 5 个独立的分组成员**，转发时各用各的 key ——
这**恰好能工作**，但意味着：

| 现象 | 说明 |
|---|---|---|
| 一个渠道 N 个账号 → 分组里 N × M 个成员 | M = 该渠道模型数，组合会爆炸 |
| 无法"渠道内挑一个余额多的账号" | 因为挑 key 的代码是死的 |
| `channels.key_rotation_strategy`、`channel_keys.priority` 配置无效 | UI 有、转发不读 |

#### 两个可选修法（**需要你选**）

**修法 A：只在「订阅渠道」启用动态选 key（改动最小，推荐 MVP）**

思路：不给 Grant 绑定 key 的行为动刀，而是给订阅渠道开一条新路。

```go
// handler.go:368 附近改成
if channel.Dialect == model.DialectSubscription {
    // 订阅渠道: 从该渠道所有可用 key 里选, 不看 Grant 绑定的那把
    channelKey, err = op.SelectChannelKey(channelModel.ChannelID, channel.KeyRotationStrategy)
} else if grant.ChannelKey != nil && grant.ChannelKey.Enabled {
    channelKey = grant.ChannelKey
} else {
    channelKey, err = op.SelectChannelKey(...)
}
```

| 优点 | 缺点 |
|---|---|
| 现有渠道行为**零变化** | 订阅渠道的 Grant 仍需绑定"某个"key（只是转发时忽略） |
| 改动 ~20 行 | 分组成员仍需按 key 建（组合爆炸问题不解决） |

**修法 B：让 Grant 可"不绑 key"，key 由渠道统一轮换（彻底）**

思路：`ChannelGrant.ChannelKeyID` 允许为 0/NULL 表示"这个 Grant 用渠道当前可用 key"。

| 优点 | 缺点 |
|---|---|
| 一个渠道 N 个账号 → 分组里只有 M 个成员（不再爆炸） | 改唯一约束 + 迁移 + 所有读 Grant 的地方要处理 NULL |
| 真正实现"渠道内挑账号" | 改动面大（~1.5 天），回归风险高 |

#### 我的建议

**MVP 用修法 A。** 理由：

1. 你现在的诉求是"一个渠道可多账号"**能跑起来**，不是"成员数量最少"
2. 修法 A 对现有 25 个渠道**零影响**，回归风险最低
3. 组合爆炸在 Gemini 场景下不严重：Gemini CLI 一个渠道的模型数通常个位数
4. 修法 B 可以在真实多账号用起来、感到成员膨胀后再做

但**修法 A 有个前提必须满足**：订阅渠道建 key 时，Grant 仍要绑定某个 key，
而转发时忽略它 —— 这需要**明确写注释**说明"订阅渠道的 Grant.ChannelKey 只是占位"，
否则后来的人会以为它生效。这类"看起来生效其实不生效"的字段是我们系统里已经出现过的问题。

---

## 十三、收敛后的实施计划（定稿）

### 13.1 阶段（按你的四条决定重排）

| 阶段 | 内容 | 预估 | 依赖 |
|---|---|---|---|
| **S0** | **能力验证**：直接调 `ProbeChannelModels` 确认 Gemini 能返回模型 | 0.5 天 | 无 |
| **S1** | `DialectSubscription` 常量 + 后端允许 `base_url` 留空（修 `channel_probe.go:50` 那条多余检查） | 0.5 天 | S0 |
| **S2** | `internal/subplatform/gemini.go`：内置 Go 平台描述（登录参数 + 模型发现 + 协议 + 默认头） | 0.5 天 | 无 |
| **S3** | 建渠道接线：spec → channel 字段 → 建 key（account_kind=oauth）→ 拉模型 → 入库 | 1 天 | S1+S2 |
| **S4** | **修 `SelectChannelKey` 死代码（修法 A）**，让订阅渠道可用多渠道 key | 0.5 天 | 无 |
| **S5** | 前端：渠道类型选「订阅」→ 平台下拉 → 登录 → 多账号管理 → 自动填充模型 | 2 天 | S3+S4 |
| **S6** | 定价确认：`MarkupRatio` + 供货价策略（0 还是参考价） | 0.5 天 | S5 |
| | **合计** | **5.5 天** | |

**比原方案 7 天少 1.5 天**，因为 YAML 加载器移出了（决定 1+2），但多了 S4 死代码修复（决定 3）。

### 13.2 交付物检查点

每阶段结束必须可验证，不能靠"应该能跑"：

| 阶段 | 验收标准 |
|---|---|
| S0 | 打印出 Gemini 的模型列表（哪怕只有几个），否则 S3 无法成立 |
| S1 | 建一个 base_url 为空的 `subscription` 渠道能保存成功 |
| S2 | 单测：`gemini.go` 的 spec 字段与现有 `signin/providers.go` 的 Gemini 常量**逐字段一致** |
| S3 | 建渠道后 `channel_models` 表里出现模型行，且 `channel_keys` 有一条 oauth key |
| S4 | **变异测试**：把 `SelectChannelKey` 调用改回去，测试必须失败 |
| S5 | 界面上能：选订阅 → 选 Gemini → 登录 → 加第 2 个账号 → 看到模型 |
| S6 | 对外 `/v1/chat/completions` 走该渠道，账单价格 = 供货价 × MarkupRatio |

**S4 那条特别标注变异测试**：因为我们系统里已经出现过"配置项看起来生效其实不生效"的死代码，
这类修复**必须有测试证明它真的生效**，不能只写代码。

### 13.3 与既有方案的关系（最终）

| 文档 | 状态 |
|---|---|
| `design-magpie-smart-routing.md` | 保留为参考，**本阶段不执行**（额度感知是第二阶段的事） |
| `design-magpie-plugin-system.md` | **作废其"移植"结论**，保留取证部分 |
| `design-subscription-channel.md`（本文） | **执行这份**，且已按你四条决定收敛 |

---

## 十四、S0 的地基风险：已核实解除

**结论：这条链路是完整的，我已经读完了。**（原先标为风险，现已核实）

`op/channel_probe.go:240-258` 的实现：

```go
quota, err := RetrieveUserQuota(ctx, client, dataBase, key, project)
names := quota.AvailableModels()
if len(names) == 0 {
    return nil, errors.New("gemini cli: retrieveUserQuota 未返回任何可用模型...")
}
result := make([]model.ChannelFetchModel, 0, len(names))
for _, name := range names {
    fetched := model.ChannelFetchModel{
        Name:      name,
        Protocols: model.ProtocolGeminiCLI,   // 协议直接写死, 无需推断
        Source:    model.CapabilitySourceModelsDev,
    }
    ...
}
```

`AvailableModels()`（`op/geminicli.go:444-465`）从 `quota.Buckets` 里取 `ModelID`，去重后排序返回。

**所以 S0 从"未验证的地基风险"降级为"确认性检查"。** 但有一个行为必须提前知道：

### 14.1 一个必须让你知道的行为：额度为 0 的模型不会出现

`AvailableModels()` 里有这一行（`op/geminicli.go:458-460`）：

```go
if bucket.RemainingFraction <= 0 {
    continue
}
```

**含义**：`RemainingFraction <= 0` 的模型会被**跳过**，不进导入列表。

| 场景 | 结果 |
|---|---|
| 账号某个模型额度已用完 | 该模型**不出现在**自动导入清单 |
| 账号所有模型额度用完 | `AvailableModels()` 返回空 → 探测**报错**而不是返回空清单（`:244-247` 有明确错误信息） |

这是合理的设计（额度为 0 的模型导进来也用不了），但你要知道：
**订阅渠道的模型清单 = 当前额度可用的模型**，不是"该平台所有模型"。

如果将来需要"列出全部模型、但标记哪些不可用"，要改这一行 —— 现在不改。

### 14.2 S0 现在只需要确认什么

链路已核实，剩下的只是**运行时确认**：用你现有的 Gemini 账号跑一次，
看到模型列表打印出来即可。如果账号已失效，S0 可以用单测喂真实响应样例
（`ParseRetrieveUserQuota` 就是为此导出的，见 `op/geminicli.go:470`）。

---

## 十五、一句话

**四条决定把方案收敛成：一个内置 Gemini 平台描述 + 一个订阅渠道类型 + 一次死代码修复，5.5 天。**

**定价不新造，直接复用 `MarkupRatio → UserPriceLLM`。**

**S0 的地基风险已核实解除**：`RetrieveUserQuota → AvailableModels → ChannelFetchModel` 链路完整，
协议直接写死为 `ProtocolGeminiCLI`。只需运行时确认一次即可。

### 13.4 S1 已完成（2026-11-17）

**S1 交付**：`DialectSubscription` 常量 + `Dialect.Subscribable()` 判定 + 探测与保存两侧放开地址必填。

**实施中被测试发现的第三道关口**（原方案只列了两处）：

| # | 校验点 | 位置 | 处理 |
|---|---|---|---|
| 1 | 保存侧地址必填 | `op/channel.go:301` | 按方言放开 |
| 2 | **SSRF 主机校验** | `op/channel.go:336` `netguard.CheckURL` | 地址为空时跳过（空地址没有主机可查；补全后走同一道校验） |
| 3 | 探测侧地址必填 | `op/channel_probe.go:49` | 按方言放开 |

第 2 条是**原方案漏掉的**：`normalizeChannelConfig` 里有 `netguard.CheckURL`，
空地址会被它以 `upstream url scheme must be http, https, ws or wss, got ""` 拒绝。
这是测试断言“订阅渠道能存空地址”时才暴露的 —— 只改方案里列的两处，S1 会“能探测但存不下来”。

**变异验证（3 次，全部正确抓住）**：

| 变异 | 结果 |
|---|---|
| `Subscribable()` 恒返回 true（放弃地址必填） | FAIL（generic/xunfei/gemini_cli/空 全部 want false） |
| `CheckURL` 无条件执行（回到未修状态） | FAIL（订阅渠道空地址被 SSRF 校验拒） |
| 探测侧去掉 `Subscribable()` 判定 | FAIL（订阅渠道报地址缺失） |

**全量验证**：`go build` OK；`go test ./internal/...` 无失败；`tsc --noEmit` exit 0；`vitest` 60 文件 539 测试全通过。

### 13.5 S4 已完成（2026-11-17，修法 A）

**S4 交付**：订阅渠道在转发主路径改走按渠道选 key，其余方言零变化。

```go
// handler.go:372
if channel.Dialect.Subscribable() {
	channelKey, err = op.SelectChannelKey(channelModel.ChannelID, channel.KeyRotationStrategy)
} else if grant.ChannelKey != nil && grant.ChannelKey.Enabled {
	channelKey = grant.ChannelKey
} else { ... }
```

**故意保留的字段**：订阅渠道的 `ChannelGrant.ChannelKeyID` 仍然非空且被回填，
但它只是占位、不参与本轮选择。代码里写明了这一点 —— 这类"看着生效其实不生效"的字段
正是本系统已经踩过的坑（`key_rotation_strategy`、`channel_keys.priority`）。

**未改动的三处**（都有各自的正当理由）：

| 位置 | 为什么不改 |
|---|---|
| `videotask.go:141` | 视频任务回放**必须**用创建时的那个凭据（任务 ID 绑在那家上游），换 key 就是错 |
| `breaker_probe.go:52` | 熔断探针遍历**每个 Grant** 各用自己的 key，正是要覆盖所有账号；改了会漏探 |
| `op/group.go:482` | 纯展示（成员对应哪个 key），不影响转发 |

**验证（含变异）**：

| 测试 | 作用 |
|---|---|
| `TestSubscriptionChannelRotatesAccounts` | 端到端：同渠道两账号连发两次，上游必须收到两把不同 Bearer |
| `TestGenericChannelKeepsGrantBoundKey` | 边界：普通渠道仍只用 Grant 绑定的那把（承诺"零变化"） |
| `TestSelectChannelKeyRotatesAcrossAccounts` 等 4 个 | `SelectChannelKey` 本身的轮询/优先级/熔断/全不可用 |

**变异**：把 `if channel.Dialect.Subscribable()` 改成 `if false && ...`（即回到死代码状态），
`TestSubscriptionChannelRotatesAccounts` 精准失败并报出
`订阅渠道多账号未轮换, 两次都用 "Bearer token-one" —— SelectChannelKey 仍是死代码`。
还原后全绿。

### 13.6 实施中查清的一个约束：`account_kind=oauth` 的 Key 必须存账号 JSON

这是 S4 调试时花时间最多的点，**值得写进方案**：

- `channel_keys.key` 对 `account_kind=oauth` 存的是**账号 JSON**（含 `access_token` 等字段），
  不是裸 token。用裸字符串当 oauth key 会让该 key 不可用，症状是转发报
  `no available upstream; waiting disabled`（429）—— **报错完全指不到真实原因**。
- 平台不同，JSON 形状也不同：Codex 是顶层 snake_case，Claude 在 `claudeAiOauth` 下且为 camelCase
  （见 `internal/model/account_json.go:76-115`）。

**对 S3/S5 的影响**：S3 建订阅渠道时写 key 必须走 `AccountImport`（它负责解析与校验 JSON），
**不能**直接 `ChannelCreate` 塞 `ChannelKeyConfig{AccountKind: "oauth", Key: <token>}`。
这条如果不提前知道，调试成本就是本次这样的十几轮。

### 13.7 S3 已完成（2026-11-17）

**S3 交付**：平台描述层 + 建渠道自动装配 + 平台清单接口 + 启动校验。

| 新增/改动 | 作用 |
|---|---|
| `internal/subplatform/platform.go` | 平台描述：`ID`/`Name`/`Dialect`/`AccountPlatform`/`BaseURL`/`Description` |
| `internal/subplatform/validate.go` | 自洽校验：方言必须是订阅方言、账号平台必须已登记 |
| `internal/op/channel_subplatform.go` | `applySubscriptionPlatform`：按平台补方言与地址 |
| `internal/model/channel.go` | 新增 `SubPlatform` 字段 |
| `internal/server/handlers/channel_subplatform.go` | `GET /api/v1/channel/subplatform/platforms` |
| `cmd/start.go` | 启动时跑 `subplatform.Validate()`，不通过就 exit 1 |
| `channel_account_signin.go` | 补 `credentialJSONOf` 的 antigravity 分支 |

**装配规则（三条，都有测试钉住）**：

1. 用户填了地址 → **不覆盖**（平台描述是默认值，自建中转/企业专线要能改）
2. 用户没填 → 用平台描述的地址
3. 非订阅方言带平台名 → **报错**（自相矛盾状态）

**`credentialJSONOf` 缺 antigravity 是 S3 发现的真缺口**：它的 `default` 分支报
"平台 X 的登录凭据转换尚未实现"，而 `ProviderOf` **支持** antigravity 登录。
也就是说登录能成功，但换完令牌转凭据这一步会失败 —— 补上了，且复用 gemini 同一份 JSON 形状
（解析层两者共用字段，差异在授权身份不在凭据形状）。

**变异（3 次全部正确抓住）**：

| 变异 | 结果 |
|---|---|
| 无条件覆盖用户地址 | FAIL（`got daily-cloudcode...` want 用户自填地址） |
| 不校验非订阅方言带平台名 | FAIL（"非订阅方言带平台名必须报错"） |
| 未登记平台也放行 | FAIL（"未登记平台必须报错"） |

**S3 让 S4 测试失败了一次，这是正确行为**：S4 建订阅渠道的测试数据没给 `SubPlatform`，
而 S3 要求订阅渠道必须有平台名。已修测试数据（加 `subPlatformForDialect` 辅助），
而不是放宽校验 —— 放宽会让空壳渠道能建起来。

### 13.8 S5 已完成（2026-11-17）

**S5 交付**：渠道表单支持订阅类型 —— 选平台卡片、平台下拉、地址选填、平台清单从后端取。

| 改动 | 作用 |
|---|---|
| `api/channel.ts` | 加 `sub_platform` 字段、`SubPlatform` 类型、`subPlatforms()`、`useSubPlatforms()` |
| `channel/state.ts` | 状态加 `sub_platform`，读写归一化 |
| `channel/Form.tsx` | 订阅时显示平台下拉 + 提示语，地址改为选填 |
| `lib/channel-presets.tsx` | 加订阅模板（地址留空） |
| `locales/*.json` | 3 个新键 × 3 语言 |
| `channel/subPlatformState.test.ts` | 5 个用例 |

**三个设计决定**：

1. **平台清单从前端请求，不硬编码**：后端加平台只要写个 Go 文件，下拉里自然就有。
   前端抄一份清单的代价是后端加了平台前端看不到，而这类漏改只在用户操作时才暴露。
2. **非订阅方言提交时恒发空 `sub_platform`**：后端对"非订阅方言带平台名"是硬报错，
   用户从订阅切回普通方言时若残留旧平台名，保存会被卡死且看不出原因。
3. **订阅渠道仍显示地址输入框，只是改为选填**：藏起来会让自建中转/企业专线用户无处可改，
   与"平台描述是默认值不是强制值"矛盾。placeholder 显示平台默认地址，让用户知道留空会用什么。

**验证**：`tsc --noEmit` 零错误；`vitest run` 61 文件 / 544 测试全过
（原 60/539，新增 5 个）；locale 三个语言各 2681 键且完全一致。

### 13.9 剩余：S2 与 S6

| 阶段 | 内容 | 状态 |
|---|---|---|
| S0 | 死代码确认 | 完成 |
| S1 | 方言 + 地址必填豁免 | 完成（`d47332a`）|
| S3 | 平台描述层 + 建渠道装配 | 完成（`433c406`）|
| S4 | 转发按渠道选 key | 完成（`bdc9923`）|
| S5 | 前端订阅表单 | 完成（本节）|
| S2 | Gemini 平台描述细化（探测/配额/刷新接线核对）| 部分已由 S3 覆盖 |
| S6 | 定价确认（供货价来源）| **待你决定** |

### 13.10 S6 已完成（2026-11-17，方案 A）

**决定**：订阅渠道供货价取 models.dev 参考价（方案 A）。代码无需改动 —— 这条链路原本就存在，
S6 的工作是**证明它生效**并钉住语义。

**链路**：`fillSupplyPriceFromReference`（`channel_autoadd.go:380`）按模型名查 `llmModelCache`
（models.dev 同步来的 `llm_infos`），只填四价全 0 的模型；`UserPriceLLM(markup)` 再按
`factor = 1 + markup` 加成对外。

**实施中查清的两件事（都影响产品行为）**：

1. **预填受 `channel_auto_add_fill_price` 控制，默认 `false`**。
   开关关着时订阅渠道拉进来的模型供货价全 0，而 `0 × (1+markup) = 0` ——
   **渠道看起来配好了，实际全部免费，界面上没有任何地方显示这件事**。
   要用方案 A，部署时**必须打开这个开关**。
2. **参考价缺失时不编假价**（`if !ok { continue }`）。缺失的模型供货价保持 0，
   意味着该模型对客免费。这是"未定价"的真实表示，但也意味着漏同步模型 = 免费送。

**验证（含变异）**：

| 测试 | 钉住什么 |
|---|---|
| `TestSubscriptionChannelPricingFollowsReference` | 供货价取参考价；用户价 = 供货价 ×(1+markup)；参考价缺失时供货价保持 0 |
| `TestSubscriptionChannelMarkupFollowsGlobalWhenUnset` | 渠道 markup 未设(-1)时跟随全局 |

**变异（2 次，第 2 次修正了测试漏洞）**：

| 变异 | 结果 |
|---|---|
| `Scale` 改成 `factor := 1 + 0*ratio`（加价失效） | FAIL（"用户价应为供货价 2 倍 {4,8}，实际 {2,4}"）|
| 参考价缺失时填假价 1 | **第一版测试没抓住** —— 因为测试只把 m0 传给预填函数，m1 从未进入。修正测试让两模型都参与预填后，变成 FAIL（"m1 供货价应为 0，实际 {1,1}"）|

第 2 次变异值得单独记：**如果不做这个变异，我会误以为测试覆盖了"缺失不填"**。
测试当时是绿的，但它只验证了我传进去的那个模型。

### 13.11 S2 已完成（2026-11-17）—— 查出并修了两个真缺口

S2 原计划只是"核对探测/配额/刷新接线"。核对的结论是：**发现两个会让订阅渠道实际不可用的缺口**，
都已修复并用变异验证。

#### 缺口 1：探测分派不认订阅方言（会让"拉模型"完全失效）

S1 只让订阅渠道**允许**空地址，但探测分派（`channel_probe.go:57`）只判断
`Dialect == DialectGeminiCLI`。订阅渠道因此掉进通用分支，去拼 OpenAI/Anthropic 的
`/models` URL —— 而订阅平台根本没有这种端点。

实测错误（变异回退后）：

```
openai: upstream 404 Not Found: 404 page not found;
anthropic: upstream 404 Not Found: 404 page not found (请求路径: [/models /models])
```

**这个报错完全指不到真实原因**：它看起来像"上游挂了"，实际是分派错误。

修法：`if target.Dialect == model.DialectGeminiCLI || target.Dialect.Subscribable() {`。
订阅平台的模型清单只能从平台的配额/可用模型接口拿，没有通用 models 端点可拼。

#### 缺口 2：订阅账号额度耗尽被当成"限流"（会持续浪费请求）

通用分类器把 `resource_exhausted` / 429 判成 `rate`（限流）。这对按量计费渠道是对的 ——
限流窗口过去就恢复。但订阅渠道的 429 是**账户级月度额度耗尽**：用完就是彻底用完，
要等下个计费周期，"冷却几秒再试"永远好不了。

后果：一个额度耗尽的订阅账号会被**持续选中、持续失败**，每次只冷却几秒，
而真正该发生的是渠道级暂停（`op.RecordChannelQuotaError`）让管理员换号。

修法：新增 `classifyUpstreamErrorForChannel(err, dialect)`，**只对订阅方言**把 429 改判为
`quota`；普通渠道与 `gemini_cli` 维持 `rate`（有专门用例钉住这条边界）。

#### S2 验证

| 测试 | 钉住什么 |
|---|---|
| `TestProbeChannelModelsSubscriptionDialectUsesGeminiChain` | 订阅方言走 Gemini RPC 链（断言真的调了 `retrieveUserQuota`）|
| `TestSubscriptionQuotaExhaustedPausesChannel` | 订阅 429 → quota；generic/gemini_cli 429 → rate |

变异：

| 变异 | 结果 |
|---|---|
| 分派回退为只认 `gemini_cli` | FAIL，并打印出 `/models` 请求路径 |
| 撤销订阅渠道的 quota 改判 | FAIL（`class=rate want quota`）|

#### 核对通过、无需改动的部分

| 项 | 结论 |
|---|---|
| 账号刷新 | `AccountRefreshRun` → `AccountOAuthSpecOf(platform)` → antigravity 已登记（`account_oauth.go:188`），链路完整 |
| 额度暂停机制 | `RecordChannelQuotaError` 写 `disabled_until` 并同步缓存，**被动触发**（转发遇额度错误时），无主动轮询任务 |
| 额度暂停选择 | `SelectChannelKey` 首行即查 `ChannelQuotaPaused`，与 S4 的按渠道选 key 天然衔接 |

**"被动触发"这一点要记住**：订阅渠道的额度耗尽是在**第一次转发失败时**才发现，不是提前探测。
配合上面的修法，这一次失败就足以暂停整个渠道，不会反复重试。
