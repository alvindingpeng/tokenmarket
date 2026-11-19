# 第二批安全加固验收记录

当前状态：**已部署生产并通过上线验收（2026-10-24）**。生产 `prod.example.com` 运行 v1.0.0，迁移与兼容性证据见下。

## 交付边界

- 管理员高危操作在执行副作用前要求密码二次认证，验证凭证绑定当前登录会话、用户及令牌版本，具有短有效期。读取普通列表不要求二次认证。密码确认不是双人审批，也不是 TOTP 多因素认证。
- 需要转发或支付时恢复原文的凭据使用 AES-256-GCM 信封加密：随机数据密钥加密凭据，外部主密钥包装数据密钥；版本、归属与完整性校验防止密文调换。主密钥不写入数据库、源码或日志。
- 客户端 API Key 单向哈希存储，创建时仅返回一次原文。旧凭据迁移后仍可用于调用，列表只显示掩码。哈希不能还原，旧程序不能直接使用迁移后的数据库。

## 部署前检查

- [ ] Go 全量测试与 vet、前端类型检查及相关测试通过。
- [ ] 验证凭据加密的错误主密钥、篡改、幂等迁移、重启加载与备份恢复。
- [ ] 验证 API Key 旧密钥迁移、查找、禁用、删除、权限隔离与列表不泄漏。
- [ ] 验证二次认证缺失、伪造、过期、其他会话与限流，确认失败请求没有业务副作用。
- [ ] 停止服务后备份数据库、旧程序与配置，建立一致的回退点。
- [ ] 外部主密钥文件权限 0600，并记录独立恢复要求。

## 部署后检查

- [ ] 服务 active；本地与公网 healthz 均返回新版本。
- [ ] 公网前端资源包含同一版本号。
- [ ] 数据库完整性通过，现有 API Key/上游凭据条目数一致。
- [ ] 查询原始数据库字段，凭据列不再保留原文，只输出计数与格式验证结果。
- [ ] 已有 API Key 可通过鉴权，错误密钥被拒，管理员高危操作无二次认证时被拒。
- [ ] 重启后依然可读取密文和使用既有 API Key。

## 当前验证证据（非上线验收）

- 部署工具隔离测试 8 项通过（增加重复 API Key 指纹归属拒绝）（含停机后页重写/WAL 截断工具）：WAL 一致副本、私有权限、运行中拒绝备份、外键异常拒绝、负冻结与 API Key 哈希格式闸门，以及迁移前后账务/旧密钥指纹保全比对。
- 既有资金发布保护测试 4 项通过。
- `internal/secretstore` 单测与 race 测试通过；不等于数据库读写/启动迁移验证完成。
- 前端 TypeScript 与 81 项 Vitest 测试通过（含 14 项二次认证请求测试），完整 lint 通过。密码弹窗 Playwright 8 项通过（含两代验证交错时旧响应不得覆盖新忙态）：390/1280 宽度下取消不补发、错误密码不重发、正确密码只重发一次且带证明，以及在途取消后迟到凭证作废。新增一次性 API Key 展示 Playwright 4 项通过，创建载荷无密钥材料、关闭后 DOM 无完整密钥且列表无完整旧密钥复制入口；与密码确认合跑 12/12 通过。另 7 项账务恢复与首页浏览器回归通过。最新前端全量复查为 88 项 Vitest、TypeScript、lint 全部通过。
- 最新 `handlers`、`auth`、`model` 定向测试通过；已修复真实路由重复构建问题，并增加算法/发行者/过期约束及禁止调用方自选 API Key 的负面测试。模型层 serializer 注册与数据库幂等断言已修复；最新全量 Go build、vet、test 均通过。安全关键包 race 检查中 db/migrate/model/op/secretstore/auth/middleware 已通过；handlers 新增默认密码门禁 fixture 已修复进程级设置缓存串场，父复跑整包 race 通过；上述安全关键包并发门禁全部通过。
- 只读确认生产仍为 v0.45.0、服务健康、active_requests=0。已通过 SQLite backup API 建立私有权限的一致演练副本：users=3、api_keys=2、channel_keys=25、billing_records=9074、balance_ledgers=18608；完整性正常，无外键异常、负冻结或待处理结算任务。已对私有副本执行真实 CLI 迁移：2 条 API Key 转指纹并删除明文列，2 条设置与 25 条渠道凭据转信封。compare 的全部账务保全/指纹一致性项通过，inspect 完整性与外键正常。重复迁移 0 写入、27 条既有信封验证通过；错误主密钥退出码 1。新增 opt-in 只读测试逐行核验 110 条设置、25 条渠道凭据解密原文与迁移前一致，2 条旧 API Key 指纹匹配。演练副本已执行 WAL 截断、VACUUM 页重写与再次截断，完整性仍正常；扫描活动副本文件，旧 API Key 与长度至少 12 字节的旧渠道密钥字节命中均为 0（不代表物理介质安全擦除，也不涉及旧备份）。未对正式生产停机、迁移或上线。

