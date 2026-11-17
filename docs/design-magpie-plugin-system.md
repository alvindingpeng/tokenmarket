# 移植 magpie 插件系统 —— 方案（先结论，再细节）

> 状态：**方案稿，未实现**。magpie 结论全部来自源码取证（/tmp/magpie-src，带 文件:行号）；
> 本系统结论来自当前仓库取证与生产库实测。凡未经实证的判断，文中标注「未验证」。

---

## 一、结论先行

### 1.1 你问的两件事，其实是两件不同的事

你把「插件系统」和「添加订阅账号」当成一件事，但在 magpie 里它们**分属两套机制**：

| | 内置插件（plugins/） | 外部插件（OpenCode 风格，Bun/JS） |
|---|---|---|
| 形态 | **编译进主程序的 Go 包**（无 build tag，不可选编译） | **Bun 子进程里的 JS/npm 包** |
| 加载 | plugins/all **全部 import（无条件编译）**，plugins.yml 只是**运行期**开关 | 运行时安装，host.js 用 stdin/stdout JSON 通信 |
| 能做什么 | 页面、路由策略、中间件、预算、脱敏……**共 6 个既有扩展点，不能新增 provider** | **定义新的订阅服务**（provider）、页面、工具 |
| 是否与「加账号」有关 | **无关**（它连 provider 都不碰） | **正是「加账号」的机制** |

**能让你「添加订阅账号」的，是第二套（外部 OpenCode 插件），不是第一套。**
第一套只是「把 magpie 自己的功能拆成可选模块」，移植它对「加订阅账号」毫无帮助。

### 1.2 外部插件确实能做到「加订阅账号」（已核实）

插件在 package.json 里**自己声明一个 provider**（internal/plugin/providers.go:149-165）：

```go
type Provider struct {
	ID      string   // OpenCode 的: google, github-copilot
	Spec    string   // the plugin
	Name    string
	API     string
	Methods []Method // 登录方式: oauth / api key
	Usage   bool     // 能否报额度 (auth.usage)
}
```

再通过一个 auth hook 提供 authorize（登录）、usage（额度）、fetch（把请求转发到该上游并翻译）。
于是 magpie **不需要改一行主程序代码**，就能多出一个订阅类型。

这就是 magpie 的「添加订阅账号」能力从哪来的答案。

### 1.3 但这个方案在你的系统里，性价比很差（核心判断）

移植外部插件系统，等于要引进并长期维护：

| 组件 | 现实成本 |
|---|---|
| **JS 运行时** | 我们现在 go.mod **零 JS 运行时依赖**（无 goja/otto）。magpie 用的是 **Bun 可执行文件**（internal/plugin/bun.go），要随程序分发、要处理 musl/glibc（bunmusl_test.go）、要处理进程崩溃（bunload_test.go 用 kill -ILL/-SEGV 测） |
| **npm 生态** | 要能装 npm 包、解析版本、下载校验（sha512，dist.go:34） |
| **宿主协议** | stdin/stdout 逐行 JSON、流式 head/chunk/credit 背压、abort（host.js 头部注释） |
| **安全边界** | **magpie 的插件没有沙箱**（internal/plugin 下无 sandbox 实现）。插件是任意代码，能读能发网络。你的系统是**多用户网关**，开放插件等于把服务器交给第三方 npm 包 |

**换句话说**：这不是「移植一个功能」，是「在你的 Go 项目里嵌一个 JS 运行时生态」。
对一个人维护的项目，这是负债不是资产。

### 1.4 你真正的目标，有一条便宜得多的路

你要的是「**能添加订阅账号**」，不是「要有插件系统」。这两者不等价。

订阅账号的完整生命周期其实是四件事，插件只是**让外部代码填这四件事**的容器：

1. **登录**（OAuth 授权码/设备码，拿到 refresh_token）
2. **刷新**（用 refresh_token 续 access_token）
3. **列模型**（问上游有哪些模型）
4. **转发**（把客户端请求翻译成该上游格式）

**我们系统这四件事全都有了**，而且已经是**表驱动**的，不是硬编码：

