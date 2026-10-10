# 更新说明

本项目遵循[语义化版本](https://semver.org/lang/zh-CN/)，版本号唯一来源是 `main.go` 里的
`// Version vX.Y.Z` 标记（`scripts/version.sh` 读取它，CI 的 release workflow 也解析同一行）。
每次发布把新章节追加到本文件顶部，GitHub Release 的正文由 `scripts/publish-release.sh`
自动取本文件中对应版本的那一节。

## v0.18.0 — 2026-10-11

本版本实现三大核心功能模块，显著增强平台的运维能力和稳定性保障。

### 1. API Key 配额管理

- **配额限制**：支持每日/每月请求次数和成本双重配额
- **自动控制**：配额超限时自动禁用 API Key 并触发告警
- **实时统计**：新增 `GET /api/v1/apikey/:id/quota` 查询当前用量
- **UI 集成**：API Key 列表显示配额进度条，编辑页支持配额配置
- **数据库迁移**：`013_apikey_quota.sql` 新增 `max_requests_per_day/month`、`max_cost_per_day/month`、重置时间戳字段

### 2. 告警通知系统

- **多通道支持**：邮件（SMTP）、Telegram Bot、Webhook 三种告警方式
- **完整基础设施**：告警表、去重逻辑（kind+code+24h 窗口）、后台工作线程
- **内置场景**：余额不足、API Key 配额超限、渠道连续失败、成功率下降
- **管理 API**：`GET /api/v1/alert` 查询记录，`POST /api/v1/alert/:id/resolve` 手动解决
- **指标暴露**：Prometheus `octopus_alerts_raised_24h` 统计最近 24 小时告警数
- **数据库迁移**：`014_alerts.sql` 告警表 + `015_alert_settings.sql` 配置表

### 3. 多上游 Key 轮换

- **多 Key 池**：每个渠道支持配置多个上游 API Key
- **三种策略**：轮询（round-robin）、加权（weighted）、故障转移（failover）
- **故障隔离**：连续失败达阈值自动排除 Key，冷却期后自动恢复
- **独立统计**：每个 Key 维护独立的成功率和延迟指标
- **管理 API**：`GET/POST/DELETE /api/v1/channel/:id/keys` 管理 Key 池，`PUT /api/v1/channel/:id/rotation` 配置策略
- **数据库迁移**：`016_channel_keys.sql` 新增 `channel_keys` 表和轮换策略字段

### 技术细节

- 配额检查在 `BillingReserve` 前执行（relay handler），避免无效请求消耗余额
- 告警后台线程每分钟扫描待发送告警，发送成功后更新状态
- Key 轮换状态缓存在内存 `channelKeyCache`，定期持久化到数据库
- 故障检测基于滑动窗口（最近 N 次请求），支持配置失败阈值和冷却时间
- 所有新功能的设置项存储在 `settings` 表，通过 `SettingGet*/SettingSet*` 系列函数访问

## v0.17.1 — 2026-10-10

功能与增强（本批次一并上线）：

- **登录暴力破解防护**：按「IP + 账号」双维度指数退避锁定，参数在设置页可调
  （`login_max_attempts` 默认 5、`login_lockout_seconds` 默认 900，0 关闭退避）。
- **凭据轮换**：`POST /api/v1/ops/auth-secret/rotate` 轮换 JWT 签名密钥（管理员）。
- **服务端登出**：`POST /api/v1/user/logout` 吊销令牌版本；认证 Cookie 全链路改为 httpOnly。
- **统计口径统一**：`stats` 的输入 Token 改为「净输入读」口径，与计费 / `usageBuckets` 一致；
  日志 CSV 导出补 `cache_write_tokens` 列。
- **路由策略优化**：成员失败后若同组仍有可用成员则立即换成员，不再在失效端点上等待重试间隔。
- **评分按上游模型全局共享**：同一渠道模型在任意分组、任意用户下共享同一份延迟/成功率成绩；
  某模型表现差只降它自己，不牵连同渠道其它模型。
- **可靠性软性惩罚**：综合评分中，成功率低于可靠性下限（`reliability_floor`，默认跟随系统 70%）
  的成员按 `succ/floor` 比例折减总分，而非无条件垫底——既保留价格权重主导（修复「价格 60 却未
  优先调用最便宜模型」），又把彻底不可用（成功率≈0）的成员自动压到垫底。冷成员不惩罚，保留一次试用。
- **在线更新健康门**：更新重启后自动探测 `/healthz`，异常则回滚到更新前的备份二进制。
- **代理信任收敛**：`server.trusted_proxies` 默认为空，`c.ClientIP()` 不再无条件信任
  `X-Forwarded-For`（反向代理场景需显式配置该键）。
- **品牌**：以 shareapi 为主题重设计站点标识（网关方块 + 共享端点 + 分发连线），重生成 favicon /
  PWA 图标 / 首屏开启动画，并把 `logo.svg` 纳入 service worker 预缓存、缓存版本升到 v4。

修复：

- `stats_hourlies` 因 `hour` 主键被 GORM 当作自增，导致 hour=0 落库错位（午夜桶空白）；
  改 `autoIncrement:false` 并加迁移 17 一次性清理历史越界小时行。

## v0.16.0 — 2026-10-10

功能：恢复指向本仓库的在线更新检查与管理员一键更新。更新器会校验平台发布包、目标版本并原子替换服务程序；Release 流程同步发布各平台更新包。

修复：日志详情页的「缓存率」此前用 `缓存命中 / 全部输入 Token` 计算，而上游归一化后的
`prompt_tokens` 已把**写缓存**（cache_creation）一并计入，于是走显式缓存写入的那批请求命中率被
大幅压低（实测一行 6%，按后端计费桶口径应为 100%，最大偏差 94 个百分点）；同一屏的「输入」列只扣了
缓存读、没扣缓存写，与「缓存」「缓存写」三列相加会超过真实输入。现统一为一组纯函数
（`web/src/lib/log-usage.ts`，附 12 个单测）：命中率 = 缓存读 / (全部输入 − 缓存写)，输入列 = 全部输入 −
缓存读 − 缓存写，口径与后端 `internal/relay/state.go` 的 `usageBuckets` 一致；比率保留一位小数，
不再因取整而分不清 49.8% 与 49.9%。

## v0.15.1 — 2026-10-09

文档：按当前实际运行的系统重写 `README.md` 与 `README_zh.md`，中英两份结构、表格与事实一一对应。
不新增功能，不改变任何运行时行为。

### 重写要点（中英一致）

- **以本分支为准**：开篇写明仓库身份（`alvindingpeng/tokenmarket`，基线 `v0.13.9`）与「本文件描述
  本分支当前真实运行的能力」，并给出指向 CHANGELOG 的差异入口。
- **不夸大产物**：明确本分支不发布二进制、不发布容器镜像 —— 上游的 `release.yaml` 只在上游 `master`
  分支触发，故这里的 Release 只有更新说明没有 `octopus-*.zip`；`docker-compose.yml` 的 `image:` 仍指向上游
  镜像名（两者在 Docker Hub 均查无此库），已在文档中提示先替换再用。
- **凭据不再泄漏**：旧文档的三处客户端示例内嵌了真实 `sk-octopus-…` 密钥，统一改为 `sk-octopus-REPLACE_ME`；
  同时把密钥长度改为实测的 48 位（`GenerateAPIKey` 生成 48 位随机串）。
- **校正事实**：`/v1` 只认 API 密钥（`Authorization: Bearer` 或 `x-api-key`，会话 Cookie 不适用，且停用 /
  过期 / 用尽 `max_cost` 在入口拒绝）；Node.js 要求改为 Vite 8 的 `^20.19 || >=22.12`；测试一节补上
  `go test ./internal/...`，并注明 `gofmt -l` 只对 `internal/server/handlers/` 门禁（`internal/` 仍带上游
  遗留格式差异）。
- **新增章节**：客户端接入示例（OpenAI SDK / Claude Code / Codex，密钥占位）、界面截图（仅列仓库内
  真实存在的 12 张 PNG，并说明更新页面尚未截图）、文档索引（`docs/` 15 篇）。
- **路线图如实列缺口**：生视频协议位已预留但未实现、`partial_images` 未做、能力探测未做、定时同步渠道模型
  不采纳、自动定价 / 自动上架刻意排除、界面内自更新停用（`/api/v1/update` 回 `update paused`）、不发布镜像。

## v0.15.0 — 2026-10-09

修复「管理员设置页面修改系统信息配置未生效」: 此前站点名称、描述、联系方式、公告、维护提示
只有写入端（设置存储保存成功），没有任何消费端，所以改完确实"看不到效果"。本版补齐分发与消费链路。

### 新增功能

- **公开站点信息端点** `GET /api/v1/site/config`：白名单下发 site_name / site_description /
  site_contact / announcement / maintenance_mode / maintenance_notice 六个字段，无需登录即可读取，
  未登录的登录页因此也能显示站点身份。公告按 `site_announcement_enabled` 开关在后端过滤，
  前端只判断正文是否为空；维护提示为空时回退 `service under maintenance`。
- **浏览器标签页标题跟随站点名称**（`web/src/lib/site-title.ts`）：挂在 `AppContainer` 最外层，
  登录页与已登录界面都生效；名称留空回退默认品牌 Octopus，不会把标题清成空串。
- **全局横幅 `SiteBanners`**：维护模式提示（红色、常驻不可关闭 —— 管理员必须看得见自己开着
  开关）与公告（可关闭，按正文记忆在 `sessionStorage`，改写公告后自动重新出现），
  同时接入应用外壳、密钥视图与登录页三处。
- **登录页展示站点身份**：标题下的站点名称（留空回退品牌名）、一句话描述、联系方式。
  联系方式自动识别 http(s) 链接与邮箱并渲染成可点链接，纯文本原样显示。
- **分享图与密钥视图抬头**同样读取站点名称，不再硬编码品牌名。

### 问题修复

- 注册开关接口 `/api/v1/user/register-config` 补发 `approval_required`：此前前端恒走
  "注册成功，请登录"分支，开启审批后新注册用户看到的提示是错的。
- 登录页站点文案统一由 `/api/v1/site/config` 提供，`register-config` 不再重复下发同名三字段，
  避免同一份配置出现两个数据来源。
- 保存任意设置后除失效 `['settings','list']` 外，同时失效 `['site','config']` 与
  `['user','register-config']`：管理员改完配置立刻反映到标题、公告与维护横幅，无需刷新页面。

### 工程与发布

- 新增 `docs/site-info-distribution.md`: 记录站点信息的唯一下发端点、各字段消费点、缓存失效
  责任划分，以及 `AppShell` 网格横幅槽的版式陷阱（条件渲染整行会塌陷内容区高度）。
- `AppShell` 网格增加独立的横幅行（`md:grid-rows-[auto_auto_minmax(0,1fr)]`，导航
  `md:row-span-3`）：横幅出现或消失都不改变导航 sticky 容器的高度，也不挤占内容区滚动高度。

## v0.14.0 — 2026-10-09

基于上游 v0.13.9 的本项目第一批完整发布，汇总此前未成版本的全部开发与修复。

### 新增功能

- **渠道模型自动添加（v1）**：渠道编辑页新增「自动添加」开关，保存渠道后探测上游 `/models`
  并入渠道。语义为只增不删、授权按位 OR 并入、单次上限 200 个模型、写入
  `channel.model-added` 审计；探测逻辑抽到 `internal/op/channel_probe.go`，与手动刷新共用同一实现。
- **生图支持**：`/v1/images/generations` 与 `/v1/images/edits` 两条 OpenAI Images 协议路由完整转发，
  按张计费；预扣保守张数由 `balance_reserve_images` 参数化（默认 4）。
- **计费与对账**：消费明细（筛选 + 分页 + CSV 导出）、用户余额流水、价格快照与对账、
  低余额告警、审计日志 CSV 导出。
- **运营可靠性两批**：渠道健康探测（延迟超阈值判 degraded、连续 5 次失败才判 down、恢复补发
  recovered）、告警规则可配置、告警闭环、自动备份（加密 / WebDAV 异地推送 / 校验和）、
  日志生命周期、限流实时用量、`/healthz` 与 `/metrics`（含 `metrics_auth` 鉴权选项）、运维概览。
- **渠道模型改价工作台**：发布管理内直接维护渠道商供货价，用户价按全局上浮比例联动。
- **分组编辑置顶/置底**（继承上游 `0538c3e`）、**子路径部署**用于反向代理场景（继承上游 `bdc9948`）。
- **系统信息配置**：站点名称 / 描述 / 联系方式 / 公告 / 维护模式（消费方接线仍在排期内）。

### 问题修复

- 渠道编辑弹窗在矮屏（手机、平板横屏、压缩后的桌面窗口）看不到「保存」按钮：
  表单与详情区高度原先写成固定 24/29rem，会被 `max-h-[calc(100dvh-1rem)]` 压缩的弹窗
  配合 `overflow-hidden` 把底部按钮行裁出可视区。改为统一的 `SHELL_HEIGHT` 高度阶梯
  `min(24rem,100dvh-5rem)` / `min(29rem,100dvh-5rem)`，常态尺寸不变，矮屏按可视区夹住。
- 发布管理「模型上架与定价」弹窗被 JSX 注释挤压布局、把注释文本渲染成可见内容；
  同时修复弹窗内列表横向滚动与底部按钮常驻。
- 版本号注入缺失导致「设置 > 信息」误报前端/后端版本不一致（浏览器缓存告警）：
  本地构建脚本改为同时向后端 `conf.Version` 与前端 `VITE_APP_VERSION` 注入同一版本号。

### 工程与发布

- 新增 `scripts/version.sh`（读取版本标记）、`scripts/build-local.sh`（前端 + 注入版本的后端一体化构建）、
  `scripts/publish-release.sh`（推送标签并携带本文件对应章节创建 GitHub Release）。
- 提交后自动实时推送到本仓库 `main` 分支，并在版本变更时发布 Release（`.git/hooks/post-commit`）。
- 前端单测（vitest）从 0 建起：定价 / 统计 / URL 拼装等纯逻辑 24 个用例；
  后端 e2e 回归 156/156 通过（含 9 条自动添加断言）。

## 上游基线 v0.13.9 及更早

v0.13.9 以前的历史与上游 [bestruirui/octopus](https://github.com/bestruirui/octopus) 一致，
更新说明见上游的提交记录（`git log --oneline v0.13.9`）。
