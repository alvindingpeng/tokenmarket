# 余额充值 / 兑换码 / 渠道商提现 — 设计方案

> 状态：**P1/P2/P3 已全部实现并上线**（2026-10-21 复核更正：本文此前误标「尚未实现」，实际代码已完整落地）。
> P4 增强项（Stripe/支付宝当面付完整形态、满赠活动、自动打款、提现风控增强）仍为预留。
> 未实施清单见 `docs/backlog-unimplemented.md`。
> 范围：余额充值（第三方支付 + 兑换码）、兑换码管理、渠道商收入提现
> 参考实现：New API（QuantumNous/new-api）的 TopUp / Redemption / 支付设置，已核对其源码关键机制（见 §3）

---

## 1. 目标与范围

**目标**
1. 用户可自助充值余额：走第三方支付平台（易支付 Epay 协议起步，后续 Stripe / 支付宝当面付）。
2. 用户可用兑换码入账余额；管理员可批量生成、管理兑换码。
3. 渠道商可把累计收益（供货价收入）申请提现，管理员审核打款。
4. 全部资金动作有流水、可对账、可审计；与现有计费（预扣/结算/释放）体系无缝衔接。

**不做（本期）**
- 发票、退款流程（仅预留订单状态 `refunded`）
- 自动打款（支付宝转账 API / 微信商家转账）——留到 Phase 4，v1 人工打款
- 法币多币种结算 —— v1 单币种 + 可配汇率

---

## 2. 现状底座（已具备，直接复用）

| 能力 | 现状 | 位置 |
|---|---|---|
| 账户余额 | `User.Balance`（可用）/`User.Frozen`（预扣冻结），decimal(18,6) | `internal/model/user.go:39` |
| 累计口径 | `TotalSpent`（消费，上浮后）/`TotalRevenue`（渠道商累计收入，供货价） | `internal/model/user.go:41-42` |
| **充值预留字段** | `TotalRecharged` —— 注释明确「充值功能上线后累计充值额」 | `internal/model/user.go:44` |
| 余额流水 | `balance_ledgers`（LedgerList / LedgerQuery，按 kind 过滤） | `internal/op` |
| 计费流水 | `BillingRecord`：`UserCost = OwnerRevenue + PlatformRevenue` 三方对平 | `internal/model/billing.go:6` |
| 预扣/结算 | `BalanceReservation`（预扣/结算/释放逐笔对应）、`BillingSettle` | `internal/op` |
| 管理员调账 | `op.UserSetBalance`（写 ledger + 审计 `user.balance`） | `internal/op:187` |
| 对账 | `BillingReconcile`（balanced / mismatch / review，每日 `run`） | `internal/model/billing.go:14` |
| 设置与密钥 | `SettingKey` 体系，**已有 secretKeys 加密存储机制** | `internal/model` |
| 角色 | admin / **reseller（渠道商）** / user | `internal/model/user.go:14` |
| 审计 | `audit()` 全链路动作记录 | handlers |

**关键事实（决定提现模型）**：渠道商收益结算只执行
`UPDATE users SET total_revenue = total_revenue + ? WHERE id = ?`
—— 收益**只累计、不入 `Balance`**。因此天然形成双池：

- **消费池**（`Balance`）：充值 / 兑换码入 → 消费出；**不可提现**（充值款按消费抵扣，防套现洗钱）。
- **收益池**（`TotalRevenue`）：渠道商供货收入累计 → **只进不出**，提现从这里扣。

这与 New API 的支付合规方向（payment_compliance）一致，也避免「充值→提现」资金回流风险。

---

## 3. New API 可借鉴的机制（已核对源码）

