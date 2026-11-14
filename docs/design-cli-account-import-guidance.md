# 方案 A: 本机 CLI 取凭据的导入引导

> 背景: 用户提出「导入账号能不能用 CLI 连接订阅账号」。
> 调研结论(见下)把这件事分成两半: 服务端发起 OAuth 在我们这个服务器部署形态下做不到
> (回调打不到用户的 localhost), 而**引导用户在本人机器上跑官方 CLI、
> 再把产出的凭据文件导入**是现在就能做且真正有价值的。
> 本文只做 A 部分, 且**所有路径与命令都标注了证据来源**。

## 0. 一句话结论

我们**不新增任何认证协议**, 只把「去哪跑什么命令、产出在哪个文件、长什么形状」写进导入界面。
用户在本机登录一次, 拿到文件, 贴进现有的导入框 —— 后端解析与刷新链路完全复用。

## 1. 证据来源

不是为了凑文档去搜二手博客。下面的结论来自**官方发布物本身的字符串**,
可用同样方法复核。

### 1.1 Claude Code — 路径与形状均已实证

来源: `npm pack @anthropic-ai/claude-code-linux-x64@2.1.325` 的真实二进制, `strings` 提取。

| 项 | 证据(二进制原文) |
|---|---|
| 登录命令 | `claude auth login`(二进制原文: "Run ``claude auth login`` (or /login in a local session)") |
| 凭据路径 | `Read(~/.claude/.credentials.json)` |
| 覆盖变量 | `storagePath = <CLAUDE_CONFIG_DIR 或 ~/.claude> / ".credentials.json"` |
| 内容形状 | `claudeAiOauth` 键(camelCase), 与我们的解析分支一致 |
| 授权端点 | `https://platform.claude.com/oauth/authorize` |

**这条最重要**: 我们 `AccountJSONParse` 的 claude 分支读的就是 `claudeAiOauth.accessToken` /
`.refreshToken` / `.expiresAt`(毫秒), 与官方文件形状**完全吻合**。
也就是说 Claude 这条链**今天就能用**, 缺的只是告诉用户文件在哪。

### 1.2 Codex — 路径已实证, 形状本已支持

来源: `npm pack @openai/codex@0.169.0-linux-x64` 的真实 Rust 二进制。

| 项 | 证据(二进制原文) |
|---|---|
| 登录命令 | `Run codex login`; 另有 `codex login --with-api-key` / `--with-access-token` |
| 凭据文件 | `auth.json`, 位于 `CODEX_HOME`(默认 `~/.codex`) |
| 内容形状 | 顶层含 `OPENAI_API_KEY` / `tokens`; 我们的解析器已支持 `tokens` 解包 |

### 1.3 Gemini — 路径已实证, 但**与我们现有解析不兼容**

来源: `npm pack @google/gemini-cli@0.68.0`。

| 项 | 证据 |
|---|---|
| 凭据路径 | `~/.gemini/oauth_creds.json`(沙箱规则里明确列出) |
| 内容形状 | `access_token` / `refresh_token` / `expiry_date`(**OAuth 令牌**) |

⚠️ **这是本方案唯一的硬冲突**: 我们的 `gemini` 平台解析的是 **Cookie** 形状
(`c_ses` / `c_oses` / `csesidx` / `config_id`), `Kind = cookie`。
而 Gemini CLI 产出的 `oauth_creds.json` 是 **OAuth 令牌**。

把 `oauth_creds.json` 贴进 gemini 导入框, 结果是:
`gemini: 缺少 c_ses` —— 报错, 且报错内容会误导人(用户明明拿到了合法的凭据)。

**这不是文档能解决的, 是解析能力缺口。** 见 §4 的取舍。

### 1.4 Kiro — **没有官方 CLI**

`npm view @aws/kiro-cli` → 404; npm 上只有一个 unrelated 的占位包(`kiro-cli` 0.0.1,
由个人发布, 描述自述为 "placeholder")。

Kiro 的凭据目前只能从 Web 控制台 / 第三方工具获取。
**本方案不会给 Kiro 编一条 CLI 命令**, 因为没有官方 CLI 可指向。

### 1.5 Grok — 未核实到官方 CLI

`npm view grok-cli` 有 1.0.5, 但**未核实其是否为 xAI 官方发布**, 也未核实凭据落盘路径。
我们的 grok 平台要的是 `sso` **Cookie**, 而任何 OAuth CLI 产出的都会是令牌, 大概率同样是 §1.3 那类冲突。

**结论: Grok 本轮不给引导**, 等核实来源再说。编一条来源不明的命令比不给更糟。

## 2. 平台支持矩阵(本方案的真实增量)