| 件 | 位置 | 现状 |
|---|---|---|
| 刷新端点注册表 | internal/model/account_oauth.go:113 accountOAuthSpecs | **map 注册表**，加平台只需加一个条目 |
| 平台清单 | 同上，已有 14 处 AccountPlatform | codex / gemini / claude / kiro / antigravity … |
| 登录流程 | internal/signin/providers.go | 已有 OAuth + 回调 + 会话 |
| 列模型 | internal/op/channel_probe.go | 已按 dialect 分发 |
| 转发 | internal/relay/channel.go + Protocol 位掩码（model/channel.go:6-18） | 已支持 7 种协议 |

**所以「加一个新订阅平台」在现有架构下 ≈ 加一个注册表条目 + 一个探测函数 + 一个 Protocol 常量。**
不需要插件系统。

### 1.5 结论

> **不建议移植 magpie 的插件系统。**
> 它解决的是「社区要能给 magpie 加厂商、而 magpie 作者不想合并代码」这个**开源项目特有**的问题。
> 你的系统不存在这个问题（你自己就是作者）。
>
> **更硬的理由（见 2.6）**：插件**不能自定义协议**——magpie 按 npm 包名把协议写死成
> chat/responses/anthropic/gemini 四种（provider/plugins.go:256-265）。
> 而你最想加的 Gemini CLI（`ProtocolGeminiCLI`）与讯飞星火（`ProtocolXunfeiSparkWS`）
> **都不在这四种里**。**移植了也解决不了你最想解决的问题。**
>
> **要做的是：把「加平台」这件事从代码改动，降级为配置/注册表驱动。**
> 这条路 3-5 天，且不引入 JS 运行时与第三方代码执行风险。

---

## 二、magpie 插件系统取证（供判断，不想看可跳到第九节）

### 2.1 两套机制的区分（关键）

docs/subsystems/plugin-runtime.md:1-8 原文：

> magpie 的 built-in plugins are the parts of magpie not everyone needs, each one a package under
> plugins/ that the user can switch off. The runtime in internal/pluginrt keeps their registry…

plugins/ 下有 15 个 Go 包：all budget gateway groups library middleware otel redact rotation routing search sessions tools usage warmups。
plugins/all/all.go 把它们全部 import，且注释写明：

> Only magpie 的 main imports it: core never imports a plugin, and a plugin never imports another.

这是**编译期**的模块化，作用域**只在 magpie 自己内部**。

### 2.2 外部插件 = OpenCode 协议的 JS 插件

internal/plugin/host.js:1-6 原文：

> magpie 的 plugin host: runs OpenCode (v1) server plugins under Bun and
> answers magpie over stdin/stdout, one JSON message a line.
>
> magpie → host: {id, method, params}; the host answers {id, result} or {id, error}.
> A fetch streams its reply first: {id, event: "head", …}, then {id, event: "chunk", data} …

**是子进程 + JSON 行协议**，不是 Go plugin，不是 wasm。

### 2.3 插件如何「加订阅账号」

插件在 package.json 声明 provider，宿主把它变成 plugin.Provider（internal/plugin/providers.go:149），
magpie 的 provider 层再把它与内置 provider 并列（internal/provider/plugins.go）。

docs/subsystems/provider-plugins.md:23-27 给了所有权矩阵：

| 操作 | 内置拥有该订阅 | 插件拥有该订阅 |
|---|---|---|
| 登录、刷新、模型 | 内置 provider 实现 | 社区插件经 magpie 的插件集成 |
| **上游请求与翻译** | 内置 gateway adapter | **插件 fetch，经插件宿主** |
| 用量与上游错误 | 内置实现 | 插件，经 magpie 的 provider 集成 |

### 2.4 插件的能力面（P4 社区扩展点，plugin-runtime.md:269-281）

| 扩展点 | 插件提供什么 |
|---|---|
| Router strategy | export const strategies = {name: (pool, seats, options) => order} |
| Usage observer | export function onUsage(row, options) |
| Search API | search hook 的条目 |
| Tool | OpenCode tool hook，经 MCP /mcp/magpie/tools 暴露 |
| Window page | magpie.page {title, file, icon}，iframe allow-scripts 沙箱 |

