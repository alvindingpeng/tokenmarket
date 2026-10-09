# 计费明细与余额流水

## 一、计费明细(消费明细)

管理后台 > 计费 > 消费明细, 支持筛选 + 分页 + CSV 导出; 数据来自 `billing_records`, 一行 = 一次已结算调用。

### 筛选维度

| 参数 | 含义 |
|---|---|
| `model` | 模型模糊匹配(客户端模型名 `group_model` 或上游落地模型名 `model_name`) |
| `channel` | 发布编码 `share_code` 模糊匹配 |
| `status` | `settled` / `refunded` |
| `from` / `to` | 结算时间范围(RFC3339, 含端点) |
| `user_id` / `owner_id` / `api_key_id` | 仅管理员有效 |
| `limit` / `offset` | 分页, 列表上限 500, 默认 20 |

### 可见性铁律

非管理员无论传什么参数都只看自己(`user_id` 被强制改写为本人的主键); 管理员可查全量或指定用户。 列表、导出走同一套条件构造, 不存在"列表看不到但能导出来"的旁路。

### CSV 导出

`GET /api/v1/stats/billing/records/export` — 单次上限 5000 条, 超出在文件尾以 `# truncated` 注释行提示; 导出行为进审计(`billing.export`)。 列含金额三方分解(`user_cost` / `owner_revenue` / `platform_revenue`)与价格快照(`user_price_*` / `supply_price_*` / `price_source`), 可直接对账。

### 界面

明细行右侧展示**结算时单价**(上浮后的用户价, 每 1M token), 使"这笔钱怎么算出来的"可当场复核; 改价不影响历史行, 因为明细存的是当时的快照。

## 二、余额流水

`GET /api/v1/stats/balance/ledger`(普通用户看自己, 管理员可加 `user_id`) + `?kind=` 过滤; 表 `balance_ledgers`。

### 变动口径

| kind | 触发点 | Amount(可用净变动) | Frozen(冻结变动) | Cost |
|---|---|---|---|---|
| `reserve` | 请求开始预扣 | `-estimate` | `+estimate` | 0 |
| `settle` | 请求结束结算 | `+estimate - cost` | `-estimate` | 实际费用 |
| `release` | 无用量/失败驳回 | `+estimate` | `-estimate` | 0 |
| `adjust` | 管理员改余额 | 差值 | 0 | 0 |

不变量(可随时用 SQL 校验):

- 同一请求的 `reserve.Frozen + settle.Frozen = 0`;
- `settle.Amount = reserve.Frozen + settle.Frozen... ` 即 `settle.Amount = estimate - cost`,`cost` 与计费明细的 `user_cost` 相等;
- `adjust.Amount = balance_after - 上一次余额`, 且 `actor_id` 记录操作者;
- 流水与余额在**同一事务**内写入, 不存在余额动了而流水没记的情况。

### 历史回填

迁移 `016` 用存量 `balance_reservations` + `billing_records` 回填历史流水(标注 `note: 历史回填`), 让升级后用户的流水页不留空洞。 历史行的 `balance_after` 无法重建, 记为 0 并在界面显示为 `—`; 回填幂等(表中已有流水则整体跳过)。

## 三、验证

`octopus-e2e-billing.py` 69/69: 明细分页/模型筛选/负例/状态/时间范围、CSV 头与行数、流水预扣-结算成对且 `balance_after` 链正确、管理员调账差值 4.002 与操作者、普通用户 `user_id` 越权被忽略。