| 机制 | New API 做法 | 我们采纳 |
|---|---|---|
| 兑换码表 | `model/redemption.go`：`Key char(32) uniqueIndex`、`Status(enabled/disabled/used)`、`Quota`、`ExpiredTime`（0=不过期） | ✅ 表结构照搬，`Quota`→`credits`（余额面额） |
| 兑换原子性 | `Redeem()`：事务内先查后 **条件更新** `WHERE id=? AND status=enabled`，影响行数=1 才入账 | ✅ 原样采纳（防并发双兑） |
| 订单幂等 | `model/topup.go`：`TradeNo uniqueIndex` + `UpdatePendingTopUpStatus()` 条件迁移（pending→success），重复回调返回 alreadyDone | ✅ 原样采纳 |
| 钱包上限 | `creditTopUpQuota` 原子入账并校验钱包上限（防超限入账） | ✅ 采纳，改为余额上限可配 |
| 充值配置 | `payment_setting.go`：金额档位 `[10,20,50,100,200,500]` + 折扣表 `AmountDiscount`（如 100 元 9 折） | ✅ 采纳，扩展为「档位 + 满赠」 |
| 回调地址 | `service/epay.go` GetCallbackAddress（notify_url 服务端地址） | ✅ 采纳 |
| 支付渠道 | epay / stripe / creem / waffo 等多 provider 抽象 | ✅ 采纳 provider 抽象，v1 只做 epay |
| 合规 | `payment_compliance.go` 充值协议版本 | ✅ v1 加「充值服务协议」勾选 |

---

## 4. 总体架构

```
                    ┌──────────────── web (React) ────────────────┐
                    │ 用户端: 钱包/充值/兑换码/提现                   │
                    │ 管理端: 兑换码管理/充值订单/提现审核/支付设置    │
                    └──────────────────────┬───────────────────────┘
                                           │ JWT / API Key
┌──────────────────────────────────────────▼──────────────────────────────────────┐
│ handlers 层 (internal/server/handlers)                                          │
│   wallet.go   recharge.go   redemption.go   withdrawal.go   paynotify(公开)     │
└───────────────┬───────────────────────────────────────────────────┬─────────────┘
                │                                                   │
┌───────────────▼───────────────┐   ┌───────────────────────────────▼─────────────┐
│ op 层 (业务/事务)              │   │ pay 层 (internal/pay, 网关抽象)             │
│  wallet: 统一入账/扣账入口      │   │  PaymentGateway 接口                        │
│  redemption: 生成/兑换         │   │   ├─ epay (v1, 易支付协议)                   │
│  recharge: 订单生命周期         │   │   ├─ stripe (v2)                            │
│  withdrawal: 申请/审核/打款     │   │   └─ alipay_f2f (v2, 支付宝当面付)           │
└───────────────┬───────────────┘   └─────────────────────────────────────────────┘
                │
┌───────────────▼────────────────────────────────────────────────────────────────┐
│ model 层: recharge_orders / redemption_batches / redemption_codes / withdrawals │
│           + 现有 users / balance_ledgers / billing_records / audits             │
└────────────────────────────────────────────────────────────────────────────────┘
```

**统一资金入口（核心约束）**：所有余额变动必须走 `op.WalletCredit` / `op.WalletDebit`，
一次事务内完成「users 表更新 + balance_ledgers 落流水」，禁止旁路直接改 `Balance`
（现有 `UserSetBalance` 改造为走该入口）。这样流水与余额恒等，对账才有意义。

---

## 5. 数据模型（DDL 草案）

金额规范：**订单/提现金额一律整数「分」（`amount_cents BIGINT`）**，杜绝浮点；
余额继续 decimal(18,6)（与现状兼容，计价单位同现有余额，见 §11 决策点 D2）。

### 5.1 recharge_orders 充值订单

```sql
CREATE TABLE recharge_orders (
  id            BIGINT PRIMARY KEY,
  order_no      VARCHAR(32) NOT NULL UNIQUE,   -- 业务单号 yyyymmddHHMMSS+rand
  user_id       BIGINT NOT NULL INDEX,
  provider      VARCHAR(16) NOT NULL,          -- epay | stripe | alipay_f2f
  method        VARCHAR(16) NOT NULL,          -- alipay | wechat | qq | card (展示用)
  amount_cents  BIGINT  NOT NULL,              -- 实付法币（分）
  currency      VARCHAR(8) NOT NULL DEFAULT 'CNY',
  credits       DECIMAL(18,6) NOT NULL,        -- 入账余额（不含赠送）
  bonus         DECIMAL(18,6) NOT NULL DEFAULT 0, -- 满赠送的余额
  subject       VARCHAR(128) NOT NULL,         -- 商品名（支付页展示）
  status        VARCHAR(16) NOT NULL,          -- pending|paid|failed|expired|refunded
  trade_no      VARCHAR(64) UNIQUE,            -- 第三方流水号（幂等键）
  fail_reason   VARCHAR(255),
  notify_raw    TEXT,                          -- 回调原文（审计/纠纷取证）
  created_at    TIMESTAMP NOT NULL,
  expired_at    TIMESTAMP NOT NULL,            -- pending 超时关闭
  paid_at       TIMESTAMP
);
```