**注意：这个表里没有「新增 provider」。** provider 来自另一条路径（package.json 声明 + auth hook），
不在这五个「扩展点」里。这是 magpie 文档结构上一个容易误读的地方。


### 2.6 一条最具决定性的限制：**插件不能自定义协议**

这是移植 magpie 插件方案在本项目里的**致命缺陷**，必须单独写清楚。

插件的 "forward" 能力看着最强，但协议（wire protocol）是 magpie **按 npm 包名写死判定**的，
见 internal/provider/plugins.go:256-265：

```go
func pluginProtocol(provider string, m plugin.Model) Protocol {
	switch m.NPM {
	case "@ai-sdk/anthropic", "@ai-sdk/google-vertex/anthropic":
		return Anthropic
	case "@ai-sdk/openai", "@ai-sdk/azure":
		return Responses
	case "@ai-sdk/google":
		return CodeAssist
	case "@ai-sdk/github-copilot":
	
	}
}
```

**插件只能落在 chat / responses / anthropic / gemini 这四种已有协议上，不能定义第五种。**

这对我们意味着什么 —— 我们系统最需要"加订阅"的那些上游，恰好都不是这四种：

| 我们已有的协议 | 对应本系统常量 | magpie 插件能否表达 |
|---|---|---|
| OpenAI Chat / Responses / Anthropic | `ProtocolOpenAI*` / `ProtocolAnthropicMessage` | 能 |
| **Gemini CLI 信封（v1internal:generateContent）** | `ProtocolGeminiCLI`（channel.go:18） | **不能**（magpie 的 CodeAssist 是 google 原生，不是 CLI 信封） |
| **讯飞星火原生 WebSocket** | `ProtocolXunfeiSparkWS`（channel.go:17） | **不能** |

也就是说：**即便你花 19 天把插件系统搬过来，你最关心的 Gemini CLI / Antigravity 那条路径，
插件依然表达不出来 —— 它需要我们现在的 `ProtocolGeminiCLI` + 信封改写代码。**

这条比"安全风险"更能决定结论：**移植了也解决不了你最想解决的问题。**

### 2.5 安全边界（重要）

- 插件的**页面**有沙箱：iframe sandbox allow-scripts，connect-src none，只能 postMessage 声明过的读接口（plugin-runtime.md:275）。
- 插件的**主进程代码（fetch/auth）没有沙箱**：它就是 Bun 里的任意 JS，能 import node:fs/net/tls/http2/dns。
  宿主只对 `$` 与 spawn 加了 cwd/stdio 重定向（host.js:259-268），**不是权限限制**。
- **最严重的一条（已核实，比我初稿写的更糟）**：宿主把 plugin-auth.json 的**明文凭据文件路径**
  直接交给插件，见 internal/plugin/host.go:1177 `"authPath": AuthPath()`。
  插件能读**全部**账号的 token。
- **环境变量全量继承**：host.go:2061-2075 的 hostEnv 只把代理变量改名，其余走 `default` 分支原样传下去。
  也就是说 systemd 注入的 SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID/SECRET
  （我们生产就是这么放密钥的，见 /opt/switchyard/keys/antigravity-oauth.env）同样会被子进程读到。
- 完整性校验：dist tarball 是 **sha512**（internal/plugin/dist.go:34、167），npm 包靠 npm integrity；
  **没有插件签名、没有发布者校验**。GitHub topic 插件的源码注释自己写着 "not reviewed by anyone"
  （internal/plugin/github.go:18-21）。

**对一个多用户网关来说，这是不可接受的风险面。** 你的系统有 25 个渠道、真实密钥、多用户计费，
而且密钥同时存在于两处插件都能读到的地方：/opt/switchyard/keys/ 下的文件，与进程环境变量。

**对一个多用户网关来说，这是不可接受的风险面。** 你的系统有 25 个渠道、真实密钥、多用户计费。

---

## 三、我们系统现状（决定移植成本）

### 3.1 已经是注册表驱动（好消息）

internal/model/account_oauth.go:111-123：