只读 API Key 迁移验收：`python3 scripts/security-batch2-inspect.py /path/to/data.db --require-hashed-api-keys`。该命令检查明文列已删除及指纹格式，不打印密钥；不证明加密凭据可解密或旧 API Key 可鉴权，这两项需独立验证。

## 候选构建（尚未部署）

- 候选版本按不兼容数据库迁移约定升级为 v1.0.0；前后端由 `scripts/build-local.sh` 同步构建。当前候选仍需在所有最后修复落盘后重新构建，不应把中间构建视为正式发布产物。
- 最新全量 Go build/vet/test 再次通过；生产 `jsoniter` 构建标签下 handlers/model/op/cmd 测试通过；限流预算测试连续 3 轮通过。
- 新增数据库关闭、主密钥 Reset/Init、数据库重开验收：既有签名密钥及渠道凭据恢复原文，旧 API Key 指纹可查，重启复核零写入且信封字节不变。
- 当前候选真实打开演练副本成功，27 条信封验证、0 条新加密；inspect 无无效/重复指纹。
- 已修复发布驱动 stderr 统计解析，且真正接入重复迁移零写 + 只读 status 检查；入口恢复严格要求 200，新增 502 拒绝测试。21 项隔离测试通过（迁移假执行器改为真实 stderr 日志，防止假绿）。
- 已修复 status 调用 InitDB 会迁移 schema 的缺陷：SQLite mode=ro/query_only，不创建库、不跑 schema 迁移，旧库字节前后相同测试通过；缺失配置非零退出。状态检查目前仅支持 SQLite，其他类型明确拒绝。
- 已修复客户端登出/401 后迟到凭证入缓存与提示期间 Abort 后重发竞态，新增 3 条测试；前端 99 单测、TypeScript、lint 全通过。一次性 Key 明文从 mutation data 清除，仅在当前展示弹窗状态保留；最新桌面/移动 Playwright 12/12 再次通过。
- 最新 Go build/vet/全量测试和 db/auth/handlers race 门禁均通过。只读生产验收工具23项隔离测试通过，尚未对生产执行。
- 尚需收敛备份导入指纹所有权验证，重建并冻结最终产物，再执行正式停机发布及旧 Key 实测。生产仍未停机、未迁移、未部署。

## 生产发布与上线验收（2026-10-24，实际执行）

生产发布由 `scripts/deploy-security-batch2.py` 驱动，候选二进制 SHA-256 `7b609c6ab8c132f942dd931f8c042a7a0eeb24b9f8f700bd45fb9e494bcbd0b8`，版本 v1.0.0，上一版本 v0.45.0。

### 首次发布故障与修复

首次发布在 `candidate secret migrate` 失败：`unable to open database file: out of memory (14)`（SQLite `SQLITE_CANTOPEN`）。根因是 `config.json` 的 `database.path` 为相对路径 `data/data.db`，systemd 以 `WorkingDirectory=/opt/switchyard` 运行服务，而发布脚本的 `real_runner` 未设置 `cwd`，候选二进制在部署者 shell 的工作目录（`/root`）下打开该相对路径而失败。数据库未被修改（打开失败发生在任何迁移之前）；主密钥已创建并有独立备份；一致恢复点已建立；脚本按 fail-closed 保持入口 503、服务停止。

恢复：回滚恢复点内的 v0.45.0 二进制并恢复 nginx 原配置，前端恢复可用。修复：`_exec` 增加 `cwd`，候选 `secret migrate`/`secret status` 统一以 `cwd=/opt/switchyard` 执行；测试记录并断言该工作目录，新增回归用例。发布脚本 21 项测试通过后重新发布成功。

