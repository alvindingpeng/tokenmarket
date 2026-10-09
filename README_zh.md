<div align="center">

<img src="web/public/logo.svg" alt="Octopus Logo" width="120" height="120">

### Octopus

**自托管的 LLM API 网关：聚合、路由、计费与运维，全部装进一个二进制**

`v0.15.0` · [English](README.md) | 简体中文

</div>

> **本仓库** —— [alvindingpeng/tokenmarket](https://github.com/alvindingpeng/tokenmarket) —— 基于上游
> [bestruirui/octopus](https://github.com/bestruirui/octopus)（基线：上游 `v0.13.9`）。下面写的是
> **本分支当前真实运行的能力**；与上游的差异逐条记在 [CHANGELOG.md](CHANGELOG.md) 里。

---

## 目录

- [它做什么](#它做什么)
- [快速开始](#快速开始)
- [协议兼容性](#协议兼容性)
- [转发：选路、故障转移与错误处理](#转发选路故障转移与错误处理)
- [定价与计费](#定价与计费)
- [渠道、分组与发布](#渠道分组与发布)
- [运维：健康探测、告警、指标、备份](#运维健康探测告警指标备份)
- [限额与访问控制](#限额与访问控制)
- [角色与管理界面](#角色与管理界面)
- [站点身份与维护模式](#站点身份与维护模式)
- [配置](#配置)
- [接口总览](#接口总览)
- [客户端接入示例](#客户端接入示例)
- [界面截图](#界面截图)
- [文档](#文档)
- [从源码构建](#从源码构建)
- [测试与 CI](#测试与-ci)
- [版本号与发布](#版本号与发布)
- [路线图](#路线图)
- [致谢](#致谢)

---

## 它做什么

Octopus 在多家上游供应商前面摆一个 OpenAI 形态的入口。客户端照旧调 `/v1/chat/completions`，Octopus 负责
挑渠道、必要时转换协议、预扣余额、按实际用量结算，并在上游出问题时切走。

| 领域 | 能力 |
|------|------|
| 🔀 **多渠道聚合** | 一个模型名背后挂多个渠道，各自独立凭据与限额 |
| 🔄 **协议互转** | OpenAI Chat ⇄ OpenAI Responses ⇄ Anthropic Messages，入站出站都转 |
| 🎨 **生图** | `/v1/images/generations` 与 `/v1/images/edits` 完整转发，按张计费 |
| 🧲 **选路** | 7 种模式：手动、按序故障转移、低价优先、延迟优先、成功率优先、综合评分、随机 |
| 🛡️ **故障转移** | 单成员重试、冷却、全忙时有界等待队列 |
| 💰 **计费** | 调用前预扣、调用后结算、每日对账、价格快照 |
| 📊 **可观测** | 实时请求链路、消费明细、审计记录、`/healthz`、Prometheus `/metrics`、运维中心 |
| 🔐 **多用户** | 三种角色（管理员 / 渠道商 / 用户）、每人独立 API 密钥、注册可选审批 |
| 🏷️ **白标** | 站点名称、描述、联系方式、公告横幅、维护模式 |
| 📦 **部署** | 单个静态二进制，前端内嵌，支持 SQLite / MySQL / PostgreSQL |

**明确没有的：** 内置支付网关（余额由管理员充值），以及内置币种换算（价格按模型存储，扣减一个数值余额）。

---

## 快速开始

### 取得二进制文件

**本分支不发布二进制文件。** 这里的 Release 只有更新说明 —— 上游那条交叉编译并上传压缩包的 workflow
绑定的是上游的 `master` 分支和上游的镜像仓库，所以本仓库的 Release 上不会有 `octopus-*.zip`（上游的
Release 页有，但那份代码落后于本分支基线，且不含本分支新增的功能）。二进制请自行构建：

```bash
bash scripts/build-local.sh -o ./octopus    # 只构建当前机器这一份
# 或者要全平台矩阵（linux amd64/arm64/armv7/386、windows amd64、darwin amd64/arm64、android）：
# bash scripts/build.sh  ->  build/archives/octopus-<os>-<arch>.zip
```

然后：

```bash
./octopus start
```

数据都在二进制旁边的 `./data/`：首次启动写出 `data/config.json`，默认 SQLite 库是 `data/data.db`。

### Docker

本分支**不发布容器镜像**。要容器，本地自己构建：

```bash
bash scripts/build.sh                       # 交叉编译到 build/
docker build -f scripts/dockerfile/Dockerfile -t octopus .
docker run -d --name octopus -v /path/to/data:/app/data -p 8080:8080 octopus
```

仓库里带的 `docker-compose.yml` 中，`image:` 仍指向上游镜像名（`bestruirui/octopus`）。用之前先把它换成
你刚构建出来的 tag，否则跑的是上游那份没有本分支功能的代码。

### 首次登录

打开 `http://localhost:8080`，用初始账号登录：

- **用户名**：`admin`
- **密码**：`admin`

> ⚠️ **首次登录后立刻改密码**，尤其是在这个端口可能被 `127.0.0.1` 之外访问之前。注册默认关闭，所以在打开
> 之前 `admin` 是唯一账号。

---

## 协议兼容性

### 入站（客户端可以调什么）

```
POST /v1/chat/completions     # OpenAI Chat Completions
POST /v1/responses            # OpenAI Responses
POST /v1/messages             # Anthropic Messages
POST /v1/images/generations   # OpenAI 生图
POST /v1/images/edits         # OpenAI 生图（multipart，支持多张输入图）
GET  /v1/models               # 该密钥可见的模型列表
```

鉴权用界面「API 密钥」里创建的密钥，放在 `Authorization: Bearer sk-octopus-…` 或 `x-api-key: sk-octopus-…`。
`/v1` 这一层**只认密钥**：界面的会话 Cookie 不是 API 凭据，在这里会被拒绝。已停用、已过期、或已用尽
`max_cost` 配额的密钥，都在入口处直接拒绝。

### 出站（渠道怎么寻址）

渠道 = **协议** + 基础地址。接口路径由程序自己拼接，所以基础地址**不要**包含 `/v1` 或具体接口路径：

| 渠道协议 | 自动拼接的路径 | 基础地址示例 |
|----------|----------------|--------------|
| OpenAI Chat | `/v1/chat/completions` | `https://api.openai.com` |
| OpenAI Responses | `/v1/responses` | `https://api.openai.com` |
| Anthropic Messages | `/v1/messages` | `https://api.anthropic.com` |
| OpenAI 生图 | `/v1/images/generations`、`/v1/images/edits` | `https://api.openai.com` |

> 💡 **Gemini 与其他供应商。** 上游在 `v0.13.9` 移除了 Gemini 原生协议：原本 `gemini` 类型的渠道会迁移成
> OpenAI Chat 协议。这类渠道请改填该供应商的 OpenAI 兼容端点。

### 方言（dialect）

同一协议下的不同供应商仍可能在请求体 / 响应体上有差异（例如思考内容放在 `reasoning_content` 还是 `reasoning`）。
这类差异由渠道的**方言**表达，而不是靠改地址绕。目前只定义了 `generic`（标准协议、不做厂商特化）；这个机制
存在的意义是：以后新增厂商特化是改代码，不是改表结构。

---

## 转发：选路、故障转移与错误处理

**分组**就是客户端看到的模型名，里面是排好序的成员，每个成员对应一个渠道。分组的**模式**决定谁服务这次请求：

| 模式 | 行为 |
|------|------|
| `manual` | 只用你勾选的成员，按你定的顺序，不做动态选择。 |
| `failover` | 按成员顺序走，失败切下一个。 |
| `price` | 用户价最低者优先，失败时按价格递增切换。 |
| `latency` | 首响应耗时滑动平均最快者优先。 |
| `success` | 滑动窗口成功率最高者优先。 |
| `score` | 价格、延迟、成功率归一化后加权评分取最高。默认权重 **40 / 30 / 30**，可按分组、也可按系统设置。 |
| `random` | 在可用成员间均匀分发。 |

`price` 与 `score` 要比较价格，可以选比较口径：`blended`（输入输出均值）、`input`、`output` —— 读取便宜但
生成很贵的模型，不该在错误的维度上胜出。

### 分组级转发参数

成员启动超时、成员重试次数与间隔、单成员流式空闲超时、非流式响应超时、整请求最大耗时（含重试与流式传输）、
无可用成员时最大等待秒数、同分组最大排队数、单请求跨成员最大尝试轮次。后端对每个值都做钳制（超时 `1–3600` 秒，
`0` 表示关闭排队或等待），于是手抖打错一个数字不会造出无限队列或永不放弃的请求。

### 故障转移的卫生条件

- 失败的成员进入**冷却**，而不是被继续猛砸；冷却与成员级指标会持久化、重启后恢复 —— 冷启动的进程不会去
  踩踏刚刚把它打撇的那家上游。
- 所有成员都忙或都在冷却时，请求进入**有界等待队列**；超过 `max_wait_seconds` 就直接拒绝，而不是吊着。
- **上游错误被吸收，而不是盲目透出**：要么换个成员成功，要么给客户端一个能解析的合成错误。Agent 的调用循环
  不会因为某家供应商一个 5xx 就断掉。

### 实时链路

日志页通过 SSE（`/api/v1/log/overview/stream`）推送进行中的请求，并给出每次请求的各成员尝试、原始请求 /
响应正文，以及对进行中流的停止操作。链路从客户端发出请求那一刻就开始记录，而不是等上游响应。

---

## 定价与计费

### 价格从哪来

1. **models.dev** —— 按周期同步（`model_info_update_interval`，默认 24 小时）进一张可重建的底表；
   `/api/v1/model/rebuild-price` 可整表重算。
2. **价格管理页** —— 你自己覆写的价格，优先级高于同步值。
   凡是被渠道用到但 models.dev 里没有的模型，系统会自动建一行价格，于是总有个地方给你填价，而不会存在
   一个悄悄不计费的模型。
3. **渠道成本** —— 你付给供应商的钱，在**发布管理（模型上架与定价）**里维护：按模型、按凭据填供货价，
   用户价按 `供货价 × (1 + markup_ratio)` 推导，默认上浮 `0.2`。零价上架会被拦下。

两类维度分别计价：**token**（输入 / 输出 / 缓存读 / 缓存写）与**媒体**（按张；按秒与分档已为后续协议建模）。

### 一次请求怎么计费

```
预扣 → 调用上游 → 结算 → 释放
```

- **预扣**在请求发出前按估算冻结余额：没余额就没有调用。估算用默认输出 token 上限
  （`balance_reserve_output_cap`，4096）与保守张数（`balance_reserve_images`，默认 4，校验 1–64，用于调用方
  没传 `n` 的情况）。`min_balance` 设定拒绝调用的余额下限。
- **结算**用上游响应里的实际用量替换估算。用量**取自上游，绝不用本地分词器反推** —— 计费必须与供应商向你
  收的钱一致，否则每一份对账报告都是编的。上游确实没给出可用数字时，会按兜底估算记账并标明这一点。
- **释放**由 10 分钟一轮的任务执行：预扣超过 30 分钟仍未结算就视为泄漏并解冻。请求崩了不能让钱永远冻着。

### 要证据，不要侥幸

- 每次请求都把**价格快照**与账单一起写下去。改价绝不改写历史；一次改价之前开出的账单必须仍可解释。
- **每日对账**任务从日志重算昨日账目，记录一致或差异。它**只报告**，从不自动调整余额。
- **消费明细**（按模型、渠道、状态筛选，分页，CSV 导出）与**余额流水**（预扣 / 结算 / 释放 / 调账）对
  用户和管理员都可见。
- 管理员对钱、模型、用户做的每件事都进**审计记录**；审计导出本身也被审计（`audit.export` 记下导出多少行 /
  共多少行，以及当时生效的筛选条件）。

---

## 渠道、分组与发布

### 渠道

一个渠道就是一路上游：名称、协议、一个或多个**命名凭据**、可选代理、模型授权、按凭据的成本。

- **自动添加模型**（`model_auto_add`，默认**关**）。开启后保存渠道会探测上游 `/models` 并并入。让它可以放心
  长期打开的是这几条规则：
  - **只增不删** —— 上游不再返回的模型本地保留，不会被清掉；
  - **授权按位 OR 并入** —— 探测只会加协议位，不会清掉你手工设过的授权；
  - **单次上限 200 个新模型**，超出部分截断并写进审计 detail；
  - **留痕**为 `channel.model-added`，附新增模型名。
  探测与手动刷新共用同一份实现，所以「预览看到的」和「保存得到的」永远是同一件事；探测失败是
  best-effort：只记日志，绝不把已经保存成功的操作拖成失败。
- **发布**：渠道可以用**共享编码**而不是真实名称发布到用户侧，渠道商和用户都看不见究竟是哪家的哪个凭据在服务。
- **代理**可全局配置（`proxy_url`）也可按渠道配置，模型探测同样走这份代理设置。
- 渠道级统计：请求数、token、成本、成功 / 失败，以及按日序列。

### 模型能力

自动添加时，生图类模型按名称关键词归类（dall-e、flux 等），授权变化时重新推导。这是刻意做的近似 ——
真正的能力探测在路线图里。

### 限流

七个维度，越具体越优先：`system` → `user` → `api_key` → `group` → `channel` → `channel_key` →
`channel_model`。每条策略含 **RPM**（请求 / 分钟）、**TPM**（token / 分钟）与并发上限，`0` 为不限。策略旁边
直接显示实时用量，于是你能看见某个密钥离天花板还有多远，而不是靠 429 反推。
---

## 运维：健康探测、告警、指标、备份

### 探针

```
GET /healthz    # 永远不鉴权 —— 存活状态：status、db、version、active_requests、uptime
GET /metrics    # Prometheus 文本格式；可选 bearer 鉴权
```

数据库不响应时 `/healthz` 返回 `503 degraded`。它刻意永不加鉴权：给存活探针加鉴权只会让编排系统没法探活。

`/metrics` 暴露 `octopus_up`、`octopus_build_info`、`octopus_relay_requests`、`octopus_relay_tokens_total`、
`octopus_billing_cost_total`、`octopus_active_requests`、`octopus_channels`、`octopus_users`、
`octopus_user_balance_total`、`octopus_alerts_raised_24h`，以及 Go 运行时计数。把 `metrics_auth` 设为 `bearer`
即要求抓取方带令牌（`Authorization: Bearer …` 或 `?token=`）；令牌按需生成，并可在界面里轮换。

### 渠道健康探测

默认关闭 —— 主动探测会在上游产生真金白银的费用。开启后按间隔探测：延迟超过 `health_latency_ms` 判
`degraded`；并且只有连续失败达到 `alert_fail_streak` 次才判 `down`（单次抖动不吵人）；恢复时补发 `recovered`。

### 告警

- 类型包括渠道故障、对账差异、结算失败、备份失败、归档失败，以及**低余额**（`low_balance`：结算时按
  `min_balance + estimate` 预判、提前预警，预扣被拒时兜底补报 —— 两个触发点、同一个 reason，会被去重合并）。
- 按渠道 / 类型 / 原因在窗口内去重（`alert_dedup_minutes`，默认 10）。
- 送达站内告警流；若设了 `alert_webhook_url` 则同时推到你的 webhook。界面上有测试按钮，不必等真出事才验证地址。
- 阈值与权重在界面里可改，`/api/v1/ops/alerts/rules` 回显**生效中**的规则（含默认值）—— 界面显示的就是在跑的。

### 备份

- 定时一致性备份（`auto_backup_interval`，默认 24 小时），保留 `auto_backup_keep` 份（默认 7 份），每份写完即校验。
- 可选 **AES-256-GCM** 加密（`backup_encrypt`）与 **WebDAV** 异地推送（`backup_remote`），每个备份旁边留一份
  `.sha256` 校验和。
- 口令与异地凭据只存在 `config.json` / 环境变量里，**绝不入库** —— 因为库正是被备份出去的东西。
- 恢复与校验走命令行：

```bash
./octopus backup verify  --file data/backup/db-20261009.db.sha256
./octopus backup decrypt --in backup.db.enc --out backup.db
./octopus version
```

### 日志生命周期

调用日志、尝试记录与正文每小时先归档再清理，超过 `log_retention_days`（默认 7 天，归档开关
`log_archive_enabled`）。请求 / 响应正文默认保存（`log_store_body`），可用 `log_mask_fields`（逗号分隔的
JSON key）做脱敏 —— 公网机器上保存正文之前请先配好掩码，因为正文正是密钥最容易待的地方。

### 运维中心

`/api/v1/ops/overview` 给出 24 小时请求数、成功率、token、费用、进行中请求、渠道启用 / 总数、以及 24 小时
告警数；界面上还能看备份、对账、日志生命周期、健康探测与告警历史，每项都能手工「立即执行」。

---

## 限额与访问控制

| 关注点 | 机制 |
|--------|------|
| 钱 | 调用前余额预扣、`min_balance` 下限、每密钥 `max_cost` 配额与 `expire_at` 过期、每密钥可访问分组的白名单 |
| 量 | 各维度的 RPM / TPM / 并发，最具体的策略胜出 |
| 过载 | 有界等待队列、整请求截止时间、单成员尝试上限、冷却 |
| 来源 | `cors_allow_origins`（空 = 不允许跨域；`*` = 全允许） |
| 管理后台 | 会话 Cookie 鉴权、逐路由角色校验；除 `/api/v1/site/config` 与登录 / 注册接口外 `/api/v1/*` 不公开 |
| 机密 | API 密钥形如 `sk-octopus-…`；`auth_secret` 与 `metrics_token` 是内部键，不会出现在设置接口里；日志正文可脱敏；备份凭据不入库 |
| 模型面 | `model_filter` 在渠道列模型时应用全局过滤表达式 |

---

## 角色与管理界面

| 角色 | 可见页面 |
|------|----------|
| **用户** | 主页 · 模型配置（分组）· 计费 · API 密钥 · 日志 · 设置 |
| **渠道商** | 加上：渠道 · 发布管理 |
| **管理员** | 加上：用户管理 · 审计记录 · 限流管理 · 运维中心 · 价格管理 · 设置（全部标签） |

前端是 React 19 + TypeScript + Vite + Tailwind CSS v4，配 TanStack Query、Zustand、`use-intl`
（en / zh_hans / zh_hant），可作为 PWA 安装（`web/public/manifest.json` 与 service worker；SW 按注册作用域
隔离缓存键，因此不同子路径下的两个部署实例不会互相抢存储）。请求链路页走 SSE。分组成员支持拖拽排序，
分组编辑支持置顶 / 置底。

**子路径部署**：前端以 `base: './'` 构建，service worker 从注册作用域推导自己的路径，因此挂在反向代理的
`https://example.com/octopus/` 下面也能跑，不需要为路径重新构建。

### 注册

两条注册通路**默认都关**：自助注册用户（`register_user_enabled`）与注册渠道商（`register_reseller_enabled`）。
打开之后，`register_approval_required`（默认**开**）会让新账号进入待审批状态，由管理员批准 —— 登录页从
`/api/v1/user/register-config` 读这个开关，于是它告诉注册者的是真话：现在到底能不能登录。

---

## 站点身份与维护模式

「设置 → 系统信息配置」里的 7 个设置项由**同一个**端点下发：`GET /api/v1/site/config`，无需登录 ——
未登录的访客也看得见自己正在访问的是谁：

| 字段 | 出现位置 |
|------|----------|
| `site_name` | 浏览器标签页标题、登录页抬头、API 密钥视图抬头、分享图抬头。留空回退 `Octopus`，不会把标题清成空串。 |
| `site_description` | 登录页标题下方一句话 |
| `site_contact` | 登录页；是 URL 或邮箱时自动渲染成可点链接，否则原样显示纯文本 |
| `announcement` + `site_announcement_enabled` | 登录用户可见、可关闭的横幅；开关在**服务端**生效，关闭时公告正文根本不会出现在响应里 |
| `maintenance_mode` + `maintenance_notice` | 含登录页在内各处常驻、不可关闭的红色横幅；非管理员登录被 `503` 拒绝，并带上同一句话 |

保存任意设置后会同时失效站点配置与注册配置缓存，所以界面在同一帧就反映改动，不必刷新。详见
[docs/site-info-distribution.md](docs/site-info-distribution.md)。

> ⚠️ **维护模式不是防火墙。** 它挡的是非管理员的**登录动作**；已持有有效会话的请求照常放行，`/healthz`、
> `/metrics` 与 `/v1` 转发流量都不受影响。用它把人赶出管理后台，不要用它拦流量。

---

## 配置

### `data/config.json`

```json
{
  "server":   { "host": "0.0.0.0", "port": 8080 },
  "database": { "type": "sqlite",  "path": "data/data.db" },
  "log":      { "level": "info" },
  "backup":   { "passphrase": "", "remote_url": "", "remote_user": "", "remote_password": "" }
}
```

首次启动会建好 `data/` 并把默认值写进 `data/config.json`（已存在则不动它）。

| 配置项 | 说明 | 默认 |
|--------|------|------|
| `server.host` | 监听地址 | `0.0.0.0` |
| `server.port` | 服务端口 | `8080` |
| `database.type` | `sqlite` \| `mysql` \| `postgres` | `sqlite` |
| `database.path` | SQLite 文件路径，或 DSN | `data/data.db` |
| `log.level` | `debug` \| `info` \| `warn` \| `error` | `info` |
| `backup.passphrase` | 备份加密口令，空为不加密 | `""` |
| `backup.remote_url` | WebDAV 目录，空为不推送异地 | `""` |
| `backup.remote_user` / `backup.remote_password` | WebDAV 凭据 | `""` |

每一项都可用环境变量覆写，格式是 `OCTOPUS_` + 配置路径（以 `_` 连接）：`OCTOPUS_SERVER_PORT`、
`OCTOPUS_DATABASE_TYPE`、`OCTOPUS_DATABASE_PATH`、`OCTOPUS_LOG_LEVEL`、`OCTOPUS_BACKUP_PASSPHRASE`……
`OCTOPUS_GITHUB_PAT` 用于提高版本检查的 GitHub 速率限制。

### 数据库

| 类型 | `database.path` 格式 |
|------|----------------------|
| SQLite | `data/data.db` |
| MySQL | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `host=localhost user=postgres password=xxx dbname=octopus port=5432 sslmode=disable` |

> 💡 MySQL / PostgreSQL 的库需要你自己建，表结构会自动迁移。迁移脚本是 `internal/db/migrate/` 下按序追加的
> 文件 —— 只追加、不改写历史，这正是「改价不会篡改旧账单」这条约束在升级后仍然成立的原因。

### 运行时设置（不在配置文件里）

三十多项运营设置 —— 统计落库周期、模型信息同步周期、上浮比例、注册开关、预扣上限、评分权重、日志留存、
备份节奏、健康探测参数、告警阈值、指标鉴权、站点身份 —— 都存在数据库的 `setting` 表里，在界面中编辑。它们可以
导出 / 导入 JSON（`/api/v1/setting/export`、`/api/v1/setting/import`），于是调好的一台实例能在另一台复现。

> ⚠️ **退出请用 `Ctrl+C` 或 `SIGTERM`，不要用 `kill -9`。** 统计先在内存里累积，每 `stats_save_interval`
> 分钟（默认 10）批量写库；强杀会丢掉缓冲区里那一部分。

---

## 接口总览

管理接口在 `/api/v1` 下，会话 Cookie 鉴权，逐路由做角色校验：

```
/api/v1/user, /user/manage     登录、注册、注册开关、个人信息、管理员建用户、审批
/api/v1/apikey                 创建、列表、更新、删除、统计、密钥登录
/api/v1/channel                CRUD、启用、统计与按日序列、模型授权、拉取模型、发布、模型列表
/api/v1/group                  CRUD、事件、分组指标
/api/v1/model                  列表、创建、更新、删除、改价、重建价格、上次更新时间
/api/v1/stats                  按日、按小时、总计、按密钥、计费、消费明细与导出、余额流水、收入
/api/v1/log                    列表、导出、清空、SSE 流、尝试记录、请求 / 响应正文、停止
/api/v1/audit                  列表、动作枚举、导出（CSV，有上限且导出本身留痕）
/api/v1/ratelimit              策略 CRUD、检查、实时用量
/api/v1/ops                    概览、对账、备份、日志生命周期、健康、告警、指标令牌
/api/v1/setting                列表、设置、导出、导入（仅管理员）
/api/v1/site                   config（公开）
/api/v1/update                 当前版本、最新版本、执行更新（仅管理员）
```

关于这一层的几点说明：`POST /api/v1/user/update` 同时承担「管理员改别人」和「用户改自己」两类动作 —— 一个
入口、内部按角色校验；`/api/v1/channel/list` 与 `/api/v1/group/list` 是无请求体的 `GET`；内部键 `auth_secret`
与 `metrics_token` 不会出现在 `/api/v1/setting/list` 里。

---

## 客户端接入示例

**分组名称**就是客户端传的模型名。密钥形如 `sk-octopus-<48 位>`，在界面「API 密钥」里创建；
不要把管理员的会话 Cookie 当密钥发出去 —— `/v1` 只认密钥。

### OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key="sk-octopus-REPLACE_ME",   # 在管理界面 > API 密钥 里创建
)
completion = client.chat.completions.create(
    model="octopus-openai",           # 填分组名称
    messages=[{"role": "user", "content": "你好"}],
)
print(completion.choices[0].message.content)
```

同一个密钥同样可以用于 `/v1/responses`。

### Claude Code

编辑 `~/.claude/settings.json`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_AUTH_TOKEN": "sk-octopus-REPLACE_ME",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "ANTHROPIC_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_SMALL_FAST_MODEL": "octopus-haiku-4-5",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "octopus-haiku-4-5"
  }
}
```

### Codex

编辑 `~/.codex/config.toml`：

```toml
model = "gpt-5"
model_reasoning_effort = "xhigh"
model_provider = "octopus"
preferred_auth_method = "apikey"

[model_providers.octopus]
base_url = "http://127.0.0.1:8080/v1"
name = "octopus"
supports_websockets = false
requires_openai_auth = true
wire_api = "responses"
experimental_bearer_token = "sk-octopus-REPLACE_ME"
```

并把 `~/.codex/auth.json` 里的 `OPENAI_API_KEY` 留空，让它使用上面那个 bearer token。

> 💡 上面的 `model` 值是**你自己创建的分组名**，不是供应商的真实模型名。一个叫
> `octopus-sonnet-4-5` 的分组，背后可以挂任何说 Anthropic 或 OpenAI 协议的渠道。

---

## 界面截图

### 桌面端

<div align="center">
<table>
<tr>
<td align="center"><b>主页</b></td>
<td align="center"><b>渠道管理</b></td>
<td align="center"><b>分组管理</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="主页" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="渠道管理" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="分组管理" width="400"></td>
</tr>
<tr>
<td align="center"><b>价格管理</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="价格管理" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="日志" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="设置" width="400"></td>
</tr>
</table>
</div>

### 移动端

<div align="center">
<table>
<tr>
<td align="center"><b>主页</b></td>
<td align="center"><b>渠道</b></td>
<td align="center"><b>分组</b></td>
<td align="center"><b>价格</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="移动端主页" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="移动端渠道" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="移动端分组" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="移动端价格" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="移动端日志" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="移动端设置" width="140"></td>
</tr>
</table>
</div>

> 较新的页面（用户管理、审计记录、限流管理、运维中心、计费、发布管理）还没有截图；
> 它们的版式与上面的页面一致。

---

## 文档

设计文档在 `docs/`，都是在当时做决定的时候写下的，不是事后补的：

| 文档 | 内容 |
|------|------|
| [billing-detail.md](docs/billing-detail.md) | 消费明细、余额流水、CSV 导出 |
| [billing-audit.md](docs/billing-audit.md) | 「余额没被扣」那次排查，以及真正的问题所在 |
| [channel-pricing.md](docs/channel-pricing.md) | 按渠道 / 按凭据的改价工作台 |
| [image-generation.md](docs/image-generation.md) | 生图支持：协议、授权、按张计费 |
| [reliability-first-batch.md](docs/reliability-first-batch.md) | 超时、重试、冷却、等待队列 |
| [ops-observability.md](docs/ops-observability.md) | 日志检索/导出、`/healthz`、`/metrics`、运维概览 |
| [ops-alert-loop.md](docs/ops-alert-loop.md) | 告警闭环与健康指标告警 |
| [ops-second-batch.md](docs/ops-second-batch.md) | 第二批运营可靠性 |
| [probes-and-alert-rules.md](docs/probes-and-alert-rules.md) | 探针鉴权、可配置的告警规则 |
| [backup-hardening.md](docs/backup-hardening.md) | AES-256-GCM 加密、WebDAV 异地推送、校验和 |
| [audit-export-and-low-balance.md](docs/audit-export-and-low-balance.md) | 审计 CSV 导出与低余额告警 |
| [site-info-distribution.md](docs/site-info-distribution.md) | 站点配置端点、消费点、生效时机、版式陷阱 |
| [frontend-tests.md](docs/frontend-tests.md) | vitest 覆盖了什么 |
| [roadmap.md](docs/roadmap.md) | 待办清单，包含被否决项与否决理由 |

---

## 从源码构建

**前置要求：** Go `1.26.4`（见 `go.mod`）、Node.js `^20.19 || >=22.12`（Vite 8 的要求）、pnpm。

```bash
git clone https://github.com/alvindingpeng/tokenmarket.git
cd tokenmarket
cd web && pnpm install && pnpm run build && cd ..   # 必须先构建前端
go run main.go start
```

> 💡 `static/static.go` 用 embed 把 `static/out` 打进二进制。前端必须在后端**之前**构建，否则今天构建出的
> 二进制里装的是昨天的界面。

要部署本分支，有两个构建脚本必须分清：

```bash
bash scripts/build-local.sh -o /path/to/binary   # 本地：前后端一起，且两边都注入版本号
bash scripts/build.sh                            # 发布：交叉编译全平台矩阵
```

`build-local.sh` 从同一个版本源分别给前端传 `VITE_APP_VERSION`、给后端传 `-X internal/conf.Version`。
裸跑 `go build` 会得到 `version=dev`，界面随即告警前后端版本不一致 —— 这个仓库里曾经就把一次陈旧资源告警
误判成真问题过。

**开发模式**（热更新，`/api` 代理到 `127.0.0.1:8080`）：

```bash
cd web && pnpm run dev     # http://localhost:5173
go run main.go start       # 另一个终端
```

---

## 测试与 CI

```bash
cd web && pnpm exec tsc --noEmit        # 类型检查（也包含在 pnpm run build 里）
cd web && pnpm exec vitest run          # 前端单测 24 条 / 4 个文件
go vet ./internal/...                   # 后端静态检查
go test ./internal/...                  # 后端单元测试
gofmt -l internal/server/handlers/      # 必须不输出任何内容（只对 handlers 门禁，internal/ 仍有上游遗留格式）
```

- vitest 覆盖的是**纯前端逻辑**：价格格式化、统计计算、URL 拼装。这套测试是本分支从零建起来的，
  见 [docs/frontend-tests.md](docs/frontend-tests.md)。
- 后端单元测试在 `internal/model`、`internal/op`、`internal/ratelimit`、`internal/relay` 里（自动添加的
  合并规则、告警去重、备份加密与异地推送、日志脱敏、限制钳制、等待队列）。跨全栈的行为由一套仓库外的
  端到端回归覆盖（156 条断言：计费、结算、限流、生图协议、自动添加规则）。它从当前源码
  构建临时二进制，用独立端口与全新 SQLite 库，因此绝不触碰生产数据。
- `.github/workflows/build.yaml` 每次 push 都构建前端与后端。`release.yaml` 与 `template-check.yaml` 来自上游，
  指向上游的 `master` 分支和上游的镜像仓库；本分支的发布由 `scripts/publish-release.sh` 完成（见下）。

---

## 版本号与发布

每次更新都带版本号，并把更新说明写进本仓库。

| 唯一事实来源 | 位置 | 谁读它 |
|--------------|------|--------|
| 版本号 | `main.go` 里的 `// Version vX.Y.Z` 标记 | `scripts/version.sh`、CI 发布、`scripts/publish-release.sh` |
| 更新说明 | [CHANGELOG.md](CHANGELOG.md) 里对应的 `## vX.Y.Z` 章节 | `scripts/publish-release.sh` → GitHub Release 正文 |

本项目的语义化版本约定：新增用户可见能力 +**次版本号**；仅修复、样式、文案与文档 +**修订号**。
运行时版本号出现在 `octopus version`、`/healthz`、`octopus_build_info` 与界面「设置 > 信息」。

`post-commit` 钩子负责实时同步：先把 `HEAD` 推到 GitHub 的 `main`，随后若版本标记变了就打标签、推标签，
并用 CHANGELOG 对应章节创建 Release（幂等：已存在则 PATCH，不产生重复）。日志在 `.git/auto-push.log` 与
`.git/publish-release.log`。

> ⚠️ **提交前先改 `main.go` 的版本号**，不要提交后再补。先提交后改号，会让下一次发布把已有标签的 Release
> 指向新提交，而标签本身还留在旧提交上。

完整流程见 [docs/release-versioning.md](docs/release-versioning.md)，各版本改了什么见 [CHANGELOG.md](CHANGELOG.md)。

---

## 路线图

本分支已知的缺口，以及留在代码旁边的判断：

| 事项 | 状态 |
|------|------|
| 生视频（`/v1/videos`、按秒计费） | 协议位 `1 << 5` 已预留，路由未实现 |
| 生图流式（`partial_images` 事件流）与上游用量对账 | 未开始 |
| 模型能力探测（替代名称关键词归类） | 未开始 |
| 定时同步渠道模型（而非保存时触发） | 暂不采纳：上游费用与变更量不可控 |
| 自动添加时顺带自动定价 / 自动上架 | 刻意排除：两者都是动钱的动作 |
| 界面内自更新 | 本分支停用：`internal/update` 未配置发布源，`/api/v1/update` 会答 `update paused`；请以替换二进制的方式部署 |
| 本分支的容器镜像与二进制产物 | 不发布 |

完整待办见 [docs/roadmap.md](docs/roadmap.md)。

---

## 致谢

- 🙏 [bestruirui/octopus](https://github.com/bestruirui/octopus) —— 本分支所基于的上游项目
- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) —— 本项目的 LLM API 适配模块源自该仓库的实现
- 📊 [sst/models.dev](https://github.com/sst/models.dev) —— AI 模型数据库，提供模型价格数据
- 🇨🇳 [AtomGit](https://atomgit.com/bestruirui/octopus) —— 上游的国内代码托管
- 💬 [Linux.do](https://linux.do/)

## 许可证

与上游一致，AGPL-3.0-only，见 [LICENSE](LICENSE)。