```go
// accountOAuthSpecs 平台 -> 令牌端点规格。未登记的平台的 OAuth 刷新会明确失败, 而不是
// 拿一个猜出来的端点去打上游 —— 打错端点最坏的情况是让对方以为我们在暴力破解。
var accountOAuthSpecs = map[string]AccountOAuthSpec{
	AccountPlatformCodex: { TokenURL: "https://auth.openai.com/oauth/token", … },
	AccountPlatformKiro:  { TokenURL: "https://oidc.us-east-1.amazonaws.com/token", … },
	…
}
```

这条注释还体现了我们已经在乎的一个安全属性：**未登记平台明确失败，不猜端点**。

### 3.2 缺口在哪（真正要补的）

| 缺口 | 现状 | 补法 |
|---|---|---|
| 平台是**编译期常量** | 14 处 AccountPlatform 常量 + 注册表 | 15-20 行代码加一个平台，但**要重新编译** |
| **不能用户自定义** | 没有运行时添加平台的入口 | 需要「动态平台」表 |
| 登录流程**参数按平台写死** | signin/providers.go 顶部每个平台一组常量（claudeAuthorizeURL / codexAuthorizeURL …），但分发只有 **1 处 `case`**（:142 是唯一的 AccountPlatform 分支，其余走默认） | **比预想简单**：把这些常量搬进注册表即可，不需要重构控制流 |
| 探测**按 dialect 硬编码** | channel_probe.go | 已有分发点 |

### 3.3 关键差异

magpie 允许**任意第三方代码**接管上游请求 → 灵活但危险。
我们的架构是**协议位掩码 + 注册表** → 只能加「已有协议形状」的平台，但安全可控。

**你现在需要的是加 Gemini/Antigravity/Claude/Codex 这类已知协议形状的订阅，
不是让别人给你写任意转发逻辑。** 后者用不上，前者现有架构直接支持。

---

## 四、推荐的方案：**可配置订阅平台**（不是插件系统）

### 4.1 目标（一句话）

**让你不重新编译，就能在界面上添加一个新的订阅平台**（填端点、填 scope、填 client、选协议形状），
系统据这条配置完成登录、刷新、列模型、转发。

### 4.2 为什么这条路对应你的真实需求

| 你要的 | 插件系统给的 | 本方案给的 |
|---|---|---|
| 加一个订阅平台 | 能，但要写 JS 包并信任它 | 能，界面填表 |
| 改上游端点 | 能 | 能 |
| 自定义转发逻辑 | 能（任意代码） | **不能**（只能用已有协议形状） |
| 安全 | 第三方代码无沙箱 | **无外部代码执行** |
| 维护成本 | 一个 JS 运行时生态 | 一张表 + 三个函数指针位 |

只有「自定义转发逻辑」这一项输给插件系统。**但你现在并不需要它** —— 你要的 Gemini/Antigravity 是已知形状。

### 4.3 设计：把 accountOAuthSpecs 从编译期 map 变成「内置 + 动态」两层

```
现有:                              改成:
  accountOAuthSpecs (map 常量)      内置 accountOAuthSpecs (不变, 编译期)
                                    + 新表 platform_specs (数据库, 运行时)
                                    = 查找时先内置, 再动态
```

**新表 `platform_specs`**（新增迁移）：

```sql
CREATE TABLE platform_specs (
    id              INTEGER PRIMARY KEY,
    platform        TEXT NOT NULL UNIQUE,   -- 平台标识, 如 my_gemini_alt
    display_name    TEXT NOT NULL,
    token_url       TEXT NOT NULL,          -- 刷新端点
    client_id       TEXT NOT NULL,
    client_secret   TEXT,                   -- 信封加密, 可空(公共客户端)
    scopes          TEXT NOT NULL,          -- 空格分隔
    body_format     TEXT NOT NULL,          -- form | json
    authorize_url   TEXT,                   -- 授权端点, 可空(设备码/无交互)
    device_code_url TEXT,
    probe_dialect   TEXT NOT NULL,          -- 复用现有 dialect 枚举
    protocol        INTEGER NOT NULL,       -- 复用 Protocol 位掩码
    extra_headers   TEXT,                   -- JSON, 如 Antigravity 那三行
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at, updated_at
);
```