| 平台 | 我们现有解析 | 官方 CLI | CLI 产出形状 | 能否直接导入 |
|---|---|---|---|---|
| **Claude** | `claudeAiOauth.accessToken` (oauth) | `claude` | `claudeAiOauth.*` | ✅ **形状吻合, 直接可用** |
| **Codex** | `tokens.*` (oauth) | `codex login` | `auth.json` + `tokens` | ✅ **已支持, 直接可用** |
| Gemini | `c_ses` (cookie) | `gemini` | `oauth_creds.json` (oauth) | ❌ **形状不兼容** |
| Kiro | `refreshToken`/`clientId`/`clientSecret` (oauth) | **无官方 CLI** | — | ❌ 无 CLI 可引导 |
| Grok | `sso` (cookie) | 未核实 | 未知 | ❌ 不编 |

**所以 A 的真实交付面是 Claude 与 Codex 两个平台。** 其余三个不给引导,
理由各不相同但都是"没有可靠证据"。

## 3. 交互设计

在现有导入对话框(`AccountImport.tsx`)的**平台按钮下方**加一块可折叠的"如何获取凭据"提示:

```
[codex] [kiro] [claude] [gemini] [grok]
┌─ 如何获取这份凭据 ───────────────────────────┐
│ 1. 在你自己的电脑上运行:  codex login        │
│ 2. 打开/导入文件:  ~/.codex/auth.json        │
│    (Windows: %USERPROFILE%\.codex\auth.json) │
│ 3. 把该文件的全部内容粘到下面的输入框          │
└─────────────────────────────────────────────┘
```

要点:
- **只在支持 CLI 引导的平台显示**(claude / codex)。选 kiro/gemini/grok 时**不显示**,
  而不是显示一条"暂无 CLI"的空框 —— 空框会让人以为是加载失败。
- 路径给出**复制按钮**, 命令给出一键复制。
- 明确写"在你的**自己的电脑**上运行": 这是 A 与 B 的分界, 避免用户以为我们帮他登录。
- 每个平台附一句该文件敏感性的提醒(它等价于账号密码)。

## 4. Gemini 的取舍 —— 需要你决定

Gemini CLI 的 `oauth_creds.json` 是真实且有用的凭据, 但我们**吃不下**。
两条路:

**方案 G1: 扩解析分支, 让 gemini 也支持 OAuth 令牌形状。**
- 改动: `account_json.go` 的 gemini 分支, 按"有 `access_token` 就走 oauth, 有 `c_ses` 就走 cookie"分流;
  `Kind` 相应变化; 需要新增 gemini 的 token 端点规格(`account_oauth.go`)才能自动续期。
- 风险: gemini 的 OAuth 端点/client_id 我们**没有核实过**。不核实就写规格,
  等于让"刷新"这条链带着未验证的假设上线 —— 而刷新写错会让账号静默失效。
- 成本: 一个平台的全套(解析+端点规格+刷新+测试+预检+三语文案)。

**方案 G2: 本轮不做 Gemini CLI 引导, 只做 Claude 与 Codex。**
- 干净、零风险, A 的两个平台先落地。
- Gemini 继续走现在的 cookie 路径(那是给"从 Web 会话抠出 Cookie"的用户用的)。

**我建议 G2**, 然后把 G1 作为独立立项 —— 它需要先核实 Gemini 的 OAuth 端点,
那是和"A"完全不同量级的工作。

---

### 4.1 G1 已完成(2026-11)

端点核实完毕, 全部取自 `npm pack @google/gemini-cli@0.68.0` 的二进制字符串:

| 项 | 值 | 证据 |
|---|---|---|
| token 端点 | `https://oauth2.googleapis.com/token` | bundle 内 google-auth-library 常量 |
| client_id / client_secret | **运行时配置**, 见下 | `var OAUTH_CLIENT_ID = ...` / `OAUTH_CLIENT_SECRET = ...` |
| 请求体 | form-urlencoded + `grant_type=refresh_token` | 官方 Node 客户端的实际做法 |
| 响应 | `access_token` / `expires_in` (snake_case) | 标准 Google OAuth 响应 |
| 凭据文件 | `HOME_DIR/.gemini/oauth_creds.json` | 沙箱规则原文 |
| 登录方式 | **会话内 /auth 斜杠命令** | 发行包内只定义了 mcp/extensions 等子命令, 无 auth/login |

### 4.2 client 身份为什么改成运行时配置

发行包里能提取到 Google 分配给 Gemini CLI 的 `client_id` 与 `client_secret`。
第一版实现把它们写死进了代码 —— 随后 **GitHub push protection 直接拦下推送**,
把两项都判成 secret。

拦下这次之后重新想了一遍, 结论是**它本来就不该写死在仓库里**:

- 那是 Google 给 Gemini CLI 这个**应用**的身份。能从公开包体里提出来, 不等于我们有权
  以该应用的身份分发与使用 —— 写进代码等于把冒用行为固化, 让每个部署者都在做。
- 部署者应当用自己注册的 Google OAuth 客户端: 自己的配额、自己的责任;
  也避免所有部署挤在同一个 client 上被上游限流。

所以改成读环境变量:

| 变量 | 说明 |
|---|---|
| `SWITCHYARD_GEMINI_OAUTH_CLIENT_ID` | 部署自行注册的 Google OAuth client_id |
| `SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET` | 同上, client_secret |