### 5.2 redemption_batches / redemption_codes 兑换码

```sql
CREATE TABLE redemption_batches (
  id            BIGINT PRIMARY KEY,
  name          VARCHAR(64) NOT NULL,          -- 批次名（如「双十一大促」）
  credits       DECIMAL(18,6) NOT NULL,        -- 每张面额
  count         INT NOT NULL,                  -- 张数
  prefix        VARCHAR(8),                    -- 码前缀（如 D11）
  per_user_limit INT NOT NULL DEFAULT 0,       -- 单用户限用张数, 0=不限
  expires_at    TIMESTAMP,                     -- NULL=不过期
  created_by    BIGINT NOT NULL,
  created_at    TIMESTAMP NOT NULL
);

CREATE TABLE redemption_codes (
  id         BIGINT PRIMARY KEY,
  batch_id   BIGINT NOT NULL INDEX,
  code       VARCHAR(32) NOT NULL UNIQUE,      -- 20~24 位 Crockford Base32（去 0/O/1/I 易混淆字符）
  credits    DECIMAL(18,6) NOT NULL,
  status     VARCHAR(12) NOT NULL,             -- enabled | used | disabled | expired
  used_by    BIGINT INDEX,
  used_at    TIMESTAMP,
  created_at TIMESTAMP NOT NULL
);
```

### 5.3 withdrawals 提现

```sql
CREATE TABLE withdrawals (
  id           BIGINT PRIMARY KEY,
  user_id      BIGINT NOT NULL INDEX,
  amount       DECIMAL(18,6) NOT NULL,         -- 申请金额（收益池口径）
  fee          DECIMAL(18,6) NOT NULL,         -- 手续费 = max(最低费, 金额×费率)
  actual       DECIMAL(18,6) NOT NULL,         -- 实付 = amount - fee
  method       VARCHAR(16) NOT NULL,           -- alipay | wechat | bank
  account_name VARCHAR(64) NOT NULL,
  account_no   VARCHAR(255) NOT NULL,          -- 加密存储（复用 secretKeys 机制）
  status       VARCHAR(12) NOT NULL,           -- pending|approved|paid|rejected|cancelled
  review_note  VARCHAR(255),
  reviewed_by  BIGINT,
  reviewed_at  TIMESTAMP,
  paid_ref     VARCHAR(128),                   -- 打款流水号/凭证
  paid_at      TIMESTAMP,
  applied_at   TIMESTAMP NOT NULL
);
```

### 5.4 users 表新增字段

```sql
ALTER TABLE users ADD COLUMN total_withdrawn DECIMAL(18,6) NOT NULL DEFAULT 0;  -- 累计已提现
ALTER TABLE users ADD COLUMN withdraw_frozen DECIMAL(18,6) NOT NULL DEFAULT 0;  -- 提现中冻结
```

**可提余额** = `TotalRevenue - TotalWithdrawn - WithdrawFrozen`
（申请时 `withdraw_frozen += amount`；打款时 `withdraw_frozen -= amount; total_withdrawn += amount`；
拒绝/取消时 `withdraw_frozen -= amount`。全程 SQL 条件更新防负数。）

---

## 6. 余额与流水设计

### 6.1 流水 kind 扩展（balance_ledgers）

| kind | 方向 | 说明 |
|---|---|---|
| `topup` | + | 支付充值入账 |
| `redemption` | + | 兑换码入账 |
| `consume` | − | 请求消费（现有结算） |
| `reserve` / `release` / `settle` | ± | 现有预扣三态 |
| `admin_adjust` | ± | 管理员调账（现有） |
| `withdraw_freeze` / `withdraw_unfreeze` / `withdraw_paid` | 收益池 | 提现三态（记在收益侧，不动 Balance） |

每条流水带 `ref_type + ref_id`（指向订单/兑换码/提现单），实现「任一金额变动可追到单据」。

### 6.2 入账/扣账语义

- **充值/兑换入账**：`Balance += credits+bonus`，`TotalRecharged += credits`（现成字段），
  同事务写 ledger；入账前校验余额上限（借鉴 New API `creditTopUpQuota`）。