注意 `client_secret` 必须走现有信封加密（`serializer:switchyard_secret_*`），不能明文落库。

### 4.4 三个改动点（都不大）

| # | 改动 | 位置 | 工作量 |
|---|---|---|---|
| 1 | `AccountOAuthSpecOf` 先查内置 map，未命中再查 `platform_specs` | internal/model/account_oauth.go:237 | 0.5 天 |
| 2 | `signin.Provider` 的构造改成从 spec 拼装，而不是每平台一个分支 | internal/signin/providers.go | 1 天 |
| 3 | 前端「添加订阅平台」表单 + 列表 + 删除保护 | web/src/components | 1.5 天 |

### 4.5 明确不做的事（防止过度设计）

- **不引入 JS/Go 插件运行时**
- **不引入 npm 生态**
- **不允许上传代码**
- **不做插件沙箱**（因为没有代码要跑）

### 4.6 风险与约束（必须承认的）

1. **只能表达「OAuth + 已有协议形状」的平台**。需要特殊签名（如 AWS SigV4）、特殊信封的，仍要写代码。
2. **动态平台的安全性由填写者负责**。它是管理员功能，必须限定 role=admin。
3. **`client_secret` 落库有泄露风险**，必须用现有信封加密 + 不回显给前端。
4. **未验证**：我没有验证过「动态 spec 能否覆盖 Antigravity 的全部需求」（它需要 3 个特殊 header + 特殊信封）。
   很可能 Antigravity 这类仍需走内置代码路径。**建议先做能力验证再开工。**

---

## 五、如果确实要「插件感」：轻量替代（推荐折中）

你想要的可能是「**以后加平台不用我改代码**」，不一定是「开放插件生态」。
这两者的最小实现是**声明式平台清单**：

### 5.1 用一个 YAML/JSON 文件描述平台，放配置目录

```yaml
# /opt/switchyard/platforms/mygemini.yaml
platform: mygemini
display_name: My Gemini
protocol: gemini_cli
probe_dialect: gemini_cli
token_url: https://oauth2.googleapis.com/token
authorize_url: https://accounts.google.com/o/oauth2/v2/auth
scopes: [cloud-platform, userinfo.email]
client_id: xxx.apps.googleusercontent.com
headers:
  User-Agent: antigravity/1.1.0
```

启动时扫描该目录，合并进注册表。**效果与 4.3 相同，但不需要新增数据库表和前端 CRUD。**
代价：改平台要动文件（对有 ssh 的你，这不是问题）。

### 5.2 对比

| | 数据库表 + 前端表单（4.3） | 配置文件（5.1） |
|---|---|---|
| 工作量 | ~3 天 | **~1.5 天** |
| 改平台方式 | 界面点选 | 编辑文件 + 重启 |
| 需要前端 | 是 | **否** |
| 适合谁 | 多管理员/团队 | **你一个人** |

**如果只有你用，我建议 5.1。**

---

## 六、实施顺序与工作量

### 6.1 若走推荐方案（5.1 配置文件版）

| 阶段 | 内容 | 预估 |
|---|---|---|
| 0 | **能力验证**（见 4.6-4）：用一份 YAML 描述现有 gemini_cli，确认能跑通登录+刷新+列模型 | 0.5 天 |
| 1 | `platform_specs` 加载器（扫目录、解析、校验、合并注册表） | 0.5 天 |
| 2 | `signin.Provider` 改为 spec 驱动 | 0.5 天 |
| 3 | 文档 + 一份真实示例 | 0.5 天 |
| | **合计** | **~2 天** |

### 6.2 若走 4.3（数据库 + 前端）

| 阶段 | 内容 | 预估 |
|---|---|---|
| 0 | 同上能力验证 | 0.5 天 |
| 1 | `platform_specs` 表 + 迁移 + 信封加密 | 0.5 天 |
| 2 | `AccountOAuthSpecOf` 双层查找 | 0.5 天 |
| 3 | `signin.Provider` spec 驱动（已核实分发只有 1 处 case，参数搬表即可，比我原估的轻） | 0.5 天 |
| 4 | 前端 CRUD + i18n | 1.5 天 |
| | **合计** | **~4 天** |