未配置时 gemini 的 OAuth 刷新**明确失败并指出该配哪两个变量**, 而不是带着空 client
去打上游 —— 后者会回 `invalid_client`, 被读成"账号坏了", 排查方向完全错。
Cookie 形状的 gemini 账号不受影响, 无需任何配置。

**副产品:** 包里有**两个** `apps.googleusercontent.com` 形态的 client, 另一个是
google-auth-library 自带的 `CLOUD_SDK_CLIENT_ID`。两者长得几乎一样, 用错会得到
`invalid_client`。这是核实阶段真实踩到的坑, 记在这里避免将来重犯。

**真实端点验证**(用假 refresh_token 打真实端点做对照实验):

| 请求 | 响应 | 结论 |
|---|---|---|
| 正确 id/secret + 假 refresh_token | `invalid_grant` | 端点、凭据、格式全部正确 |
| 错误 client_secret | `invalid_client`: "client secret is invalid" | 证明 secret 必须正确 |
| JSON 请求体 | 同样 `invalid_grant` | 端点其实也接受 JSON —— 选 form 是为跟随官方客户端 |

**实现要点:** 解析层按"有没有 refresh_token"分流(而不是按平台名定死), 让同一平台下
Cookie 与 OAuth 两种凭据共存; `expiry_date` 按**毫秒时间戳**解析(按字符串解析会恒为零,
导致定时任务永不刷新、账号静默失效); `StoreAccessTokenField` 显式写成 snake_case
(与 Claude 的 camelCase 相反); 前端预检同步放宽, 否则后端支持了、前端却把人拦在门外。

**gemini 因此也进了 CLI 引导表** —— 但这张表的每一行对应的是"确实能用"的路径,
不是"有这个 CLI"就算数: gemini 在 G2 阶段就是因为解析吃不下才被刻意留空的。

## 5. 落点清单(若按 G2)

### 5.1 前端

| 文件 | 改动 |
|---|---|
| `web/src/components/modules/channel/AccountImport.tsx` | 加 `CliHint` 子组件; 平台切换时渲染对应提示; 复制按钮 |
| `web/src/components/modules/channel/AccountImportBatch.tsx` | 批量框里加一行说明: 同一平台的多个账号用同一个文件格式, 每行一个 |
| `web/src/locales/{en,zh_hans,zh_hant}.json` | 新增 `cliHint*` 文案(三语同键; 英文不得含 CJK) |

文案 key 草案:
`cliHintTitle` / `cliHintStep1` / `cliHintStep2` / `cliHintStep3` /
`cliHintCopy` / `cliHintCopied` / `cliHintSecretNote` /
`cliHintCodexCmd` / `cliHintCodexPath` / `cliHintClaudeCmd` / `cliHintClaudePath`

### 5.2 后端

**零改动。** 这是 A 相对 B 的最大好处: 不新增端点、不改协议、不碰加密。

### 5.3 测试

| 类型 | 内容 |
|---|---|
| 单测(vitest) | 平台切换时提示显隐正确; kiro/gemini/grok **不显示**提示 |
| 单测 | 复制按钮写入剪贴板(可断言 clipboard mock) |
| 单测 | 三语文案 key 齐全(防止漏键在界面上印原始 key —— 本项目踩过这个坑) |
| E2E | 打开导入框 → 选 claude → 提示可见且路径含 `.credentials.json`; 选 gemini → 提示不可见 |

后端已支持的两种形状(`tokens` 封装、`claudeAiOauth`)**已有测试**, 不需新增。

## 6. 验收标准

1. 选 claude 时界面显示 `~/.claude/.credentials.json` 与获取步骤;
2. 选 codex 时显示 `codex login` 与 `~/.codex/auth.json`;
3. 选 kiro / gemini / grok 时**不显示**该提示;
4. 三语 key 齐全, 界面上不出现原始 key;
5. `tsc` 干净, vitest 全绿, E2E 通过。

## 7. 明确不做

- 不在服务端发起 OAuth(那是 B, 未验证可行性);
- 不给 Kiro 编命令(无官方 CLI);
- 不给 Grok 编命令(未核实来源);
- 不扩 Gemini 的 OAuth 解析(见 §4, 建议独立立项);
- 不承诺"点一下就登录" —— A 的语义是"引导你去本机登录"。

## 8. 已知局限(必须写在文档里, 不能只写给我们自己看)

- 用户仍需要**自己**装 CLI、自己跑登录 —— 这是 A 的本质, 不是缺陷。
- Windows 路径(`%USERPROFILE%\.codex\auth.json`)是按约定推得的: 二进制里确实用 `USERPROFILE`
  作为家目录变量(证据: inside the sandbox %USERPROFILE% is that account own home),
  但**未在 Windows 上实测**取到文件。
- Claude Code 的路径以 2.1.325 版二进制为准; 未来版本若改路径, 本文档与界面需同步更新。
  (建议在提示文案里同时给出"若路径不存在, 请查看该 CLI 文档"的兜底句。)
