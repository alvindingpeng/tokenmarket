# 更新说明

## v1.10.13 — 2026-11-17

**修复: 上游 429 后的重试不退避, 把"偶发限流"放大成"整段请求必然失败"**

订阅模型调用时用户看到：

```
failed to stream request: Request failed: Too Many Requests, error:
Resource has been exhausted (e.g. check quota)., code: 429, type: RESOURCE_EXHAUSTED
```

而 `retrieveUserQuota` 显示 17 个 bucket 全部 `remainingFraction=1`（额度满，重置在下周）——
额度没耗尽，是上游的速率窗口被打满。

**根因**：`ErrorClassRate` 分支原本是**裸 `continue`** —— 上游刚用 429 说"本窗口已满"，
网关立即重选同一成员接着打。单账号订阅渠道只有一个候选，摘掉探测名额后下一轮又选它，
于是 `max_total_attempts=10` 在约 **2 秒内**全部砸向已满的窗口。线上实测各轮间隔：

```
22:45:42 limited dur=269ms
22:45:43 limited dur=215ms   ← 2 秒内 10 次, 每条都是真实上游 429
22:45:43 limited dur=217ms
22:45:44 limited dur=270ms   ... 第 10 轮后 max_total_attempts 耗尽
```

同一账号、同一模型、同一 payload 的对照实测：

| 节奏 | 结果 |
| --- | --- |
| 间隔 8 秒，串行 10 次 | **10/10 成功**（1.0–1.4s） |
| 间隔 2 秒，12 次 | **12/12 成功** |
| 间隔 0.6 秒，12 次 | **12/12 成功** |
| 网关 10 轮内部重试（≈2 秒内） | **10/10 全部 429** |

上游本身在正常节奏下完全可用 —— 是网关自己的重试风暴把它打挂了。

**修复**（`internal/relay/handler.go`）：429 之后**退避再重试**，且这类重试**不吃** `MaxTotalAttempts`：

1. 退避时长 = `member_retry_interval_seconds` × 1/2/4/8（指数递增，封顶 8 倍）；
2. 单请求最多 3 次 429 退避重试，超过则透传上游 429；
3. 剩余 deadline 不足以支付退避时**立刻**返回，不把用户挂到超时；
4. 预算检查为"退避换来的那一轮"放行（`pendingRateRetry`）—— 否则紧预算（实测可到 1）下
   一次瞬时 429 会在退避前就被判死。

不计入总预算的理由：429 说明请求根本没被模型处理（与准入拒绝同性质）。若照计，退避反而
更快撞到 `max total attempts exceeded`，比不修更差。真正打到模型并失败的轮次照旧计。

**验证**：新增 6 条测试，其中 2 条端到端（httptest 上游返回真实 429 报文）证明
"两次 429 后退避 3.2s 成功"与"持续 429 时 7.2s 快速失败"。变异验证：把退避改成 0 后
端到端测试捕获到"总耗时仅 16.5ms"——正是原始缺陷的特征。