### 6.3 若坚持移植 magpie 插件系统（不推荐）

| 阶段 | 内容 | 预估 |
|---|---|---|
| 1 | 引入 Bun 运行时 + 可执行文件分发 + musl/glibc 适配 | 3 天 |
| 2 | host.js 等价物（stdin/stdout JSON 行协议、流式 head/chunk/credit、abort） | 4 天 |
| 3 | provider 声明解析 + auth hook（authorize/usage/fetch） | 4 天 |
| 4 | npm 安装、版本、sha512 校验 | 2 天 |
| 5 | 安全评估（无沙箱下如何限制） | 3 天+ |
| 6 | 前端插件管理页 | 3 天 |
| | **合计** | **~19 天，且长期维护一个 JS 生态** |

**19 天 vs 2 天，且前者给你一个安全负债。** 这是我不建议移植的量化理由。

---

## 七、magpie 里真正值得抄的三件事（与插件无关）

| 值得抄 | 出处 | 为什么 |
|---|---|---|
| **登录方式声明化**（methods: oauth / api / keyless） | internal/plugin/providers.go:28-40 | 把「怎么登录」变成数据，我们正好缺这个 |
| **插件的 provider 与内置 provider 并列** | internal/provider/plugins.go | 思路：不要给「内置」和「动态」写两套代码路径 |
| **`auth.single: true`（一个 provider 只允许一个身份）** | provider-plugins.md:389-392 | 防止同一账号重复登录踩坑，我们的 gemini 授权也该有 |

**不值得抄**：Bun 运行时、npm 安装机制、stdin/stdout 宿主协议、页面 iframe 沙箱（我们没有插件页面）。

---

## 八、需要你确认的三件事

1. **你要的是「我以后加平台不用改代码」，还是「别人能给我的系统写插件」？**
   - 前者 → 走第五节（~2 天）
   - 后者 → 走第三节分析，我建议不做，理由在 1.3/2.5

2. **你下一个想加的订阅平台是哪个？** 这决定 4.6-4 的能力验证怎么做。
   如果是 Gemini/Antigravity 这类，现有代码已经支持，根本不需要新机制。
   如果是某个只有 API Key、但协议形状特殊的站点，可能真的需要代码。

3. **是否需要「动态平台」能力兜底？** 如果答案是「我大概每季度加一个新平台」，
   那 2 天的配置文件方案很划算。如果「我基本不加了」，那连这个都不用做。

---

## 九、一句话总结

**magpie 的插件系统解决的是「开源项目如何让社区加厂商」，你是自己项目的主人，
这个问题不存在。**

**你真正要的「添加订阅账号」，在现有架构下是加一个注册表条目；
要做成不用改代码，配置文件方案 2 天就够，不需要一个 JS 运行时生态。**

**移植插件系统 = ~19 天 + 长期维护 + 无沙箱的第三方代码执行风险，且大部分能力你用不上。**

---

## 附：本次取证的文件清单（可复核）

| 文件 | 用途 |
|---|---|
| /tmp/magpie-src/docs/subsystems/plugin-runtime.md (548 行) | 内置插件运行时 P0-P6 全貌 |
| /tmp/magpie-src/docs/subsystems/provider-plugins.md (433 行) | 插件与 provider 的所有权、迁移 |
| /tmp/magpie-src/docs/subsystems/providers-accounts.md (526 行) | 账号模型 |
| /tmp/magpie-src/internal/plugin/host.js (171KB) | Bun 宿主，stdin/stdout JSON 协议 |
| /tmp/magpie-src/internal/plugin/providers.go:149 | plugin.Provider 结构 |
| /tmp/magpie-src/internal/plugin/providers.go:28 | plugin.Method 结构（登录方式） |
| /tmp/magpie-src/internal/plugin/bun.go:300 | sha512 校验（唯一的完整性校验，且无签名） |
| /tmp/magpie-src/plugins/all/all.go | 内置插件装配 |
| internal/model/account_oauth.go:113 | 我们的 accountOAuthSpecs 注册表 |
| internal/model/account_oauth.go:237 | AccountOAuthSpecOf 查找入口 |
| internal/signin/providers.go | 我们的登录流程 |
