# 高危操作二次验证(step-up)

起于 2026-10-24: 管理员会话一旦被拿走(肩窥、共享电脑、XSS、cookie 泄露), 余额调整、提现审批、
备份导入、敏感导出这些高危写操作就全部裸奔。本次给它们加一道"操作前重新确认身份"的闸门。

## 契约

- 重新认证方式是**密码**(不假设部署了 TOTP 之类的第二因素): `POST /api/v1/user/step-up`,
  body `{"password": "..."}`, 身份以登录态为准, 不接受请求体自报用户。
- 成功返回短期凭证 `{ token, expires_in }`(JWT, 默认 5 分钟), 高危请求放进
  `X-Step-Up-Token` 头提交。
- 缺凭证/凭证失效时服务端回 **428** 且响应带 `error_code=step_up_required`:
  请求在进业务处理前就被拦下, 因此"确认密码后原样重发一次"不可能把同一笔交易执行两遍。

## 凭证三重绑定

| 绑定 | 实现 | 失效场景 |
|---|---|---|
| 用户 | `suid` 比对当前登录身份 | 凭证跨账号复用直接拒绝 |
| 令牌版本 | `sver` 比对库内 `token_version` | 改密/禁用/服务端登出都会递增, 旧凭证当场作废 |
| 登录会话 | `sid` = 当前登录 JWT 的 SHA-256 指纹(截断) | 换一段登录会话(新登录/另一台设备)后凭证不跟随 |

签名走与登录令牌同一把全局密钥(HS256); 解析统一走 `strictJWTParserOptions`(登录/step-up 共用):
只收 HS256(防算法混淆)、issuer 必须是本服务、必须带 exp(防永久票)、iat 不得在将来(防预签)。
令牌字段名刻意与登录 Claims(`uid`/`ver`)错开, 登录 JWT 与 step-up 凭证互不通用(两头都有单测钉死)。
凭证只存前端内存, 不进 storage; 伪造/篡改/他人凭证与"缺凭证"的响应逐字节一致, 不给分辨试探的空间。

## 闸门次序与默认改密闸门(互不混淆)

路由链是 `Auth` → `RequireRole`(403) → `RequireStepUp`(428) → 业务 handler:

- **403 先于 428**: 无权限者拿到角色拒绝而不是密码确认框 —— 428 不该比 403 更早把
  "存在这么个高危端点"透露给无权限者, 凭证也不能把无权限变成有权限。
- 两个闸门都在业务 handler 之前, 任何一边拒绝都**零副作用**(有单测核对库内无新增记录)。
- **默认改密闸门(`admin_default_pwd_changed`)与 step-up 是两把独立的闸**: 标记未翻时
  step-up 端点自身也在闸门内(403, 连凭证都换不到), 直接签发出来的凭证也顶替不了标记。
- **改密本身(`/api/v1/user/change-password`)明确免 step-up**: 否则默认口令未改时
  "换不到凭证就得先改密、改密又要凭证"死循环。改密完成即翻标记并递增 token_version
  (顺带作废所有旧 step-up 凭证, 前端也在改密成功时清掉本地凭证)。

## 覆盖面(以各 handler 注册处的 `RequireStepUp` 标注为准; 读列表一律不设闸)

| 类别 | 方法 | 路由 |
|---|---|---|
| 用户管理(建号/余额调整/重置密码/删号) | POST·DELETE | `/api/v1/user/manage/create`·`update`·`reset-password`·`delete/:id` |
| 提现审批(审核/打款确认/回滚) | POST | `/api/v1/withdrawal/review`·`mark-paid`·`rollback` |
| 争议裁决 | POST | `/api/v1/dispute/review` |
| 结算生成 | POST | `/api/v1/settlement/generate` |
| 计费恢复 | POST | `/api/v1/ops/billing-recovery/run` |
| 备份(导入/生成) | POST | `/api/v1/setting/import`·`/api/v1/ops/backup/run` |
| 敏感导出(整库含凭据/审计/日志/计费明细) | GET | `/api/v1/setting/export`·`/api/v1/audit/export`·`/api/v1/log/export`·`/api/v1/stats/billing/records/export` |
| 系统设置写入 | POST | `/api/v1/setting/set` |
| 系统更新 | POST | `/api/v1/update` |
| 兑换码(批量生成/作废) | POST | `/api/v1/redemption/batch/create`·`/api/v1/redemption/code/disable` |
| 运维敏感项 | GET·POST | `/api/v1/ops/auth-secret/rotate`(登录签名密钥轮换, 全员下线)、`/api/v1/ops/metrics/token`·`/rotate`、`/api/v1/ops/log-lifecycle/run` |

