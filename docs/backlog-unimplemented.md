# 未实施功能清单(持久化待办)

> 用途: 记录**有设计稿但未实施**、**已交付但未启用**、**已知遗留债**三类事项, 防止跨会话丢失。
> 建立于 2026-10-21, 依据是对 `docs/` 全部设计稿与代码事实的逐项核对(不以文档自述为准)。
> 与 `docs/roadmap.md` 分工: roadmap 记已完成, 本文记**还没做的**。

## 0. 2026-10-21 状态核对结论

核对方式: 对每篇设计稿的关键字段 / 路由 / 任务 / 设置键在代码里 grep 求证。

### 已就地更正的过期描述

| 文档 | 原文 | 实际 |
|---|---|---|
| `roadmap.md` P3 充值 | 「未开始, 后端无任何实现」 | 早已全线实现: `handlers/recharge.go`(8 路由) / `wallet.go` / `redemption.go`(6 路由) / `withdrawal.go`(7 路由); `internal/pay` 四渠道 epay/alipay/stripe/creem; 前端 `wallet/` `redemption/` `withdrawals/` 三模块齐全 |
| `design-recharge-redemption-withdrawal.md` 头部 | 「待评审, 尚未实现」 | P1 钱包底座+兑换码 / P2 支付+充值 / P3 提现 全部已上线 |
| `design-channel-review-observation-quality.md` 头部 | 「设计稿, 未实施」 | P1 审核已完成; P2/P3 于 2026-10-21 开发 |

前端遗留过期文案: `web/src/locales/zh_hans.json` 的「充值功能后续上线」。

---

## 1. 有设计稿, 未实施

### 1.1 多区域高可用 / 应用多活 — 整篇未落地
设计稿: `design-multi-region-ha.md`、`design-app-multi-active.md`

三个**阻碍多实例**的核心改造一个都没做:

| 障碍 | 位置 | 现状 |
|---|---|---|
| 统计内存累计 + 全量快照覆盖写 | `op/stats.go` | 仍是「缓存值即权威累计, 覆盖写」 |
| 限流内存桶 | `internal/ratelimit` | 进程内 map, N 实例 = 额度 × N |
| 后台任务进程内 ticker | `internal/task` | 无库级租约选主, 多实例会重复跑 |

配套 PG + Patroni + HAProxy 主备集群未搭建(`internal/db` 支持 postgres 方言, 线上仍跑 SQLite)。

### 1.2 全球加速 backlog
设计稿: `backlog-global-acceleration.md`

| 编号 | 事项 | 卡在哪 |
|---|---|---|
| B1 | Cloudflare 边缘证书未签发 → G2 边缘无法启用 | **纯外部依赖**: 需 CF 后台操作 |
| B5 | G2 验收指标未测 | 依赖 B1 |
| C1 | G3 出口池线上零配置(`channel_egresses` 0 行) | 缺真实第二线路 |
| C2 | 区域亲和默认关闭(`egress_region_bonus_ms=0`) | 依赖 B1(需 `CF-IPCountry`) |
| C5 | 出口健康看板(出口成功率/冷却未暴露接口) | 待开发 |
| D1 | G0b MaxMind mmdb + `client_asn` | 主动延后 |
| E | G4 多区域数据面 | 未开始, 前置是 §1.1 |

### 1.3 充值/提现明确不做的子项
设计稿本身写着不做, 不是遗漏:

| 事项 | 说明 |
|---|---|
| 充值订单退款流程 | `RechargeStatusRefunded` 常量已定义, 全库无任何退款动作实现 |
| 发票 | 无任何实现 |
| 提现自动打款(P4) | 注释「后续自动打款按同一字段对接」, v1 仍是人工打款 + 凭证 |
| Stripe/支付宝完整形态 | `internal/pay` 有实现, 线上未配置商户参数 |

### 1.4 渠道模型自动上架(B5)
`roadmap.md`: 「B4/B5 自动定价/自动上架(P2) 未纳入本版」。
2026-10-21 已补 **B4 自动预填供货价 + 条件过滤**(见 `docs/channel-auto-add-conditions.md`),
2026-10-22 (v0.42.0) 已补 **A3 后台周期自动添加 + 名称白名单**。
但 **B5 自动上架(listed=true) 仍不做**: 上架等于对客可见, 必须人工确认价格, 自动上架会把零价模型暴露给用户。

---

## 2. 已交付但未启用 / 零配置

| 能力 | 交付版本 | 为什么没启用 |
|---|---|---|
| G2 入口边缘接入 | v0.38.0(源站侧) | CF 边缘证书未签发(B1) |
| G3 出口池 + 按出口打分 | v0.37.0 | 线上 `channel_egresses` 0 行 |
| 区域亲和(`CF-IPCountry` 选路) | v0.37.0 | 依赖 B1 |
| `channel_insights` 延迟归因出口列 | v0.35.0 | 有数据但无出口健康视图(C5) |

---

## 3. 遗留债与已知限制

### 3.1 财务历史(需人工审批, 代码无法自动修)
- **20261008 对账 mismatch**: records=147, missing=321, 原始请求日志已过保留窗口, 证据无法重建
- **20261016 单据差异**: 已存 153 笔 / 毛额 2.27984155, 现存源账单 194 笔 / 毛额 3.278277946, 差 41 笔 / 0.998436396

两项均**未处理**, 文档明确「不得据此认定资金损失、不得猜补扣」。
处理需要: 备份 → 重算未审核单据 → 人工核对。

### 3.2 验证缺口
- 登录态页面与**真实交易全流程验收**未执行(充值与提现未用真实资金测过)
- MySQL / PostgreSQL **真实服务集成验收**未做(`financial-hardening-phase2a.md:64`)
- 备份导入拒绝测试、缓存隔离回归测试未补(`routing-safety-review-followup.md` 末段)

### 3.3 已知代码限制(设计上接受, 非 bug)
- gin `Flush()` 错误在 writer 层不可观测 → 死客户端会一直抽干上游直到空闲超时
- 流式成功响应体缓冲**无大小上限**
- 流式聚合 `result.usage == nil` 时按 0 用量结算
- 旧 `VideoJob` 记录缺 `ChannelID` → 授权删除后无法恢复(fail-closed by design)

---

## 4. 2026-10-21 本次交付

| 事项 | 文档 |
|---|---|
| 渠道观察期(P2) + 质量展示(P3) | `docs/channel-observation-quality.md` |
| 渠道模型自动添加条件(价格/倍率/形态/排除) | `docs/channel-auto-add-conditions.md` |

---

## 5. 建议优先级

| 优先级 | 事项 | 理由 |
|---|---|---|
| P1 | 财务历史两项核查 | 真实资金风险, 需人工审批与授权窗口 |
| P2 | 出口健康看板 C5 | 小改动, 但线上零出口配置, 收益暂时为零 |
| P3 | 应用多活 P3 | 改动最大, 需先有稳定期与 PG 集群 |
| 外部依赖 | CF 边缘证书 B1 | 只有运维能操作 |
