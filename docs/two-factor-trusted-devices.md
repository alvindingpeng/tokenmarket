# 2FA 信任设备与恢复码自助重生成（v1.11.0）

## 为什么做这两件事

TOTP 全链路（setup / enable / disable / step-up / 登录强制二次校验 / 10 张恢复码）
在 v1.5.6 就已交付。但它有两个把用户往外推的缺口：

1. **每台设备每次登录都必须掏手机看 6 位码。** 三周之后用户会开始把恢复码拍照存相册、
   或者干脆把 2FA 关掉 —— 那是安全措施把人逼成更不安全的行为。
2. **恢复码只能在启用那一刻拿到一次，用光后无法自助重新生成。**
   而"解绑再重绑"这条唯一出路要求输入动态码 —— 恰恰是丢了设备的人做不到的事。
   这不是体验问题，是一道**死锁**：最后一张恢复码用掉之后，账号再也回不来。

## 信任设备

### 语义

勾选登录页的「信任本机」并**成功验证过动态码或恢复码**之后，当前浏览器被记 30 天。
期间这台机器登录只验密码。它免的是**第二因子**，不是密码。

- 只作用于**登录**。高危操作的 step-up（余额调整、提现审批、备份导入、敏感导出、系统设置…）
  仍然要求重新输密码 —— 那是"这次操作由本次交互明确授权"的语义，与"这台机器可信"不是一回事。
- 有效期**固定 30 天，不滑动**。每次使用只刷新 `last_used_at`（供展示），不延长 `expires_at`。
  滑动过期会让一台十年不换的机器永远免码，30 天的承诺就名存实亡。

### 三条安全口径

| 口径 | 实现 | 反向证据（改坏就红） |
|---|---|---|
| 令牌只以哈希落库 | `token_hash = sha256(32 字节随机数 hex)`；明文只在签发那一刻经 `Set-Cookie` 下发一次 | `TestTrustedDeviceIssueAndVerify`：断言行里的 `TokenHash` 既不是明文、也不包含明文 |
| 只免第二因子，不免密码 | Cookie 由 `UserTOTPLoginWithTrust` 在**密码已验过之后**才被查看 | `TestTrustedDeviceLoginBypassesSecondFactor`：无 Cookie / 伪造 Cookie 一律 401 `two_factor_required` |
| 可吊销且会被动失效 | 设置页可逐个/全部吊销；改密、重绑/解绑 2FA 时全量清除 | `TestTrustedDeviceRevokeIsScopedAndImmediate`、`TestTOTPEnableAndDisableRevokeTrustedDevices`、`TestTrustedDeviceRevokeAll` |

Cookie 属性：`HttpOnly`（任何一处 XSS 都换不到免码能力）、`Path=/`、
`Secure` 跟随实际协议（与 `auth` 会话 Cookie 同一判据：恒 true 会让内网 http 部署永远登不上，
恒 false 会让凭据在明文链路上裸奔）。

### 判据顺序（代码里是判别式，不是注释）

```
密码校验通过
  └─ 2FA 未启用 → 直接放行（与修前一致）
  └─ 2FA 已启用
      ├─ 请求带动态码      → 按码验证（**不走**信任凭据，也不刷新其 last_used_at）
      ├─ 没带码 + 信任凭据有效 → 放行，返回 usedTrustDevice=true 供调用方续期 Cookie
      └─ 没带码 + 无/无效凭据 → 消耗一次失败预算 → 401 two_factor_required
```

"没带码且没有信任凭据"仍然消耗一次失败预算，与原实现一致：否则攻击者可以用空码无限探测，
而限流恰好是这条路上唯一能拖慢在线爆破的东西。

### 接口

```
GET    /api/v1/user/2fa/trusted-devices          只读清单（**不**要求 step-up）
DELETE /api/v1/user/2fa/trusted-devices/:id      吊销单台（step-up）
DELETE /api/v1/user/2fa/trusted-devices          全部吊销（step-up）
```

只读清单不挂 step-up 是刻意的：看一眼自己有几台设备不该被弹一次密码确认。但那两个吊销是
安全写 —— 它们能砍掉"免码"旁路。删除条件同时带 `user_id`，避免只按 id 删除导致越权写。

视图里没有令牌哈希、没有明文令牌；`expired` 由**服务端**算好下发，前端不拿本地时钟跟 UTC
时间戳比（时区/时钟漂移会算反）。

## 恢复码自助重生成

```
POST /api/v1/user/2fa/recovery-codes/regenerate   { "code": "<动态码或恢复码>" }
```

门槛与解绑同档：登录态 + step-up（重输密码）+ **一个当前有效的码**。
允许用一张恢复码换新恢复码是必须的 —— 否则"最后一张恢复码"用掉之后又回到死锁。

实现口径：

- **整体替换而不是追加**（`totp_recovery_hashes` 覆盖写）。追加只会攒下更多能进门的钥匙。
- 复用同一个生成函数 `userTOTPNewRecoveryCodes`（与 `UserTOTPEnable` 共用）。两份实现迟早会在
  "生成几个 / 用什么熵 / 哈希怎么算"上走岔，而恢复码是账号的最后一道门。
- **不吊销会话**（不 bump `token_version`）：换一批备用钥匙不需要把正在用的人踢下线。
  因此前端复用的一次性凭据面板必须换文案 —— 它此前写着"所有旧会话已失效，请重新登录"。

## 用户状态里的剩余数量

`GET /api/v1/user/status` 的 `UserView` 新增 `totp_recovery_remaining`：只下发**数量**，
用于提示"快用完了，该重新生成了"。哈希与明文都绝不下发。解析失败按 0 处理 —— 这个数字坏了
不该让整个用户状态接口失败，那会让前端连"2FA 是否启用"都读不到。

前端在字段**缺失**（旧后端）时只说"需要时可重新生成"，不显示"剩余 0 个"：0 会被读成已经用光。

## 测试

- `internal/op/trusted_device_test.go`（8 条）：签发/校验、明文不落库、归属隔离、过期失效、
  按用户隔离的吊销、全部吊销、免码登录不改 2FA 状态、启用/解绑清除、恢复码整体替换。
- `internal/server/handlers/user_totp_trusted_test.go`（3 条）：登录下发 Cookie（HttpOnly）、
  免码登录 200、无/伪造 Cookie 仍要码、清单免 step-up、吊销要 step-up、重生成后旧哈希全部消失。
- `web/src/components/modules/setting/totpDevices.test.ts`（8 条）与 `totp.test.tsx`（新增 9 条）：
  设备清单四态（缺一档就是"拿不到"被渲染成"没有陌生设备"）、剩余数量缺失不编 0、
  重生成面板不出现"会话被吊销"文案。

## 仍未做

- 信任设备**不是** step-up 的旁路（见上）。若将来要做"本机 8 小时内免 step-up"，那是另一个决定：
  它会让一台被物理接触的机器在窗口内做任意高危操作，需要单独评审。
- 没有"信任设备命名/备注"、没有按 IP/地理位置的风险提示 —— 清单只展示 UA、IP 与时间，
  够用户认出陌生设备，不足以做设备指纹风控。
