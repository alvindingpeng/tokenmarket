# 上游账号 JSON 格式调研

> 目的: 为「导入账号 JSON 实现 OAuth 接入 + 定时验活 + 轮巡」提供格式依据。
>
> **证据等级说明**: 本文所有字段与端点均标注来源。标 [实测] 的是本机仓库里**真实运行的代码**
> (new-api 的 Codex 实现、regplatformm 的注册/验活代码); 标 [推断] 的是按同族机制推演、未在本地
> 代码中找到直接证据的。本次联网检索不可用(web_search 无 API key), 因此**没有引用任何线上文档**。

## 0. 结论速览

| 平台 | 凭据形态 | 能否 refresh | 接入 Octopus 的成本 |
|---|---|---|---|
| ChatGPT (Codex) | OAuth 凭据包 JSON | **是**, 标准 token 端点 | **低**: 整包存进 `ChannelKey.Key` |
| Kiro (AWS) | OIDC 三元组 clientId/clientSecret/refreshToken | **是**, AWS OIDC | **低**: 同上 |
| Gemini (Business) | **Cookie 会话**, 不是 OAuth | **否**, 无 refresh 概念 | **中**: 需 Cookie 注入, 非标准鉴权头 |
| Grok (xAI) | **SSO Cookie** | **否**, 无 refresh 概念 | **中**: 同 Cookie 路径 |
| Claude Code | OAuth 凭据包 JSON | 是(同族机制) | 低(与 Codex 同构) |

**最重要的结论**: 坊间统称的"OAuth 账号 JSON"实际是**两类完全不同的东西** —— 真 OAuth 刷新链
(Codex/Kiro/Claude) 和 **浏览器会话 Cookie**(Gemini/Grok)。后者根本没有 refresh_token,
所以"定时验证有效性"对它们只能是"带 Cookie 打一个轻请求看是否 200"。这个区分直接决定实现路径。

## 1. ChatGPT / Codex —— 证据最完整

[实测] 来源: `new-api/service/codex_oauth.go` + `regplatformm/internal/worker/openai_worker.go`

### 1.1 端点与授权参数

| 项 | 值 | 来源 |
|---|---|---|
| token 端点 | `https://auth.openai.com/oauth/token` | [实测] codex_oauth.go |
| client_id | `app_EMoamEEZ73f0CkXaXp7hrann` | [实测] 两处一致 |
| grant_type | `refresh_token` | [实测] |
| 请求格式 | `application/x-www-form-urlencoded` | [实测] |
| 响应字段 | `access_token` / `refresh_token` / `expires_in`(秒) | [实测] |

**关键**: 刷新会**同时返回新的 refresh_token**, 旧值随即失效 [实测: 代码里回写的是响应中的新值]。
这是整个方案最容易出错的地方 —— 回写失败等于账号永久失效。

**JWT claims**: account_id 在嵌套 claim `https://api.openai.com/auth` 下的
`chatgpt_account_id`; email 在顶层 `email` [实测]。到期时间从 JWT 的 `exp` 解出。

### 1.2 标准 JSON 形状

以下为 new-api 侧解析用的结构 [实测: `CodexOAuthKey` 结构体]:

```json
{
  "id_token":      "<JWT>",
  "access_token":  "<JWT>",
  "refresh_token": "<opaque>",
  "account_id":    "<chatgpt_account_id>",
  "email":         "<user@example.com>",
  "type":          "codex",
  "expired":       "2026-11-14T05:30:00Z",
  "last_refresh":  "2026-11-13T22:00:00Z"
}
```

而 `~/.codex/auth.json` (CLI 本地文件) 的形状可能不同, 多了外层封装 [**部分推断**]:
`OPENAI_API_KEY` 键的存在有间接证据 —— 本项目 README 里就有「在 `~/.codex/auth.json` 里把
`OPENAI_API_KEY` 留空」的说明; 但 `tokens` 嵌套层本身在本地仓库中**没有实证**, 落地前需用真实样本确认。

```json
{
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "<JWT>",
    "access_token": "<JWT>",
    "refresh_token": "<opaque>",
    "account_id": "<chatgpt_account_id>"
  },
  "last_refresh": "2026-11-13T22:00:00Z"
}
```

> ⚠️ 这里的分歧必须用真实样本确认, 不能靠推测写解析器。稳妥做法是**两种都吃**:
> 存在 `tokens` 键则解包, 否则按平铺读 —— 即使嵌套层判断错了也不会失败。这是导入功能第一个会踩的坑。

## 2. Kiro (AWS) —— OIDC 三元组

[实测] 来源: `regplatformm/internal/worker/kiro/kiro_validate.go`

