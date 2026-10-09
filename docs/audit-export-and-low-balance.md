# 审计导出与低余额告警

2026-10-09 落地的两件事: 让"谁能看审计"和"余额快没了谁知道"都留下可追溯的出口。

## 1. 审计日志 CSV 导出

- 端点 `GET /api/v1/audit/export`(仅管理员), 与 `/audit/list` 共用 `auditFilterFromQuery`,
  筛选语义(keyword/action/operator/from/to)天然一致 —— 抽成一个函数是刻意的: 两份实现迟早漂移。
- 单次上限 5000; 命中上限时在文件末尾追加 `# truncated: exported X of Y rows`。
  导出是留痕动作, 少给一行必须自己说出来, 不能让"以为是全量"的错觉成立。
- **导出行为本身也记审计**(`audit.export`, detail 含 `n=导出/总数` 与生效筛选)。
  否则"谁把审计日志带走了"恰好是审计自身的盲区。
- 前端: 审计页工具栏导出按钮, 下载参数用**当前草稿筛选**(而非已生效筛选),
  避免"看着是这个条件、导出来是另一个条件"。

## 2. 低余额告警(`low_balance`)

`min_balance` 此前只在 `BillingReserve` 里当拒绝阈值用 —— 用户唯一能感知的方式是调用报错。
新增 kind `low_balance`(channel_id=0, channel_code=system), 两个触发点:

1. **结算后提前预警**: 按本笔预估测算下一单是否够付;
2. **预扣被拒时兜底**: 覆盖"账号本来就不够、一次都没结算过"的情形。

### 关键细节

- 真实门槛是 `min_balance + estimate`, **不是** `min_balance`。
  预扣阶段已保证 `available >= min_balance + estimate`, 若按 `min_balance` 判断恒不触发 —— 判错等于永不告警。
- 预扣失败改用哨兵错误 `errInsufficientBalance`: 只有"余额不足"才发告警,
  其他落库错误不该伪装成余额触底, 否则告警被噪音淹没。
- **reason 不含余额数字**: 去重按 `kind+reason` 匹配, 余额每结算一次就变一次,
  混进去等于绕过去重变成骚扰推送。reason 只含用户名/ID与两个阈值。

## 3. 验证

`octopus-e2e-billing.py` 新增 17 条断言(总计 123): 导出 200/表头/行数/筛选命中/越权 403/自审计;
低余额场景(余额 0.013, estimate 0.012288)验证结算侧提前预警 + 下一单 400 + 预扣侧兜底各报一次。