- **消费**：不变（预扣 → 结算 → 释放，走现有 `BillingSettle`）。
- **提现**：不动 `Balance`，只在收益池三字段间迁移。

---

## 7. 支付网关抽象（internal/pay）

```go
type PaymentGateway interface {
    Name() string
    // CreatePayment 创建支付单, 返回收银台 URL / 二维码内容
    CreatePayment(ctx context.Context, o *model.RechargeOrder) (payURL, qrCode string, err error)
    // VerifyCallback 验签并解析回调, 返回第三方流水号与实付金额（分）
    VerifyCallback(ctx context.Context, r *http.Request) (tradeNo, orderNo string, amountCents int64, err error)
    // QueryOrder 主动查单（补单用）
    QueryOrder(ctx context.Context, orderNo string) (status, tradeNo string, amountCents int64, err error)
}
```

### 7.1 易支付（Epay 协议，v1 首发）

- **下单**：POST 到商户网关，参数 `pid, type, out_trade_no, notify_url, return_url, name, money`，
  `sign = MD5(sorted(key=value&…)+商户密钥)`，`sign_type=MD5`。
- **回调** `POST /api/v1/pay/notify/epay`（无 JWT，公开端点）：
  1. 重算 sign 比对（按协议规则排序拼接 + 密钥）；
  2. 只认 `trade_status = TRADE_SUCCESS`；
  3. **金额校验**：回调 `money` 必须等于订单 `amount_cents/100`（不一致直接拒绝并告警）；
  4. 条件更新 `recharge_orders SET status='paid', trade_no=?, paid_at=now WHERE order_no=? AND status='pending'`；
     影响行数=0 → 已处理过，直接应答 `success`（幂等）；
  5. 成功后同一事务 `op.WalletCredit` 入账 + 写 ledger + 审计 `recharge.paid`；
  6. 应答纯文本 `success`。
- **安全**：回调 IP 白名单可配；notify 原文存 `notify_raw`；限流（单 IP 每分钟上限）。
- **补单**：定时任务每 5 分钟扫 `pending` 且 `expired_at < now-2min` 的订单主动 QueryOrder；
  管理端保留「手动补单」按钮（`admin/recharge-orders/:order_no/complete`）。

### 7.2 Stripe（v2）

Checkout Session + Webhook（`checkout.session.completed`），用 `Stripe-Signature` + whsec 验签；
金额以最小货币单位（分）比对；其余幂等逻辑与 epay 完全一致（共用订单表与入账入口）。

### 7.3 充值配置（settings 新增键）

| 键 | 说明 | 默认 |
|---|---|---|
| `payment_providers` | 启用的支付方式 | `["epay"]` |
| `payment_epay_url` / `payment_epay_pid` | 商户网关/ID | - |
| `payment_epay_key` | 商户密钥（**secretKeys 加密**） | - |
| `recharge_amount_options` | 档位（元） | `[10,20,50,100,200,500]` |
| `recharge_bonus_rules` | 满赠规则（如满100送10） | 空 |
| `recharge_rate` | 汇率: 1 美元折合多少元人民币 | `7.0` |
| `recharge_bonus_enabled` | 满赠开关(D6) | `false` |
| `recharge_min_amount` / `recharge_max_amount` | 单笔下限/上限（分） | 100 / 1000000 |
| `recharge_order_expire_minutes` | 订单超时 | 30 |

---

## 8. 兑换码设计

**生成**（管理员）：批次 + 面额 + 张数 + 前缀 + 有效期 → 批量插入（单事务分批 500 条）→
支持导出 CSV（明文码，仅生成时可导出；列表只显示前 8 位掩码）。

**兑换**（用户）`POST /api/v1/redemptions/redeem`：
1. 输入校验 + **防爆破**：同一用户/IP 连续错 5 次锁 10 分钟；每日兑换次数上限（`redemption_daily_limit`）；
2. 事务内：`UPDATE redemption_codes SET status='used', used_by=?, used_at=now
   WHERE code=? AND status='enabled' AND (expires_at IS NULL OR expires_at > now)`；
3. 影响行数=1 → `op.WalletCredit(kind=redemption)` 入账；=0 → 查原因返回（已用/已禁用/已过期/不存在）；
4. 批次 `per_user_limit` 校验（该用户在本批已用张数）；
5. 审计 `redemption.redeem`，返回「面额 + 兑换后余额」。

