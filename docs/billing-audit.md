# 计费审计: 用户余额未按调用费用扣除

## 现象
生产 360 条调用(约 42M token, 计费口径约 9.66)已产生, 但 `billing_records` 为空、 三个账号余额分文未动。

## 根因

1. **admin 免计费设计**: 生产全部流量走 admin(klarns) 的 API Key, 而旧逻辑是「API Key 归属者为 admin 则不预扣不结算」。 360 条调用全部命中豁免, 余额自然不动。
2. 历史遗留: archus(普通用户) 曾于 10-07 发起 6 笔调用, 预扣生成后走了释放而非结算(旧构建行为, 对应请求行已随 ID 方案演进丢失), 表现同样是「没扣钱」。

## 验证
`octopus-e2e-billing.py` 端到端实证当前构建的结算链路本身是正确的:

- 普通用户调用一次(prompt=1000/output=500, 供货价 in=1/out=2, 上浮 0): 余额 1.000 → 0.998, 精确扣 0.002;
- 冻结归零、 `total_spent` 同步、 `billing_records` 一条 `settled`(`price_source=channel_model`)、 发布者收入 0.002、 预扣记录已结算。

## 修复(2026-10-08)

- **取消 admin 豁免, 全量计费**: 所有调用方(含 admin)一律按实际用量扣减余额。 admin 通常同时是渠道归属者, 扣掉的余额会以 `total_revenue` 回流, 净额不变但账目与统计口径终于一致。
- **结算不再静默吞错**: `BillingSettle`/`BillingRelease` 失败改为记日志 + `RaiseAlert("billing_failed")`(经 Webhook 外发), 预扣超时回滚兜底仍在。
- 对账联动: 以往 admin 流量「有用量无计费明细」会被对账记成 missing 差异; 全量计费后该误报消失。

## 回归
`octopus-e2e-billing.py` 25/25(修复后): 普通用户与 admin 双双精确扣 0.002/次, 计费明细 2 条, 合计 0.004。

## 行为变更提示
若仍需 admin 免计费(例如不希望运营自测计入账目), 恢复 `internal/relay/handler.go` 中按 `RoleAdmin` 跳过 `reserveBalance` 的分支即可。