| 项 | 值 |
|---|---|
| token 端点 | `https://oidc.us-east-1.amazonaws.com/token` |
| 请求体 | `{clientId, clientSecret, refreshToken, grantType:"refresh_token"}` JSON |
| 响应字段 | `accessToken` (**camelCase**, 不是 snake_case) |
| 验活端点 | `https://q.us-east-1.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST` |
| 失效判定 | 响应体含 `invalid_grant` / `InvalidGrantException` → 判定 expired |

### 2.1 JSON 形状

```json
{
  "clientId":     "<uuid>",
  "clientSecret": "<secret>",
  "refreshToken": "<opaque>",
  "region":       "us-east-1"
}
```

> **注意大小写**: Kiro 用 camelCase(`refreshToken`), Codex 用 snake_case(`refresh_token`)。
> 混用会让解析静默失败并把账号误判成"无 refresh token"。导入器需要按平台分别解析, 不能共用一个 struct。

## 3. Gemini Business —— Cookie 会话, 不是 OAuth

[实测] 来源: `regplatformm/internal/handler/gemini_validate.go`

字段: `c_ses` / `c_oses` / `csesidx` / `config_id` [实测]

验活方式是**注入 Cookie 后打一个页面请求**:
- Cookie `C_SES` 域 `.gemini.google`; `C_OSES` 可选 [实测]
- 请求 `https://gemini.google.com/`?csesidx=... [实测]

```json
{
  "c_ses":     "<cookie value>",
  "c_oses":    "<cookie value>",
  "csesidx":   "<id>",
  "config_id": "<id>"
}
```

> **没有 refresh_token**。所以"定时验活"只能是重放 Cookie 看响应; 而 Cookie 会在上游会话轮换时
> 静默失效, 失效后必须人工重导 —— 不存在自动续期能力。

## 4. Grok (xAI) —— SSO Cookie

[实测] 来源: `regplatformm/internal/worker/grok_worker.go`

注册产物是一个 `sso=` Cookie [实测: 代码从 Set-Cookie 里抓 `sso=`, 并排除 `sso-rw=`]。

```json
{
  "sso":   "<cookie value>",
  "email": "<user@example.com>"
}
```

同 Gemini: **无 refresh 链**, 只能重放验活。

## 5. Claude Code

[推断] 与 Codex 同族(标准 OAuth refresh_token grant), 凭据包内同样含
`access_token` / `refresh_token` / `expires_in`。
本地仓库中未找到可直接引用的实现, **端点和 client_id 未实证**, 落地前需单独确认。

## 6. 对 Octopus 实现的三条直接影响

1. **必须按平台解析, 不能共用一个 struct** —— 字段命名就分三派:
   snake_case(Codex) / camelCase(Kiro) / Cookie 键(Gemini、Grok)。
2. **refresh 链与非 refresh 链是两套实现**: 前者的定时任务是"刷新", 后者的定时任务是"重放验活"。
   把它们塞进同一个抽象会在"刷新失败该怎么办"上分叉(前者可重试, 后者只能标记失效并等人工)。
3. **存储可以统一**: `ChannelKey.Key` 已是信封加密字符串, 整包 JSON 直接放进去即可,
   只需额外记 `ExpiresAt` 与 `Kind`(oauth / cookie) 两类元数据。

## 7. 解析原型实测(可运行的验证)

光读别人代码不够: 真实导入的样本常带外层封装, 而「两种都吃」这种设计只有真跑一遍才知道会不会翻车。
写了个最小原型(临时目录, 未入库)验证四件事, 结果:

| 场景 | 输入 | 结果 |
|---|---|---|
| 平铺 codex | `access_token/refresh_token/account_id` | 解析成功, refresh=true |
| **带 tokens 层的 CLI 导出** | `{"tokens":{...}}` | **解包后成功** —— 兼容是必要的 |
| 大小写陷阱 | 给 codex 喂 `refreshToken`(camelCase) | 报 `缺少 access_token` |
| cookie 类 | grok `sso` | kind=cookie, refresh=**false** |

第三行值得记一下: 原以为会报「缺少 refresh_token」, 实际先卡在 access_token 的校验顺序上。
这说明**报错信息会误导排查方向** —— 用户贴错平台 JSON 时, 提示应直接说「字段名像 Kiro 的」,
而不是笼统说缺 access_token, 否则会让人以为账号本身坏了。

## 8. 未解决项(需在落地前确认)

- Claude Code 的确切端点与 client_id: 本地无实证, 联网检索不可用。
- 上游对"同一账号多 IP/高并发"的判定阈值: 无公开数据, 只能靠实测踩。
- Cookie 类凭据的稳定期: 无数据, 需上线后统计失效间隔来定验活频率。

## 9. 实现落地记录(Codex 已接入)

上面第 0-8 节是调研; 以下是**已上线代码**的对应实现与本机实测结果, 供接入其余平台时对照。

### 9.1 代码落点