**明确不保护**(可按需一行接入 `middleware.RequireStepUp()`): 改密/登出/身份查询(bootstrap 与
日常所需)、`/api/v1/log/clear`、`ops/health/run`、`ops/metrics/reset`、告警测试类、充值人工补单、
渠道/分组/模型配置类写操作。

## 限流与审计

- 密码验证与登录共用同一套防爆破闸门(指数退避锁定), 但计数主体独立(`stepup:ip:`/`stepup:u:`),
  互不挤占额度; 超限 429 + Retry-After, 锁定期内即便口令正确也不放行。
- 审计记 `user.step-up`(成功/失败各一条), 明细只写结论(如 "password confirmed"
  / "password rejected"), **不含密码、不含凭证、不含会话令牌** —— 有单测扫描全部审计行兜底。

## 前端交互(web/src/api/client.ts)

- 通用 HTTP 客户端统一处理: **只对明确的 `step_up_required` 错误**弹密码确认
  (`<StepUpDialog>` 全局挂载, 真实 dialog 而非 window.prompt), 确认后把被拦下的那次请求
  原样重发一次(带凭证)。
- 其它任何错误(超时/5xx/401/校验失败)一律照常抛给调用方: 不弹窗、不自动重试;
  重发结果就是最终结果, 不再有第二次弹窗或第三次请求, 因此不会重复已执行的交易。
- 用户取消确认 = 请求根本不发, 原样抛出 step-up 错误; 并发请求共用一次弹窗。
- 取消可以发生在验证在途时(Escape 是唯一的在途入口, 取消键此时禁用): **迟到的验证响应
  属于上一代确认, 换来的凭证整体作废**, 不得替任何后一个请求盖章(弹窗代际守卫 +
  换凭证接口只换不存)。
- 导出改走 `apiDownload`(fetch + blob): a[href]/window.open 带不了验证头;
  备份导入走 `apiUpload`(FormData, 不手写 Content-Type)。
- 换凭证请求本身 `skipStepUp`, 且 401 不广播"登录失效"(401 在这里只是口令不对)。

## 测试

- Go `internal/server/auth/stepup_test.go`: 绑定(用户/版本/会话)、过期/伪造/篡改拒绝、
  登录↔step-up 令牌互换性、无会话拒签发、限流独立性; `stepup_strict_test.go`: 严格 JWT 约束
  (wrong-algorithm/issuer/missing-exp/future-iat/expired)。
- Go `internal/server/middleware/stepup_test.go`: 准入矩阵(缺凭证、外会话、登录 JWT 顶替、
  改密失效)+ 伪造与冒用矩阵(乱码、伪签、篡改载荷、他人凭证 —— 响应与缺凭证逐字节一致,
  handler 零执行)。
- Go `internal/server/handlers/stepup_test.go`: 签发端点会话绑定 + 全审计行泄密扫描、错口令
  401 + 限流 429、经**真实路由注册表**的保护面覆盖、OpenAPI 文档、403 先于 428(含零副作用核对)、
  凭证不顶替默认改密标记、改密 bootstrap 免 step-up 且整条链路走通。
- 前端 `web/src/api/step-up.test.ts`: 14 例(只认明确错误、只重发一次且 body 一致、取消不发且
  不留凭证、非 step-up 错误绝不弹窗/重试、超时不弹、重发结果即最终、旧凭证重换、并发单弹窗、
  验证 401 不踢登录、下载/上传同交互)。
- Playwright `web/e2e/step-up.spec.ts`(真实 Chromium, 网络全 mock): 弹框/取消/正确口令只重发一次/
  在途取消竞态迟到凭证作废/移动端布局。
