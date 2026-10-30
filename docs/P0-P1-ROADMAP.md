# Switchyard P0/P1 功能开发方案

## 项目背景

Switchyard 是一个自托管的 LLM 网关系统，当前已实现核心功能：
- ✅ 多渠道聚合、智能路由、故障转移
- ✅ 多用户（admin/reseller/user）、API Key 管理
- ✅ 计费系统（预留-结算-对账）、充值提现
- ✅ 审计日志、转发日志、健康监控
- ✅ 限流策略、渠道质量评分、自动熔断

**存在的问题**：
1. 用户无法及时感知余额不足、限流触发、渠道故障等关键事件
2. 用户遇到问题只能外部联系，缺少工单系统
3. 缺少调用量、成功率、延迟等可视化统计
4. 系统事件无法推送到外部系统（钉钉、飞书、Webhook）
5. 余额不足时请求突然失败，用户体验差

---

## P0 优先级功能（已完成 4/5）

### 1. ✅ 消息通知中心 【已完成】

**业务价值**：用户实时感知系统事件，减少故障发现时间。

**已完成**：
- ✅ 后端：`notifications` 表、CRUD API、未读计数
- ✅ 前端：通知中心页面、通知铃铛组件
- ✅ 通知类型：system, balance_low, ratelimit_hit, channel_down, channel_review, withdrawal, recharge, dispute, ticket
- ✅ 通知助手函数（待集成到业务逻辑）

**待完成**：
- 🔲 在业务逻辑中集成通知发送（余额不足、限流触发、渠道故障等）
- 🔲 前端路由配置（将通知中心加入导航菜单）
- 🔲 在顶部导航栏集成 NotificationBell 组件

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

### 2. 🔲 实时余额预警与自动充值

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

### 3. 🔲 API 调用统计与可视化

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

### 4. ✅ 工单系统【已完成】

> 本期接口、安全边界和验收说明见 [TICKETS.md](TICKETS.md)。优先交付文本工单与完整权限状态流转；安全附件上传与专门操作历史视图作为后续增强，不计入本期完成项。

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

### 5. 🔲 WebHook 出站通知

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

### 1. 🔲 多因素认证（2FA / TOTP）

**业务价值**：提升账号安全性，防止密码泄露导致的账号被盗。

**技术方案**：
- 后端：`users` 表增加 `totp_secret` 和 `totp_enabled` 字段
- 使用 `github.com/pquerna/otp` 库生成/验证 TOTP
- 登录流程：密码验证通过 → 要求输入 6 位验证码 → 校验通过后颁发 token
- 前端：二维码展示（用户扫码绑定 Google Authenticator）

**工作量**：1-2 天

---

### 2. 🔲 API Key 使用分析

**业务价值**：用户了解每个 API Key 的调用量、费用、成功率，便于管理和优化。

**技术方案**：
- 后端：利用现有 `billing_records` 按 `api_key_id` 聚合
- API：`GET /api/v1/apikey/:id/stats?start_date=...&end_date=...`
- 前端：API Key 详情页增加统计图表（调用量、费用趋势）

**工作量**：1 天

---

### 3. 🔲 渠道成本追踪与利润分析

**业务价值**：管理员了解每个渠道的成本、收入、利润，优化渠道配置。

**技术方案**：
- 后端：`billing_records` 已有 `supply_price_input/output` 和 `user_price_input/output`
- API：`GET /api/v1/admin/channel/:id/profit?start_date=...&end_date=...`
- 前端：渠道详情页增加利润分析（收入、成本、利润率）

**工作量**：1-2 天

---

### 4. 🔲 用户分级与配额管理

**业务价值**：区分免费/付费用户，实现差异化服务（限流、模型权限）。

**技术方案**：
- 后端：`users` 表增加 `tier` 字段（free, basic, pro, enterprise）
- 限流策略与用户等级挂钩
- 前端：用户管理页面增加等级设置

**工作量**：2 天

---

### 5. 🔲 审计日志导出与归档

**业务价值**：合规需求，长期保存审计日志（CSV/JSON 导出）。

**技术方案**：
- 后端：`GET /api/v1/audit/export?start_date=...&end_date=...&format=csv`
- 定时任务：按月归档审计日志到对象存储（S3/OSS）

**工作量**：1 天

---

## 开发优先级总结

| 功能 | 优先级 | 工作量 | 状态 |
|------|--------|--------|------|
| 消息通知中心 | P0 | 2-3天 | ✅ 已完成 |
| 实时余额预警 | P0 | 1-2天 | ✅ 已完成 |
| API 调用统计 | P0 | 2-3天 | ✅ 已完成 |
| 工单系统 | P0 | 3-4天 | ✅ 已完成（本期纯文本） |
| WebHook 通知 | P0 | 2天 | 🔲 待开发 |
| 多因素认证 | P1 | 1-2天 | 🔲 待开发 |
| API Key 分析 | P1 | 1天 | 🔲 待开发 |
| 渠道利润分析 | P1 | 1-2天 | 🔲 待开发 |
| 用户分级管理 | P1 | 2天 | 🔲 待开发 |
| 审计日志归档 | P1 | 1天 | 🔲 待开发 |

**P0 总工作量**：10-14 天  
**P1 总工作量**：6-8 天  
**全部工作量**：16-22 天（约 3-4 周）

---

## 技术栈

- **后端**：Go 1.26 + Gin + GORM + SQLite/MySQL/PostgreSQL
- **前端**：React 18 + TypeScript + Tailwind CSS + Recharts
- **部署**：单二进制 + 嵌入式前端 + systemd
- **监控**：Prometheus `/metrics` + 健康检查 `/healthz`

---

## 下一步行动

1. ✅ **完成通知中心集成**（0.5 天）：
   - 在顶部导航栏集成 NotificationBell
   - 在业务逻辑中发送通知（余额不足、限流触发等）
   - 前端路由配置

2. 🔲 **开发余额预警功能**（1-2 天）

3. 🔲 **开发 API 调用统计**（2-3 天）

4. 🔲 **开发工单系统**（3-4 天）

5. 🔲 **开发 WebHook 通知**（2 天）

---

## 文档维护

- **作者**：AI Assistant
- **创建时间**：2026-10-30
- **最后更新**：2026-10-30
- **版本**：v1.0