| 职责 | 位置 | 说明 |
|---|---|---|
| 账号 JSON 解析 | `internal/model/account_json.go` | 平铺与 CLI `tokens` 封装两种形状都吃; 各平台各自字段名, 不做跨平台归一化 |
| 令牌端点规格 | `internal/model/account_oauth.go` | 端点/client_id/请求体形态/响应字段名集中成表, 刷新逻辑只写一份 |
| 刷新执行 | `internal/op/account_refresh.go` | 按凭据串行, 读旧值-刷新-回写在同一把锁内 |
| 刷新调度 | `internal/op/account_refresh_run.go` + `task.Define` | 有界批次, 区分「可重试」与「账号已失效」 |
| 导入 | `internal/op/account_import.go` | 先解析后落库; 落库后写缓存, 否则进程内不可见 |
| 转发取令牌 | `internal/relay/token_provider.go` | 从账号 JSON 取 access_token, 不做任何 IO |
| 接口 | `internal/server/handlers/channel_account.go` | `POST /api/v1/channel/account/import`, `POST /api/v1/channel/account/:id/refresh` |

### 9.2 三处由架构约束决定的取舍

1. **刷新绝不在转发路径上做**。`auth.APIKeyProvider.Get(ctx) string` 是同步且**不返回 error**
   的接口, 而刷新是网络调用。塞进去等于给每个请求挂最长 20 秒等待, 且失败无处上报。
   因此刷新只在定时任务里发生, 转发只读缓存。

2. **整包账号 JSON 复用 `ChannelKey.Key` 列, 不新开表**。Key 列本就是「这把凭据的全部秘密」,
   已走信封加密、绑定 `(channel_id, name)`、运行时读明文。账号 JSON 是它的另一种形态。
   实测确认落库为密文(`sysec$1$...`), 令牌没有明文落库。

3. **`refresh_token` 轮换必须原子回写**。上游每次刷新都会换新令牌, 旧值当场失效。回写用了
   `map[string]any` 的 `Updates`, 绕过序列化器, 但**必须带 `Model` 身份**否则密文 codec 会
   fail closed 拒写。写入失败单独成 `AccountRefreshPersistError` —— 这是「账号即将永久失效」
   的信号, 不能被当成普通重试失败吞掉。

### 9.3 本机实测结果(2026-11-14)

用隔离实例(空库 + 自带 KEK)跑真实 HTTP 闭环, 结果:

| 用例 | 结果 |
|---|---|
| 非法 JSON | 400 `账号 JSON 必须是 JSON 对象` |
| 缺 refresh_token | 400 `codex: 缺少 refresh_token, 该账号无法自动续期` |
| 合法账号导入 | 200, 落库 `account_kind=oauth`、`token_expires_at` 由 JWT `exp` 解析 |
| 首刷失败 | 200 + `warning`, **账号仍落库**(部分成功语义) |
| 落库形态 | `key` 列为信封密文, 令牌无明文 |
| 详情接口 | 账号凭据与静态凭据同时返回, Key 已掩码 |

**最重要的实测发现: 本机所在地区被 OpenAI 拒绝。** 真实刷新打到 `auth.openai.com` 返回:

```
403 {"error":{"code":"unsupported_country_region_territory",
     "message":"Country, region, or territory not supported"}}
```

这不是代码缺陷 —— 但它意味着**在这台机器上无法验证 Codex 刷新的成功路径**。
现网部署该功能前, 必须在一台 OpenAI 支持地区的出口上再验一次「刷新成功 + 轮换回写」。
这也是测试用本地假令牌端点而非真实上游的直接原因。

### 9.4 接入其余平台的注意事项

- **Kiro**: 字段是 camelCase(`refreshToken`/`clientId`/`clientSecret`), 与 Codex 的 snake_case
  不同。解析层**刻意不做跨平台归一化** —— 用错平台的字段名会明确报错, 而不是静默读到空值。
  实测中解析层一度漏读 `accessToken`, 后果是转发拿到空 Bearer 全部 401 且无任何报错,
  已修并加测试钉住。
- **Gemini / Grok**: 是 Cookie 不是 OAuth。`AccountKind` 标成 `cookie`, 定时任务明确不碰它们。
  失效后**无法自动恢复**, 只能人工重导 —— 界面必须说明, 否则使用者以为系统会一直维持。
  「定时验证有效性」对它们只能是带 Cookie 打轻请求看状态码。
- **Claude Code**: 与 Codex 同构, 但**端点与 client_id 仍未核实**(见第 8 节遗留问题),
  接入前必须先确认。

### 9.5 仍然遗留的验证缺口

1. Codex 刷新**成功路径**未在本机验证(地区限制), 只验证了失败路径与落库/缓存链路。
2. 真实 ChatGPT 账号的端到端转发未验证(同一地区限制)。
3. 前端导入对话框只做了 `tsc` + 单元测试, 未做浏览器手工走查。
