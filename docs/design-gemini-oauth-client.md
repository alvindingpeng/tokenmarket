# Gemini 网页登录: 内置客户端与覆盖配置

## 结论先行

Gemini 的网页登录**不需要部署者先配环境变量就能用**。做法是内置一对默认 OAuth 客户端凭据,
同时允许在设置页覆盖成自己注册的客户端。

之所以此前要求配环境变量, 是因为当时(功能首次落地时)把"不冒用第三方应用身份"当成硬约束。
本轮用户明确选择"两者都要", 即接受内置默认值带来的风险换取开箱可用。本文记录这个判断和依据。

## 实测事实(不是推测, 2026-11-15)

### Google token 端点强制 client_secret

    POST https://oauth2.googleapis.com/token
    grant_type=refresh_token & client_id=<id> & refresh_token=<fake>
      -> 400 invalid_request  "client_secret is missing."

    grant_type=refresh_token & client_id=<id> & client_secret=<sec> & refresh_token=<fake>
      -> 400 invalid_grant  "Bad Request"

第二条是**对照组**: 得到 invalid_grant 而不是 invalid_client, 说明 client_id/secret 这一对本身有效,
只差真实的 refresh_token。这两条合起来证明: Gemini 是机密客户端, 换 token 必须有 secret。

这与 Claude / Codex 有本质差别 —— 后两者是 PKCE 公有客户端, secret 根本不需要,
所以它们能仅凭 client_id 内置使用。

### device flow 不可用

    POST https://oauth2.googleapis.com/device/code, client_id=<GeminiCLI id>
      -> 401 invalid_client  "Invalid client type."

即无法像 GitHub Copilot 那样用设备码流程完全绕开回调。

### redirect_uri 白名单的校验时机

    GET https://accounts.google.com/o/oauth2/v2/auth?...&redirect_uri=<任意值>
      -> 302 到 /v3/signin/identifier (先让你登录)

白名单校验发生在**登录之后**而不是打开授权页时。这一点对自建客户端的用户很重要:
如果没在 Google Cloud Console 里把自己的回调地址加进白名单, 他会在输完密码之后
才看到 `redirect_uri_mismatch`, 而不是在点按钮时就知道。界面必须提前说明这一点,
否则这会像"功能坏了"而不是"少配了一项"。

### 内置凭据的来源

取自公开 npm 包 `@google/gemini-cli@0.68.0`(当周下载量约 45 万):

    client_id     681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com
    client_secret 该包内唯一一个 GOCSPX- 字面量(位于 package/bundle/chunk-*.js)

注意包内还有**两个** apps.googleusercontent.com 形态的 client:
`681255809395-...` 是 Gemini CLI 自己的, `764086051850-...` 是 google-auth-library 自带的
`CLOUD_SDK_CLIENT_ID`。两者形态几乎一样, 用错会得到 `invalid_client`。
这一步此前在 G1 阶段已用断言钉住。

## 风险(已向用户说明并获同意)

这对凭据本身**不是泄露的机密** —— 它在公开 npm 包里, 任何人都能提取。
真正的风险不在"保密", 而在于:

1. **所有部署共用同一个 Gemini CLI 身份。** Google 的配额、限流、风控都作用在这一个 client 上。
   用得多了可能被限流, 极端情况下该 client 被停用会让所有部署的 gemini 登录同时失效。
2. **以第三方应用的身份使用。** 授权页会显示 "Gemini CLI 正在申请访问权限",
   而实际使用者是我们。这不是技术问题而是使用条款层面的问题。
3. 风险由**部署者**承担(账号被风控的损失落在使用它的人身上)。

因此设置页必须支持覆盖, 并且说明文字要把上面几点讲明白 —— 让部署者知道自己在用什么,
而不是"默认就能用所以不用想"。

## 取值优先级

    设置库(数据库)  >  环境变量  >  内置默认

- 设置库优先, 让界面上的修改立即生效, 不必进机器改 systemd drop-in。
- 环境变量作为中间层保留, 兼容既有部署(此前文档就是让人配环境变量的, 不能让他们配了反而不生效)。
- 都没有才用内置默认。

环境变量名保持 `SWITCHYARD_GEMINI_OAUTH_CLIENT_ID` / `SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET` 不变。

## 与既有设计文档的关系

`docs/design-cli-account-import-guidance.md` 里写的是"Gemini 需部署自行注册 OAuth 客户端"。那句话
在本轮之后**不再准确** —— 现在是内置默认可用、覆盖可选。读那份文档时应以本文为准。
