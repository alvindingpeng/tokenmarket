# 生图支持(P1)

## 范围

为 `/v1/images/generations` 与 `/v1/images/edits` 两条 OpenAI Images 协议路由提供完整的转发与按张计费:

- 协议位 `ProtocolOpenAIImage = 1<<4`, 路由、授权矩阵、出站转换全部接入。
- `/v1/images/generations`: JSON 请求体, 同步响应。
- `/v1/images/edits`: multipart 表单, 支持多图输入(多张 `image` 文件)。
- 计费: 按张计价(每张供货价 × 上浮 = 用户价), token 用量若上游返回则叠加。
- 预扣: 生图请求冻结 `每张用户价 × balance_reserve_images`(默认 4, 管理后台可改)。

P2 预留: `ProtocolOpenAIVideo = 1<<5` 位已占用, 路由未注册。

## 关键设计

### 形态跟着协议位走

渠道模型的媒体形态(`ChannelModel.Kind`, `image`/空)由授权协议位推导:

- 任一 `(模型 × 凭据)` 授权带生图位 ⇒ 该模型 `kind=image`。
- 生图位全部撤销 ⇒ 回退文本。保存渠道时在 `syncChannelModelKinds` 统一重算。

拉取模型列表时(`fetchUpstreamModels`)按名称关键词(`dall-e|gpt-image|flux|seedream|imagen|sd-*|...`)给生图模型默认勾选生图位, 管理员可在授权矩阵自由增删。

### 价格: 媒体价与 token 价并存

`ChannelModel.MediaSupply`(JSON 列, 增量迁移, 老行零值)与 `SupplyPrice`(token 四类)互不干扰:

- 生图行在改价工作台显示每张价输入, token 四类价保持原值随保存提交。
- 上架但 token 价与媒体价全 0 ⇒ 拒绝(与文本模型同一收入口径)。
- 媒体单价上限 1000 元/张, 负数拒绝。

### 转发

- 生图请求的正文改写: generations 走 `sjson` 改 `model` 字段; edits 是 multipart, 用 `rewriteMultipartModel` 重建表单并把 `model` 字段替换为上游模型名(文件部分原样复制, 多图不受影响)。
- 出站地址: `openai_image_generation_path`(默认 `/v1/images/generations`)、`openai_image_edit_path`(默认 `/v1/images/edits`), 与 chat/response/message 同风格, 渠道可单独改。
- 生图不走跨协议回退: 客户端生图请求只由带生图位的授权承接, 避免把图片请求转成聊天请求。
- 限流估算: 生图请求按 `(1,1)` token 预留(RPM/并发为主约束); 生图正文可能是数 MB 的 base64, 与 token 无关。

### 计费

- 预扣(`reserveBalance`): 分组内 image 成员的 `每张用户价 × balance_reserve_images`。
- 结算(`settleBillingLocked`): 从最终响应体 `data[]` 数出张数, `user_cost = token费用 + 每张用户价 × 张数`; `owner_revenue` 同口径按供货价; `price_source = channel_model_media`。
- `BillingRecord` 新增 `media_supply/media_user/media_units`(JSON 列), token 四列保持 0; 旧行反序列化不受影响(未做任何历史数据结构变更)。
- 统计: 媒体费用折入输出侧桶, 图表口径不变。

### 日志

- multipart 请求体入库的是摘要(字段 + 文件清单), 不含图片二进制。
- 响应体含 b64 时照常脱敏/截断策略处理。

## 管理后台

- 设置 `balance_reserve_images`(平台设置页, 1–64, 默认 4)。
- 改价工作台: 生图模型显示每张价输入与按张的用户价。
- 账单明细: 生图行显示 `img N`。

## 测试

`octopus-e2e-billing.py` 第 18 节(24 项断言):

- 预扣参数: 默认 4 / 非法值 0、100 被拒 / 改为 2 后按新值预扣。
- generations 转发 200, 按张计费(media_units、user_cost、owner_revenue、price_source)。
- n=3 计 3 张; edits 多图 multipart 透传计 1 张。
- kind 由授权位推导、listing 回显媒体价。
- 余额按实际费用扣减、冻结归零。