**管理**：批次列表、码列表（按状态/批次筛选）、单张/批量禁用、过期清理任务（借鉴
New API `DeleteInvalidRedemptions`）。

---

## 9. 提现设计

**来源**：仅渠道商收益池（`TotalRevenue` 口径）；**普通用户余额不可提现**（见 D4）。

**流程**
```
申请(pending) ──审核通过──> approved ──打款──> paid
     │                        │
     ├─用户取消──> cancelled   └─(打款失败可回滚 approved)
     └─管理员拒绝──> rejected（解冻）
```

**规则**（全部 settings 可配）
- 最低提现额 `withdrawal_min_amount`（如 50）；
- 手续费 `fee = max(withdrawal_fee_min, amount × withdrawal_fee_rate)`（如 1%、最低 1）；
- 冷却期 `withdrawal_cooldown_days`：收益产生后 N 天才可提（防套利/刷单后跑路）；
- 单日提现笔数/总额上限；单笔大额（超阈值）强制人工审核（v1 全部人工审核）；
- 提现账户绑定：首次提交后锁定 N 天不可改（防盗号改卡）；
- 申请时 `withdraw_frozen += amount`（条件更新：可提余额充足才成功）。

**打款**：v1 人工（管理员按 `account_no` 转账 → 填 `paid_ref` 凭证 → 标记 `paid`）；
v2 对接支付宝转账/微信商家转账 API 自动打款。

**审计**：`withdrawal.apply` / `withdrawal.review` / `withdrawal.paid` / `withdrawal.cancel`。

---

## 10. API 设计（REST，沿用现有路由/鉴权/审计风格）

### 用户端
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/wallet` | 摘要：可用余额/冻结/累计消费/累计收益/可提余额/充值记录数 |
| POST | `/api/v1/recharge/orders` | 创建订单 `{amount_cents, provider, method}` → `{order_no, pay_url, qr_code, expire_at}` |
| GET | `/api/v1/recharge/orders` | 我的订单（分页） |
| POST | `/api/v1/recharge/orders/:order_no/query` | 主动查单（前端轮询补单） |
| POST | `/api/v1/redemptions/redeem` | `{code}` → `{credits, balance}` |
| GET | `/api/v1/redemptions/history` | 我的兑换记录 |
| POST | `/api/v1/withdrawals` | 申请提现 `{amount, method, account_name, account_no}` |
| GET | `/api/v1/withdrawals` | 我的提现记录 |
| POST | `/api/v1/withdrawals/:id/cancel` | 取消（仅 pending） |

### 公开（无 JWT，验签）
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/pay/notify/:provider` | 支付回调（幂等 + 验签 + 金额校验） |
| GET | `/api/v1/pay/return/:provider` | 支付完成回跳（仅展示，不改状态） |

### 管理端（admin）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/recharge-orders` | 订单列表（按用户/状态/时间筛） |
| POST | `/api/v1/admin/recharge-orders/:order_no/complete` | 手动补单（二次确认） |
| POST | `/api/v1/admin/redemption-batches` | 生成批次（面额/张数/前缀/有效期） |
| GET | `/api/v1/admin/redemption-batches` / `/codes` | 批次/码列表 |
| POST | `/api/v1/admin/redemption-codes/:id/disable` | 禁用单张 / 批量禁用 |
| GET | `/api/v1/admin/withdrawals` | 提现队列 |
| POST | `/api/v1/admin/withdrawals/:id/review` | `{approve, note}` |
| POST | `/api/v1/admin/withdrawals/:id/mark-paid` | `{paid_ref}` 标记已打款 |

设置项走现有 `/api/v1/setting/set`（新增键见 §7.3 / §9）。

---

## 11. 决策记录（2026-10-14 已拍板）