### 迁移与保全（发布元数据 `deploy.json`）

- 密钥迁移：`settings_encrypted=2`、`channel_keys_encrypted=25`、`already_encrypted=0`、`empty_skipped=9`；key ID `9af4de39c97e28c9`；`schema_api_hash=true`、`encryption=true`。
- 幂等复验：重复 `secret migrate` 报告 `settings_encrypted=0`、`channel_keys_encrypted=0`、`already_encrypted=27`、`would_encrypt_after=0`、`decrypt_verified=true`（零写入且每个信封都能用当前主密钥解开）。
- schema 闸门：`api_key_plaintext_column_removed=true`、`api_key_hash_schema=true`、`channel_secrets_enveloped=25`、`channel_secrets_plaintext=0`、`invalid_api_key_hashes=0`。
- compare（迁移前后各一次）：users/api_keys/channel_keys/billing_records/balance_ledgers/balance_reservations/stats_api_keys 的行列保全全部 true；`legacy_api_key_fingerprints_match=true`；`settings_changed_only_to_envelopes=true`。
- inspect（compact 前后各一次）：`integrity=ok`、users=3、api_keys=2、channel_keys=25、billing_records=9283、balance_ledgers=19026、外键违规 0、负冻结 0、无效/重复指纹 0。
- compact：`compacted=true`、`integrity=ok`；旧凭据字节残留 `legacy_plaintext_residue=0`（不代表物理介质安全擦除）。
- 主密钥 `/opt/switchyard/keys/master.key` 0600/目录 0700，独立备份 `/root/switchyard-master-key-20261024-214103/master.key`；材料未写入任何日志或元数据。
- 恢复点 `/opt/switchyard/data/releases/20261024-214059-security-batch2`（含迁移前数据库、v0.45.0 二进制、配置与校验和）。数据库未被恢复或覆盖。

### 运行与兼容性验收

- 服务启动与 `systemctl restart` 后健康均为 `version=v1.0.0, db=ok, status=ok`；入口恢复，公网 `https://prod.example.com/` 返回 200、`/healthz` 报告 v1.0.0。
- 只读生产验收 `scripts/verify-security-batch2.py`：`overall=pass`（9 pass / 0 fail / 0 warn / 1 skip）。两把迁移前备份中的旧 API Key 明文按指纹匹配后**实测通过 `/api/v1/apikey/login`（200）**；随机错误 Key 被拒（401）；stats 响应仅含 `key_prefix`/`key_last4` 掩码、无 `api_key`/`key_hash` 字段与密钥形态；用迁移前备份的 `settings.auth_secret` 在内存签发的管理员会话被接受（证明信封加解密往返正确）；普通 `GET /api/v1/user/status` 无需二次认证；`POST /api/v1/user/manage/create` 无 step-up 令牌得到 **428 + `step_up_required`**，且 users 表行数/最大 ID 前后不变（零副作用）。证据文件 `/root/verify-security-batch2-evidence.json`。
- 该验收在重启后的服务上执行，即旧凭据在重启后仍可鉴权；信封解密一致性由发布驱动的 `migration_repeat`（27 条信封全部用生产主密钥验证解开）覆盖。

## SQLite 页残留处理

仅把逻辑列改成密文或删除明文列，不代表旧字节已从空闲页或 WAL 中消失。离线迁移和语义验收完成后，在服务确已停止时执行 `python3 scripts/security-batch2-compact.py /path/to/data.db --service-stopped`，完成 WAL 截断、VACUUM 页重写、再次截断及完整性检查；再执行 inspect/compare 并验证旧凭据字节不在活动 DB/WAL 文件中。该步骤不清除历史备份，也不保证 SSD、快照或底层物理介质的安全擦除。

## 恢复约束

数据库加密不能保护运行进程内存，也不能替代主机权限控制。历史备份可能仍包含旧明文，不会因迁移而自动消失。便携 JSON 导出若包含可恢复凭据，应按秘密文件管理；静态数据库备份恢复时需保留匹配的外部主密钥。API Key 哈希化为单向迁移，回退旧版本必须使用部署前的数据库与旧程序配套恢复，并评估部署后新增交易记录，禁止直接覆盖运行中的数据库。
