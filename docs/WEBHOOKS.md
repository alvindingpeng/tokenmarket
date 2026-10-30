# Webhook 出站通知（P0-5）

状态：开发中，尚未上线。最终接口和验证结果交付时更新。

## 范围与安全决策

- 仅订阅当前用户的站内通知，不广播其他用户事件。
- 本期支持通用 JSON + HMAC；钉钉、飞书、Slack 专有报文模板另行实施。
- 仅公网 HTTPS 443，禁止 userinfo/fragment、环境代理、重定向。保存时校验与实际拨号均阻断私网、环回、链路本地和元数据地址；拨号按已校验 IP 建连以防 DNS rebinding。
- 创建提供 16–128 字节密钥；编辑留空保留。使用现有 secretstore 加密并绑定归属，API 仅返回 has_secret。
- 不保存或回显外部响应正文，不在日志中输出 URL、密钥、原始网络错误。URL 可能含 token。

## 可靠性

正常情况下通知落库与待投递入队同事务；队列容量耗尽时需保留站内通知并跳过外发（最终实现核对），不能让外部订阅拖垮站内告警。数据库持久队列、批量有界 worker 与原子 lease，网络 I/O 不占业务事务。单请求 5 秒超时；网络错误、429、5xx 有界退避重试，其他非 2xx 终止。

交付语义为**至少一次**。稳定 delivery ID 供接收端持久去重，不承诺恰好一次。编辑、禁用或删除后取消旧配置投递；已经开始的网络请求无法撤回。注销或停用账号不再继续投递。

每用户最多 10 个订阅，测试发送每订阅至少间隔 60 秒。队列与终态记录设置容量限制和定期清理，终态记录计划保留 7 天；worker 每 5 秒处理有界批次，重试从 30 秒开始、单次退避最多 10 分钟；最终容量与索引交付时核对。

## API 契约

所有接口沿用 Bearer JWT 鉴权与现有 code/message/data 响应封装。跨用户资源返回 404，忽略客户端 user_id/role。

| 方法 | 路径 | data |
| --- | --- | --- |
| POST | /api/v1/webhook | 新建订阅 |
| GET | /api/v1/webhook/list | items/total/page/page_size |
| PUT | /api/v1/webhook/:id | 更新后的订阅 |
| DELETE | /api/v1/webhook/:id | deleted |
| POST | /api/v1/webhook/:id/test | delivery_id/status=pending |
| GET | /api/v1/webhook/:id/logs | items/total/page/page_size |

创建请求为 url/events/secret/enabled/retry_count；enabled 默认 true，retry_count 默认 2（0–3 次重试，不含首投）。更新为部分更新。事件采用现有通知类型：system、balance_low、ratelimit_hit、channel_down、channel_review、withdrawal、dispute、recharge、ticket；测试投递为 test。早期路线图中的 withdrawal_approved/ticket_reply 不作为本期事件名。

订阅视图只含 id/url/events/enabled/retry_count/has_secret/created_at/updated_at/last_status；last_status 从未投递时为 none。日志含 delivery_id/event/status/attempts/http_status/error/created_at/updated_at，不含 payload/URL/密钥/响应正文。http_status=0 表示尚未收到 HTTP 响应。

分页默认 page=1、page_size=20，上限 100。配额超限 409，测试过于频繁 429。测试发送仅入队，需在日志查看远端结果。

## 接收协议

POST Content-Type: application/json。请求头：

- X-Switchyard-Delivery-ID：稳定 ID
- X-Switchyard-Timestamp：签名时间戳 Unix 秒
- X-Switchyard-Signature：sha256=<hex>

HMAC-SHA256 签名消息为 timestamp + 点号 + 原始请求 body。Payload 包含 id、event、timestamp、user_id、data；data 取自用户通知的 title/content/link/related_id，不含 API Key 或转发请求正文。接收端 2xx 表示成功；测试按钮仅表示已入队。

## 接收端验签参考

原始 body 不可先解析再序列化；secret 应从服务端安全配置读取。

```python
import hashlib
import hmac
import time

def verify(secret: bytes, timestamp: str, signature: str, raw_body: bytes) -> bool:
    try:
        sent_at = int(timestamp)
    except (TypeError, ValueError):
        return False
    if abs(time.time() - sent_at) > 300:
        return False
    signed = timestamp.encode('ascii') + b'.' + raw_body
    expected = 'sha256=' + hmac.new(secret, signed, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature)
```

验签后，将 delivery ID 与业务处理在接收端同一事务内记账；重复 ID 返回 2xx，不重复执行。时间窗校验不代替持久去重；长任务先持久化事件再返回 2xx。

## 备份与运维

全库 SQLite 备份涵盖订阅与队列，恢复密文需原实例主密钥文件。现有 JSON 路由配置导出不是用户通知/工单/订阅的全量备份，本期不扩大格式。

生产验收脚本 `scripts/verify-webhooks-production.sh` 只检查认证闸门、迁移与行数，不创建订阅或外发事件。

## 验证门槛

- CRUD、日志和测试发送认证及跨用户越权测试。
- SSRF、拨号重解析、代理、重定向、超时和脱敏。
- 密钥静态加密与归属绑定、签名复算、重试上限和稳定 ID。
- 持久恢复、并发 lease、编辑禁用删除取消旧投递、通知去重集成。
- 前端 API 路径、取消信号、身份缓存隔离、三语、loading/error/empty。
- Go normal/jsoniter/vet 与前端 test/lint/build 通过后推送部署。未经授权不在生产创建实际外部投递。
