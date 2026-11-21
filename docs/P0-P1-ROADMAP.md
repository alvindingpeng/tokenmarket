# Switchyard P0/P1 功能开发方案

> **维护约定(务必遵守)**: 本文件是**早期规划稿**, 正文里"待开发"的描述不代表当前代码状态; **状态标记一律以代码为准, 不以本文自述为准**。
> 每次发布若有条目状态变化, 必须同一批次改这里并在表格/文末注明版本, 否则它又会腐烂成误导排期的文档。
> 最后一次全量核对: v1.10.20 发布前(代码基线 v1.10.19, 2026-11-21)。核对方法: 逐条 grep 路由(`NewGroupRouter`/`NewRoute`)、op 函数、`internal/model` 字段与设置键、`web/src/components/modules` 组件, 版本用 `git tag --contains <引入提交>` 求证; **不采用其他 docs/*.md 的自述**。
> 状态三分: `✅ 已完成` / `✅ 部分交付`(必须写明差哪一半) / `🔲 未做`(必须写现状与前置条件)。**不得为了好看把"部分"写成"完成"**。

## 项目背景

Switchyard 是一个自托管的 LLM 网关系统，当前已实现核心功能：
- ✅ 多渠道聚合、智能路由、故障转移
- ✅ 多用户（admin/reseller/user）、API Key 管理
- ✅ 计费系统（预留-结算-对账）、充值提现
- ✅ 审计日志、转发日志、健康监控
- ✅ 限流策略、渠道质量评分、自动熔断

**规划期的问题清单（2026-10-30 立稿时）—— v1.10.19 核对结论见每条的 → 注（第 4、5 条只解决了一部分）；原文保留作立稿依据**：
1. ~~用户无法及时感知余额不足、限流触发、渠道故障等关键事件~~ → 通知中心 + 6 类业务调用点已交付（§P0-1，v1.3.0）
2. ~~用户遇到问题只能外部联系，缺少工单系统~~ → 工单系统已交付（§P0-4，v1.3.0）
3. ~~缺少调用量、成功率、延迟等可视化统计~~ → `/api/v1/user-stats/*` + analytics 页已交付（§P0-3，v1.3.0）
4. ~~系统事件无法推送到外部系统~~ → 通用签名 JSON WebHook 已交付（§P0-5，v1.3.0），但**机器人专有模板仍未做**
5. ~~余额不足时请求突然失败，用户体验差~~ → 仅解决一半：预警与通知已有，**自动充值仍未做**（§P0-2）

---

## 当前实施记录

- 通知中心、余额预警、统计与工单**均已落地**（v1.3.0）；下文早期方案中的「待办」不代表当前代码状态，状态列以本文件各条开头的核对块为准。
- 统计页数据与性能修复已上线（提交 7fbe859），细节见 [ANALYTICS.md](ANALYTICS.md)。
- P0-5 通用签名 JSON Webhook **已交付**（v1.3.0），设计、安全边界及最终验证记录见 [WEBHOOKS.md](WEBHOOKS.md)。
- 本轮不实施机器人专有报文模板，也不自动开启外部订阅；外发由用户显式配置 —— **这两项至今仍未做**，是 §P0-5 的遗留缺口。

## P0 优先级功能（3 项已完成 / 2 项部分交付 / 0 项未做）

> 计数口径：按各条**原定的本期范围**算完成与否，但缺口一律照实写出，不用「完成」把缺口盖掉。
> 已完成：通知中心（原列 3 项待完成，现已全部交付）、工单（原文明示「附件上传与操作历史视图不计入本期完成项」）、WebHook（原文明示「预设模板（可选）」）。
> **部分交付**：实时余额预警（**标题里的「自动充值」一半完全没做**）、API 调用统计（自定义时间范围与独立成功率柱状图没做）。

### 1. ✅ 消息通知中心 【已完成】

**业务价值**：用户实时感知系统事件，减少故障发现时间。

**已完成**：
- ✅ 后端：`notifications` 表、CRUD API、未读计数
- ✅ 前端：通知中心页面、通知铃铛组件
- ✅ 通知类型：system, balance_low, ratelimit_hit, channel_down, channel_review, withdrawal, recharge, dispute, ticket
- ✅ 通知助手函数并**已接入业务逻辑**（原文"待集成到业务逻辑"已过期）

**原「待完成」三项现已全部交付（v1.3.0）**：
- ✅ 业务逻辑集成通知发送：`internal/op/notification_helper.go` 的 `NotifyBalanceLow` / `NotifyRateLimitHitAsync` / `NotifyChannelStatusAsync` / `NotifyChannelReviewAsync` / `NotifyTicket` 均有真实调用点 —— 余额(`op/billing.go:233`、`op/balance_alert.go:119`)、限流 429(`relay/handler.go:173`)、渠道故障与恢复(`op/channelhealth.go:190,224`)、渠道审核(`handlers/channel_review.go:85,103`)、充值/提现(`op/recharge.go:496`、`op/withdrawal.go:369,594`)、工单(`op/ticket.go:523`)。
- ✅ 前端路由/导航：`web/src/stores/app.ts:35` 注册 `{ id: 'notification', icon: Bell }`，`app.tsx:36,201` 懒加载 `NotificationCenter`（`lib/page-preload.ts:18`）。
- ✅ 顶栏铃铛：`web/src/components/app-shell.tsx:164` 渲染 `NotificationBell`（未读数 + 点击展开最近通知）。

**技术栈**：
- 后端：Go + GORM
- 前端：React + TypeScript + Tailwind CSS
- 数据库：notifications 表（user_id, type, title, content, link, status, created_at, read_at）

**API 端点**：
- `GET /api/v1/notification/list` - 通知列表（分页、筛选）
- `GET /api/v1/notification/unread-count` - 未读数量
- `POST /api/v1/notification/mark-read` - 标记已读
- `POST /api/v1/notification/mark-all-read` - 全部标记已读
- `DELETE /api/v1/notification/:id` - 删除通知

---

### 2. ✅ 部分交付：实时余额预警与自动充值（v1.3.0）

> **已按「绝对金额阈值 + 通知中心」方案实现，下文 `balance_alerts` / `threshold_percent` 的百分比方案仅存档**（实际表字段是 `threshold_cny` 绝对金额，不是初始余额的百分比）。
> **已交付**：预警配置与开关（`model/balance_alert.go`）、三接口 `GET/POST/DELETE /api/v1/user/balance-alert`（`handlers/balance_alert.go:16-24`）、余额变动后检查 `op.BalanceAlertCheck`（`op/balance_alert.go:84`，调用点 `op/billing.go:196`；1 小时去重窗口，充值后可再次提醒）、前端设置卡 `wallet/BalanceAlertSettings.tsx`。
> **仍 🔲 未做（本条的「自动充值」一半）**：① **自动充值全无**（全库无 `auto_recharge` / 自动扣款逻辑）；② **余额不足顶部红色 Banner 未做**（无该组件，只有通知中心站内消息）；③ **最近 10 次预警历史未做**（`balance_alerts` 只存 `last_triggered_at`，无历史表与列表）。

**业务价值**：避免余额不足导致请求失败，提升用户体验。

**技术方案**：

#### 后端实现
1. **余额预警表**：
```sql
CREATE TABLE balance_alerts (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id INT NOT NULL,
  threshold_percent INT NOT NULL DEFAULT 20,  -- 阈值百分比（余额 < 初始余额 * threshold%）
  last_triggered_at DATETIME,                 -- 最后触发时间（防止重复通知）
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY idx_user_alert (user_id)
);
```

2. **API 端点**：
   - `GET /api/v1/user/balance-alert` - 查询预警配置
   - `POST /api/v1/user/balance-alert` - 设置预警阈值
   - `DELETE /api/v1/user/balance-alert` - 取消预警

3. **集成点**：
   - 在 `BalanceReservation` 失败时检查余额
   - 如果 `current_balance < initial_balance * threshold%` 且距离上次触发 > 1 小时，发送通知
   - 调用 `NotifyBalanceLow(ctx, userID, currentBalance)`

#### 前端实现
1. **钱包页面增加预警设置**：
   - 阈值选择：10% / 20% / 50% / 自定义
   - 开关：启用/禁用
   - 历史记录：最近 10 次预警时间

2. **余额不足时顶部 Banner**：
   - 红色警告条：「余额不足 ¥X.XX，请及时充值」
   - 快速充值按钮

**工作量**：1-2 天

---

### 3. ✅ 已交付：API 调用统计与可视化（v1.3.0；成功率口径 v1.5.6 修正）

> **已按 `/user-stats/*` + Recharts 实现，下文 `/stats/overview|trend|model-breakdown` 的路由名仅存档**（真实路由见下）。
> 落点：后端 `handlers/user_stats.go:16` 的 `/api/v1/user-stats/{overview,trend,models,latency}`（另有全站口径 `/api/v1/stats/{daily,hourly,total,home,apikey,billing}`）；前端 `web/src/components/modules/analytics/index.tsx`（导航 `stores/app.ts:38`，数据层 `api/user-stats.ts`）。
> 已交付：概览卡（调用量/成功率含样本量/总费用/词元构成与缓存命中）、趋势 `LineChart`、模型 `PieChart`（按费用）与 `BarChart`（按调用量）、延迟面板（`user-stats/latency`，含首字耗时 P95）。时间筛选为 今天/本周/本月 三档预设，**趋势粒度 小时/日/周/月 四档均已支持**（前端 `analytics/index.tsx:87,146` 选择器，后端 `op/user_stats.go:78` 的 `UserStatsTrend` 与 `statsBucketExpr` 的 week/month 分支）。成功率取 `relay_requests` 终态而非计费行（v1.5.6，见 [ANALYTICS.md](ANALYTICS.md)）。
> **仍差的一半**：① **自定义时间范围**未做（`analytics/dates.ts:3` 明确记着「页面没有日期输入控件，custom 会退化成『不限时间』的假范围」）；② **成功率柱状图**未做 —— 成功率只以折线出现在趋势图里，没有独立的按日/周/月柱状图。

**业务价值**：用户直观了解调用趋势、成功率、费用分布，便于优化使用策略。

**技术方案**：

#### 后端实现
1. **聚合查询**（利用现有 `billing_records` 和 `relay_logs` 表）：
```go
// GET /api/v1/stats/overview?start_date=2024-01-01&end_date=2024-01-31
type StatsOverview struct {
    TotalRequests    int64   `json:"total_requests"`
    SuccessRate      float64 `json:"success_rate"`       // 成功率 %
    AvgLatencyMs     float64 `json:"avg_latency_ms"`     // 平均延迟
    TotalCost        float64 `json:"total_cost"`         // 总费用
    TotalTokens      int64   `json:"total_tokens"`       // 总 token 数
}

// GET /api/v1/stats/trend?start_date=...&end_date=...&granularity=day
type StatsTrend struct {
    Date     string  `json:"date"`          // 2024-01-01
    Requests int64   `json:"requests"`
    Cost     float64 `json:"cost"`
    SuccessRate float64 `json:"success_rate"`
}

// GET /api/v1/stats/model-breakdown?start_date=...&end_date=...
type ModelStats struct {
    ModelName string  `json:"model_name"`
    Requests  int64   `json:"requests"`
    Cost      float64 `json:"cost"`
    Tokens    int64   `json:"tokens"`
}
```

2. **SQL 查询示例**（按天聚合）：
```sql
SELECT 
  DATE(created_at) as date,
  COUNT(*) as requests,
  SUM(user_cost) as cost,
  AVG(CASE WHEN status = 'success' THEN 1 ELSE 0 END) * 100 as success_rate
FROM billing_records
WHERE user_id = ? AND created_at BETWEEN ? AND ?
GROUP BY DATE(created_at)
ORDER BY date ASC;
```

#### 前端实现
1. **统计分析页面** (`/stats`)：
   - **概览卡片**（顶部 4 个卡片）：
     - 总调用量
     - 成功率（百分比 + 趋势箭头）
     - 平均延迟（毫秒）
     - 总费用（¥）
   - **调用量趋势图**（折线图，Recharts）
   - **模型分布饼图**（按费用/调用量切换）
   - **成功率柱状图**（按日/周/月）
   - **时间范围筛选**：今天/本周/本月/自定义

2. **图表库**：
   - 使用 `recharts` 或 `chart.js`
   - 响应式设计（移动端适配）

**工作量**：2-3 天

---

### 4. ✅ 工单系统【已完成：纯文本；附件与操作历史仍未做】（v1.3.0）

> 本期接口、安全边界和验收说明见 [TICKETS.md](TICKETS.md)。优先交付文本工单与完整权限状态流转；安全附件上传与专门操作历史视图作为后续增强，不计入本期完成项。
> **核对现状（v1.10.19）**：已交付用户/管理员两套路由（`handlers/ticket.go:28,57`，含 list/detail/reply/close/assign/status）、四态状态机与非法跳转 409、`resolved_at` 满 72 小时自动关闭（`task/init.go:477` 的 `TaskTicketAutoClose`）、新建/回复/状态变更通知（`op/ticket.go:523`）、前端 `modules/tickets/`（列表 + 详情对话）。
> **仍 🔲 未做**：`model/ticket.go:59` 的 `Attachments` 字段**全库无任何读写方**（无上传接口，前端 `tickets/` 无附件控件）；专门的**操作历史视图**也没有。

**业务价值**：用户在系统内提交问题，管理员统一管理，提升服务质量。

**技术方案**：

#### 后端实现
1. **数据库表**：
```sql
-- 工单主表
CREATE TABLE tickets (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id INT NOT NULL,
  title VARCHAR(255) NOT NULL,
  content TEXT NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'open',  -- open, in_progress, resolved, closed
  priority VARCHAR(32) NOT NULL DEFAULT 'normal',  -- low, normal, high, urgent
  category VARCHAR(64),  -- billing, channel, account, other
  assigned_to INT,  -- 分配给哪个管理员（可选）
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  closed_at DATETIME,
  INDEX idx_user (user_id),
  INDEX idx_status (status),
  INDEX idx_assigned (assigned_to)
);

-- 工单消息表（对话记录）
CREATE TABLE ticket_messages (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  ticket_id BIGINT NOT NULL,
  user_id INT NOT NULL,
  content TEXT NOT NULL,
  attachments TEXT,  -- JSON 数组，存储附件 URL
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,  -- 是否管理员回复
  created_at DATETIME NOT NULL,
  INDEX idx_ticket (ticket_id),
  FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
```

2. **API 端点**：
   - **用户端**：
     - `POST /api/v1/ticket` - 创建工单
     - `GET /api/v1/ticket/list` - 我的工单列表
     - `GET /api/v1/ticket/:id` - 工单详情（含消息记录）
     - `POST /api/v1/ticket/:id/reply` - 回复工单
     - `POST /api/v1/ticket/:id/close` - 关闭工单
   - **管理员端**：
     - `GET /api/v1/admin/ticket/list` - 所有工单列表（筛选、分页）
     - `POST /api/v1/admin/ticket/:id/assign` - 分配工单
     - `POST /api/v1/admin/ticket/:id/status` - 变更状态
     - `POST /api/v1/admin/ticket/:id/reply` - 管理员回复

3. **状态机**：
```
open (新建)
  ↓ 管理员开始处理
in_progress (处理中)
  ↓ 管理员标记已解决
resolved (已解决)
  ↓ 用户确认 / 3天自动关闭
closed (已关闭)
  ↓ 用户可重新打开
open
```

4. **通知集成**：
   - 用户创建工单 → 通知管理员
   - 管理员回复 → 通知用户（调用 `NotifyTicket`）
   - 工单状态变更 → 通知用户

#### 前端实现
1. **用户端页面**：
   - **工单列表** (`/tickets`)：
     - 筛选：全部/进行中/已关闭
     - 状态标签：open(蓝)/in_progress(黄)/resolved(绿)/closed(灰)
     - 创建工单按钮
   - **工单详情** (`/tickets/:id`)：
     - 对话式界面（类似聊天）
     - 回复框（支持附件上传）
     - 关闭工单按钮

2. **管理员端页面**：
   - **工单管理** (`/admin/tickets`)：
     - 表格视图：ID、标题、用户、状态、优先级、创建时间
     - 筛选：状态、优先级、分配人
     - 快速操作：分配、变更状态、查看详情
   - **工单详情** (`/admin/tickets/:id`)：
     - 对话记录
     - 侧边栏：状态、优先级、分配、操作历史
     - 管理员回复框

**工作量**：3-4 天

---

### 5. ✅ 已交付：WebHook 出站通知（v1.3.0；投递重放与日志筛选 v1.3.1~v1.4.0）

> **已按「通用签名 JSON」方案实现，下文 `X-Webhook-Signature` 头名与 1s/2s/4s 退避仅存档**（实际头名 `X-Switchyard-Signature`，退避 30s 起倍增、封顶 10 分钟）。
> 落点：后端 `handlers/webhook.go:27` 的 `/api/v1/webhook`（CRUD + `/:id/test` + `/:id/deliveries/:delivery_id/replay`），投递 `op/webhook_dispatch.go`（HMAC-SHA256 = `HMAC(secret, timestamp + "." + rawBody)`，2xx 成功、429/408/5xx/网络失败按指数退避并按 `retry_count` 限次）；事件闭集 `model/webhook.go:37`（10 项，与通知类型同值）；投递与通知写入**同事务入队**。前端 `modules/webhook/`（列表/表单/投递日志/徽章，三语）齐全。设计记录见 [WEBHOOKS.md](WEBHOOKS.md)。
> **仍 🔲 未做**：**机器人专有报文模板（钉钉 Markdown / 飞书卡片 / Slack）全无** —— `internal/alert` 里的 dingtalk/feishu/wecom 是**运维告警渠道**，与本 WebHook 订阅不是同一套，不要混为一谈。

**业务价值**：系统事件推送到外部系统（钉钉、飞书、Slack、自定义 Webhook），实现集成自动化。

**技术方案**：

#### 后端实现
1. **数据库表**：
```sql
CREATE TABLE webhooks (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  user_id INT NOT NULL,
  url VARCHAR(512) NOT NULL,
  secret VARCHAR(128),  -- 用于 HMAC 签名验证
  events TEXT NOT NULL,  -- JSON 数组：["balance_low", "ratelimit_hit", ...]
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  retry_count INT NOT NULL DEFAULT 3,
  last_triggered_at DATETIME,
  last_status VARCHAR(32),  -- success, failed
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  INDEX idx_user (user_id)
);

CREATE TABLE webhook_logs (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  webhook_id BIGINT NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  response_code INT,
  response_body TEXT,
  duration_ms INT,
  created_at DATETIME NOT NULL,
  INDEX idx_webhook (webhook_id)
);
```

2. **API 端点**：
   - `POST /api/v1/webhook` - 创建 Webhook
   - `GET /api/v1/webhook/list` - Webhook 列表
   - `PUT /api/v1/webhook/:id` - 更新 Webhook
   - `DELETE /api/v1/webhook/:id` - 删除 Webhook
   - `POST /api/v1/webhook/:id/test` - 测试 Webhook（发送测试事件）

3. **Webhook 发送逻辑**：
```go
type WebhookPayload struct {
    Event     string                 `json:"event"`      // "balance_low"
    Timestamp int64                  `json:"timestamp"`  // Unix 时间戳
    UserID    uint                   `json:"user_id"`
    Data      map[string]interface{} `json:"data"`       // 事件特定数据
}

// HMAC-SHA256 签名
signature := HMAC-SHA256(secret, jsonPayload)
headers := {
    "Content-Type": "application/json",
    "X-Webhook-Signature": signature,
    "X-Webhook-Event": eventType,
}

// 异步发送 + 重试（3 次指数退避：1s, 2s, 4s）
go func() {
    for attempt := 1; attempt <= maxRetries; attempt++ {
        resp, err := http.Post(url, jsonPayload, headers)
        if err == nil && resp.StatusCode < 300 {
            break
        }
        time.Sleep(time.Second * (1 << (attempt - 1)))
    }
}()
```

4. **支持的事件类型**：
   - `balance_low` - 余额不足
   - `ratelimit_hit` - 限流触发
   - `channel_down` - 渠道故障
   - `channel_review` - 渠道审核
   - `withdrawal_approved` - 提现通过
   - `ticket_reply` - 工单回复

#### 前端实现
1. **Webhook 配置页面** (`/settings/webhooks`)：
   - Webhook 列表（URL、事件、状态、最后触发时间）
   - 创建/编辑表单：
     - URL（必填）
     - 密钥（可选，用于签名验证）
     - 事件选择（多选）
     - 启用/禁用开关
   - 测试按钮（发送测试事件）
   - 日志查看（最近 50 次调用记录）

2. **预设模板**（可选）：
   - 钉钉机器人（Markdown 格式）
   - 飞书机器人（卡片消息）
   - Slack Incoming Webhook

**工作量**：2 天

---

## P1 优先级功能（增强）

### 1. ✅ 已交付：多因素认证（2FA / TOTP）（v1.5.6）

> **已按「标准库自实现 TOTP + 信封加密存密钥」实现，下文「使用 `github.com/pquerna/otp` 库」仅存档** —— 实际 `internal/totp/totp.go` 是**纯标准库** RFC 6238 实现（HMAC-SHA1 / 30s / 6 位 / base32 无填充），`go.mod` 未引入 otp 依赖。
> 落点：`internal/totp/`（`totp.go` + 计数器重放防护测试）；`model/user.go:67-69` 的 `TOTPSecretEncrypted`（**信封加密存储且 `json:"-"` 永不下发**）与 `TOTPEnabled`；接口 `handlers/user_totp.go:33` 的 `POST /api/v1/user/2fa/{setup,enable,disable}`（登录态 + step-up + `Cache-Control: no-store`）；登录链路强制二次校验（缺码 401 `two_factor_required`、错码/重放 401，验证通过前不下发 cookie）；启用时一次性返回 10 个恢复码；启用/禁用均使全部旧会话失效并落审计。前端 `setting/TOTP.tsx`（`qrcode.react` 二维码）+ `TOTPResult.tsx`（恢复码展示）。
> **仍 🔲 未做（小项）**：无「信任设备/记住本机 N 天」；恢复码不支持自助重新生成（只能在 disable 后重绑）。

**技术方案**：
- 后端：`users` 表增加 `totp_secret` 和 `totp_enabled` 字段
- 使用 `github.com/pquerna/otp` 库生成/验证 TOTP
- 登录流程：密码验证通过 → 要求输入 6 位验证码 → 校验通过后颁发 token
- 前端：二维码展示（用户扫码绑定 Google Authenticator）

**工作量**：1-2 天

---

### 2. ✅ 已交付：API Key 使用分析（v1.10.18）

> 原文设想的 `GET /api/v1/apikey/:id/stats` 未按此路径实现；实际交付的是**持久化用量报表**，比原方案更完整。
> 落点：`GET /api/v1/apikey/usage?api_key_id=…`（`handlers/apikey.go:41`，**会话登录态**而非 Bearer Key，故可查任意自有 Key），`op/apikey_usage.go` 的 `APIKeyUsageReport`（窗口合计 / 最近 30 天 / 逐日明细 `days` / `coverage_start` / `retention_days`，口径为「明细 ∪ apikey 日汇总」，明细清理后历史仍可查）；累计支出兜底 `op/apikey_cost.go` 的 `APIKeyCostSumDB`/`APIKeyCostEffective`（max_cost 判定的数据库兜底）；前端 `modules/setting/APIKey.tsx`（用量卡 + 计费明细 CSV 导出），另有 Bearer 侧的 `/api/v1/apikey/stats` 实时计数与 `modules/apikey-dashboard/` 仪表盘。
> **仍差的一半**：**逐日明细已返回但前端未画图** —— `APIKey.tsx` 只渲染 total/recent 两行数字，`usage.days` 没有趋势图；原文设想的「调用量、费用趋势图」在这一侧仍未做。

**业务价值**：用户了解每个 API Key 的调用量、费用、成功率，便于管理和优化。

**技术方案**：
- 后端：利用现有 `billing_records` 按 `api_key_id` 聚合
- API：`GET /api/v1/apikey/:id/stats?start_date=...&end_date=...`
- 前端：API Key 详情页增加统计图表（调用量、费用趋势）

**工作量**：1 天

---

### 3. ✅ 渠道成本追踪与利润分析（已完成，v1.4.0）

**业务价值**：管理员了解每个渠道的成本、收入、利润，优化渠道配置。

**技术方案**：
- 后端：`billing_records` 已有 `supply_price_input/output` 和 `user_price_input/output`
- API：`GET /api/v1/channel/profit/:id?start_date=...&end_date=...`（管理员，最多 366 天）
- 前端：渠道详情页增加利润分析（收入、成本、利润率）

**工作量**：1-2 天

---

### 4. ✅ 已交付：用户分级与配额管理（v1.5.6）

> 落点：`model/user.go:51` 的 `Tier` 列（free/basic/pro/enterprise，索引）+ `EffectiveTier()`（:154，**空串回落 free** —— 存量账号列值为空串，当「无限制」会让全部老用户一夜无上限）；等级配额集中设置键 `user_tier_policies`（`model/user_tier.go` 的 `DefaultUserTierPolicies`：free 20 RPM/4 万 TPM/并发 2 … enterprise 不限；写坏时校验拒绝保存、运行期回落出厂默认）；**兜底而非覆盖**接入限流（`relay/ratelimit.go:16`）与配额展示（`op/quota_usage.go:55`），仅当用户维度无显式限流策略才按等级补位；管理端改 tier 接口带审计（`handlers/user.go:411` 记 `tier: old -> new`，创建时也记 :356）；前端 `setting/TierPolicies.tsx`（四档配额编辑）+ `modules/users/index.tsx`（改等级）。
> **仍差的一半**：**模型权限（可访问模型白名单）没有** —— 等级目前只影响限流兜底额度，原文「差异化服务(限流、**模型权限**)」的模型权限一半未做。
> 设计记录见 [audit-archive-and-user-tier.md](audit-archive-and-user-tier.md)。

**业务价值**：区分免费/付费用户，实现差异化服务（限流、模型权限）。

**技术方案**：
- 后端：`users` 表增加 `tier` 字段（free, basic, pro, enterprise）
- 限流策略与用户等级挂钩
- 前端：用户管理页面增加等级设置

**工作量**：2 天

---

### 5. ✅ 部分交付：审计日志导出与归档（导出 v0.14.0；归档 v1.5.6）

> **导出已交付**：`GET /api/v1/audit/export`（`handlers/audit.go:39,102`）输出 **CSV**，单次上限 5000 行，超出时在文件末尾追加 `# truncated: exported N of M rows` 而不是静默截断；导出本身也记审计（`audit.export`），前端 `modules/audit/index.tsx:75,128` 有导出按钮。**但 `format` 参数并未实现**（无 JSON 导出，只此 CSV）。
> **归档已交付**：`audit_retention_days`（默认 180）+ `audit_archive_enabled`（默认 true）+ 任务 `TaskAuditArchive`（每天一次、启动即跑），**顺序强制**：超期记录先写当日 NDJSON 并 `fsync`，确认落盘后才删除；归档失败则本轮一条都不删并发 `audit_archive_failed` 告警。运维接口 `POST /api/v1/ops/audit-lifecycle/run`（step-up）与 `GET …/list`。
> **仍 🔲 未做**：**对象存储（S3/OSS）推送没有** —— 归档只落本地 `data/archives/audit/audit-YYYYMMDD.ndjson`（[audit-archive-and-user-tier.md](audit-archive-and-user-tier.md) 明确记为不推对象存储）；JSON 导出格式亦未做。

**业务价值**：合规需求，长期保存审计日志（CSV/JSON 导出）。

**技术方案**：
- 后端：`GET /api/v1/audit/export?start_date=...&end_date=...&format=csv`
- 定时任务：按月归档审计日志到对象存储（S3/OSS）

**工作量**：1 天

---

## 开发优先级总结

| 功能 | 优先级 | 工作量 | 状态（以代码为准，核对于 v1.10.20 前） |
|------|--------|--------|------|
| 消息通知中心 | P0 | 2-3天 | ✅ 已完成(v1.3.0: 表/CRUD/未读计数/助手函数接入/导航/顶栏铃铛) |
| 实时余额预警 | P0 | 1-2天 | ✅ **部分交付**(v1.3.0): 预警配置+通知已交付; **自动充值 / 顶部 Banner / 预警历史未做** |
| API 调用统计 | P0 | 2-3天 | ✅ **部分交付**(v1.3.0, 成功率口径 v1.5.6): 概览/趋势/模型/延迟已交付(粒度 小时/日/周/月); **自定义时间范围、独立成功率柱状图未做** |
| 工单系统 | P0 | 3-4天 | ✅ 已完成(v1.3.0, 本期纯文本): 四态状态机+72h 自动关闭+通知; **附件上传 / 操作历史视图未做** |
| WebHook 通知 | P0 | 2天 | ✅ 已完成(v1.3.0; 重放与日志筛选 v1.3.1~v1.4.0): 订阅 CRUD/HMAC 投递/退避重试/投递日志与重放/三语界面; **机器人专有模板未做** |
| 多因素认证 | P1 | 1-2天 | ✅ 已完成(v1.5.6): 标准库 RFC 6238 TOTP/信封加密密钥/step-up/恢复码/登录强制二次校验/二维码; **无信任设备、恢复码不可自助重生成** |
| API Key 分析 | P1 | 1天 | ✅ **部分交付**(v1.10.18): 持久化用量报表(total/recent/逐日)+累计支出兜底+明细导出; **逐日明细前端未画趋势图** |
| 渠道利润分析 | P1 | 1-2天 | ✅ 已完成(v1.4.0) |
| 用户分级管理 | P1 | 2天 | ✅ **部分交付**(v1.5.6): tier 列+EffectiveTier+四档配额兜底限流+审计; **模型权限白名单未做** |
| 审计日志归档 | P1 | 1天 | ✅ **部分交付**(导出 v0.14.0 / 归档 v1.5.6): CSV 导出(5000 上限, 截断自述)+NDJSON 先归档后删除+失败零删除告警; **S3/OSS 推送 / JSON 导出格式未做** |

**P0 交付情况**：3 项按本期范围已完成（通知中心/工单/WebHook，遗留缺口见上表）、2 项部分交付（余额预警、API 统计）。最容易误读的是「实时余额预警」—— 它的**自动充值一半完全没有**。  
**P1 交付情况**：5 项均已落地（其中 3 项为部分交付，缺口见上表逐条）。**「待开发」的只剩各条后半句的缺口，不是整条功能。**  
**原估算工作量**（10-14 天 P0 / 6-8 天 P1）为规划期数字，已无排期意义，保留存档。

---

## 技术栈

- **后端**：Go 1.26 + Gin + GORM + SQLite/MySQL/PostgreSQL
- **前端**：React 18 + TypeScript + Tailwind CSS + Recharts
- **部署**：单二进制 + 嵌入式前端 + systemd
- **监控**：Prometheus `/metrics` + 健康检查 `/healthz`

---

## 下一步行动

> 本清单是 2026-10-30 的规划动作，**已全部完成**（v1.10.19 核对）。保留原文仅作历史记录；后续真正待开发的只有上表各「部分交付」条目里写明的缺口。

1. ✅ **完成通知中心集成**（0.5 天）：已完成（v1.3.0）
   - 顶部导航栏集成 NotificationBell：`app-shell.tsx:164`
   - 业务逻辑发送通知（余额不足、限流触发、渠道故障等）：`op/notification_helper.go` 各 Notify* 均有真实调用点
   - 前端路由配置：`stores/app.ts:35` + `app.tsx:36,201`

2. ✅ **开发余额预警功能**（1-2 天）：已完成预警配置与通知（v1.3.0）；**自动充值仍未做**

3. ✅ **开发 API 调用统计**（2-3 天）：已交付 `/api/v1/user-stats/*` + `modules/analytics/`（v1.3.0）

4. ✅ **开发工单系统**（3-4 天）：已交付（v1.3.0，纯文本；附件与操作历史未做）

5. ✅ **开发 WebHook 通知**（已完成，v1.3.0：订阅 CRUD、HMAC 签名投递、重试、日志与重放、三语管理界面）

6. ✅ **渠道利润分析**（已完成，v1.4.0：按渠道聚合收入/成本/毛利/毛利率与逐日趋势，管理员专属，`billing_records` 新增 `(channel_id, created_at)` 复合索引，单次查询上限 366 天）

---

## 文档维护

- **作者**：AI Assistant
- **创建时间**：2026-10-30
- **最后更新**：2026-11-21（v1.10.20 前全量核对，代码基线 v1.10.19）
- **版本**：v1.2

### 本轮(v1.2)修正摘要 —— 全部以代码为准

| 条目 | 原状态 | 核对结论 |
|---|---|---|
| §P0-1 通知中心 | ✅ 已完成，但列着 3 项 🔲「待完成」 | **3 项早已全部交付**(v1.3.0)：Notify* 均有真实调用点 / NAV / 铃铛 |
| §P0-2 实时余额预警 | 🔲 待开发 | **部分交付**(v1.3.0)：配置+通知已有；**自动充值、顶部 Banner、预警历史未做** |
| §P0-3 API 调用统计 | 🔲 待开发 | **部分交付**(v1.3.0)：user-stats 路由 + analytics 页（概览/趋势/模型/延迟，粒度小时/日/周/月）已交付；**自定义时间范围、独立成功率柱状图未做** |
| §P0-4 工单系统 | ✅ 已完成 | **如实**(v1.3.0)：附件上传与操作历史视图未做 |
| §P0-5 WebHook 出站 | 🔲 待开发 | **已交付**(v1.3.0; 重放/日志筛选 v1.3.1~v1.4.0)：webhook 全套 + HMAC 投递 + 退避重试；**机器人模板未做** |
| §P1-1 2FA/TOTP | 🔲 待开发 | **已交付**(v1.5.6)：纯标准库 RFC 6238 + 信封加密密钥 + step-up + 恢复码 + 登录强制二次校验 |
| §P1-2 API Key 分析 | 🔲 待开发 | **部分交付**(v1.10.18)：持久化用量报表 + 累计支出兜底 + 明细导出；**逐日明细前端无趋势图** |
| §P1-3 渠道利润分析 | ✅ 已完成(v1.4.0) | 核实无误：handlers/channel.go:47 的 /profit/:id（仅 Admin） |
| §P1-4 用户分级与配额 | 🔲 待开发 | **部分交付**(v1.5.6)：tier 列 + EffectiveTier + 四档配额兜底 + 审计；**模型权限白名单未做** |
| §P1-5 审计导出与归档 | 🔲 待开发 | **部分交付**(导出 v0.14.0 / 归档 v1.5.6)：CSV 导出 + 先归档后删除；**S3/OSS 推送、JSON 格式未做** |

**教训**: 本文历史上把 7 条早已交付的功能标成 🔲, 根因是「规划稿写完就没人回头核代码」。改本文时**只用 grep 结果下结论**, 且「部分交付」必须写清差哪一半。