| # | 决策 | 结论 |
|---|---|---|
| **D1** | 首发支付渠道 | **易支付**；但支付网关接口按多币种/多渠道设计，**预留外币支付接口**（Stripe 等后续即插即用） |
| **D2** | 余额计价与汇率 | 余额币种维持**美元**；人民币按 **1 USD = 7 CNY** 折算（`recharge_rate` 默认 7.0 可配）；**预留汇率采集接口**（`RateProvider`），支撑后续多币种充值扩展 |
| **D3** | 提现打款 | **先人工**打款+凭证，后接自动转账 API |
| **D4** | 普通用户提现 | **不允许**；仅渠道商收益可提现 |
| **D5** | 兑换码形态 | **用户兑换张数不限**：`per_user_limit` 默认 0＝不限，任何用户可连续兑换多张；保留批次级限额能力供特定活动配置 |
| **D6** | 充值满赠 | 做成**配置开关**（`recharge_bonus_enabled` + `recharge_bonus_rules`），默认关 |

### 11.1 汇率与多币种（D2 落地）

```go
// RateProvider 汇率来源; v1 只有静态配置实现, 后续可接央行/支付渠道牌价。
type RateProvider interface {
    // Rate 返回 1 单位余额币种(USD) 折合多少 target 币种(如 CNY=7.0)。
    Rate(ctx context.Context, target string) (float64, error)
}
```

- v1 实现 `StaticRateProvider`：读设置 `recharge_rate`（默认 7.0，含义「1 美元 = 7 元人民币」）。
- 订单表 `currency` + `amount_cents`（该币种最小单位）从一开始就带着，换算式：
  `credits = amount_cents / 100 / rate`，网关回调校验金额时同样按币种最小单位比对。
- 多币种充值 = 后续换 `RemoteRateProvider` + 订单允许 `currency ∈ {CNY, USD, ...}`，无需改表。
- 外币支付（D1 预留）：`PaymentGateway.CreatePayment` / `VerifyCallback` 已带
  `amountCents int64 + currency string`，Stripe 直接映射最小货币单位。

### 11.2 兑换张数不限（D5 落地）

- **默认口径：用户兑换张数不限**（`per_user_limit = 0`），同一用户可连续兑换任意多张。
- 保留批次级 `per_user_limit` 作为活动配置能力：N>0 时兑换事务内按 `used_by = ? AND batch_id = ?` 统计已兑张数，超限拒绝。
- 防爆破只针对「猜码」：连续 5 次兑换**不存在**的码，按用户与来源 IP 双维度锁 10 分钟；已兑/已禁用/已过期是已知码状态变化，不计入。
- 若后续需要「绑定指定用户才能兑换」，再给 `redemption_codes` 加 `bound_user_id` 约束即可（预留字段位）。

---

## 12. 分期实施与验收

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **P1 钱包底座 + 兑换码** | `op.WalletCredit/Debit` 统一入口、ledger kind 扩展、兑换码生成/兑换/管理、用户端钱包页 + 兑换码卡、管理端兑换码页 | 兑换入账后余额/流水/累计充值三者恒等；并发双兑只成功一次；错码 5 次锁 10 分钟 |
| **P2 支付 + 充值** | pay 网关抽象、epay 网关、recharge_orders、回调/补单/超时、充值弹窗、管理端订单页、支付设置 | 回调重复投递只入账一次；金额不符拒绝并告警；超时订单自动关闭；补单可手动 |
| **P3 提现** | withdrawals 全流程、收益池三字段、审核/打款、提现页 + 审核队列、对账扩展 | 可提余额恒等式成立；拒绝/取消全额解冻；同单不可重复打款 |
| **P4 增强** | Stripe/支付宝当面付、满赠活动、自动打款、提现风控增强 | - |

每阶段完成即走现有发布流程（版本号 + CHANGELOG + 构建 + 部署 + 验证），
并用上次的多视口 UI 检查脚本回归新页面/新弹窗的响应式表现。

---

## 13. 风险与合规

1. **资金安全**：入账只走统一入口 + DB 事务；回调幂等靠唯一索引 + 条件更新；金额三重校验（下单额=回调额=入账额）。
2. **套现洗钱**：余额不可提现（D4）+ 提现冷却期 + 大额人工审核 + 账户绑定锁定。
3. **密钥泄露**：支付密钥走现有 secretKeys 加密存储，前端只读脱缀；回调原文仅管理员可见。
4. **并发**：兑换/入账/提现冻结全部 SQL 条件更新（`WHERE` 带余额/状态约束），不依赖应用层锁。
5. **合规**：充值服务协议勾选（借鉴 New API compliance terms 版本化）；未成年人/退款政策预留。
6. **数据**：`notify_raw` / `paid_ref` 保留取证信息；审计动作全覆盖。
