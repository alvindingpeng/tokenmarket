# 静态凭据信封加密 —— 部署契约(主密钥与迁移接口)

本页是部署脚本的权威契约。实现见 internal/secretstore、internal/db/secretprotect.go、internal/db/secretmigrate.go、cmd/secret.go、cmd/start.go。

## 1. 主密钥(KEK)文件格式

- 路径(约定): `/opt/switchyard/keys/master.key`。目录 `0700 root:root`, 文件 `0600 root:root`(服务以 root 运行, 无需 chown)。
- 内容: **32 字节随机密钥材料的文本编码**, 二选一:
  - base64: `openssl rand -base64 32`(44 字符, 带换行可留, 加载时首尾空白自动去掉);
  - hex: `openssl rand -hex 32`(64 字符)。
  - 标准/URL base64、有无 padding 均可; **不接受**裸二进制、不接受口令/派生口令(不是 passphrase, 必须是 32 字节随机值)。
- 严格校验(fail closed, 无任何回退): 长度不等于 32 字节 → 启动失败; 全零密钥 → 启动失败; 文件不可读 → 启动失败; 未配置 → 启动失败(exit 1)。
- 位置要求: **独立于 data 备份**。config.json 只允许放路径(secret.kek_file), 不允许放密钥本体(config 会被备份带走); VACUUM INTO 的 data 备份不含也不需要密钥, 但**恢复到异机时必须另行带同一份 master.key**。master.key 自身单独备份(密码管理器/离线介质): 丢失 = 库内凭据不可恢复。

生成一次性:

    install -d -m 700 -o root -g root /opt/switchyard/keys
    openssl rand -base64 32 > /opt/switchyard/keys/master.key
    chmod 600 /opt/switchyard/keys/master.key

## 2. 环境变量 / 配置契约(优先级从高到低)

1. `SWITCHYARD_SECRET_KEK` —— 内联密钥材料(hex 或 base64), 一般只用于测试;
2. `SWITCHYARD_SECRET_KEK_FILE` —— 密钥文件路径;
3. `SWITCHYARD_KEK_FILE` —— 同上别名(部署脚本用这个名字完全等价);
4. config.json `secret.kek_file` —— 文件路径(只放路径)。

可选 keyID: `SWITCHYARD_SECRET_KEY_ID` 或 config `secret.key_id`(字符集 [A-Za-z0-9_.-], ≤32)。留空按密钥材料派生(同一份 key 在任何实例得到同一 ID), 多实例共享 key 无需配置。

service(switchyard.service, root, WorkingDirectory=/opt/switchyard,
ExecStart=/opt/switchyard/bin/switchyard start --config /opt/switchyard/data/config.json) 推荐接线:

    [Service]
    Environment=SWITCHYARD_KEK_FILE=/opt/switchyard/keys/master.key

## 3. 迁移接口(既有明文就地加密)

命令(与 start 用同一份配置):

    switchyard secret status  --config /opt/switchyard/data/config.json   # dry-run, 只报告不落笔
    switchyard secret migrate --config /opt/switchyard/data/config.json   # 就地加密

语义(硬保证):
- **原子**: 全部写入一个事务, 中途失败不留半加密状态;
- **幂等**: 已加密行只验不改(顺带验证当前 key 能打开), 重复执行零写入, 信封字节不变;
- **fail closed**: key 缺失/非法、或存量信封打不开(换过 key / 密文被搬移)→ 报错退出, 不写任何行;
- **范围**: 信封迁移函数处理 `settings`(密钥类/内部设置项)与 `channel_keys`；但 `secret migrate` 命令先执行 `InitDB`，因此也执行 schema 迁移 022，将旧 `api_keys.api_key` 转为单向指纹并删除明文列。旧 Key 值继续可用于认证，但数据库不能再交给旧版二进制。SQLite 信封行写入与 API Key schema 迁移各自事务保护，并非覆盖整条命令的单一事务。
- **只读状态**: `secret status` 只支持 SQLite，通过 `mode=ro` 与 `query_only` 打开既有文件，不调用 `InitDB`、不做 schema 迁移、不创建库；其他数据库类型明确拒绝而非伪装只读。
- `start` 每次启动自动执行同一迁移(先于任何写库)。不跑 migrate 也能上线, 但建议部署脚本显式跑一次拿统计。

退出码: 成功 0; key 缺失/非法、迁移失败 1(日志里有明确错误行, systemd 可见)。

日志输出(可 grep):
- 成功: `secret migration done: settings_encrypted=N channel_keys_encrypted=N already_encrypted=N empty_skipped=N`
- dry-run: `secret status (key ID <id>): would_encrypt=N already_encrypted=N empty_skipped=N`

## 4. 部署序列(建议)

    systemctl stop switchyard.service
    install -d -m 700 /opt/switchyard/keys      # 首次
    [ -f /opt/switchyard/keys/master.key ] || openssl rand -base64 32 > /opt/switchyard/keys/master.key
    chmod 600 /opt/switchyard/keys/master.key
    systemctl edit switchyard.service           # 加 Environment=SWITCHYARD_KEK_FILE=/opt/switchyard/keys/master.key
    /opt/switchyard/bin/switchyard secret status  --config /opt/switchyard/data/config.json   # 预期: would_encrypt ≈ 25 channel_keys + 当前已填的密钥类设置项
    /opt/switchyard/bin/switchyard secret migrate --config /opt/switchyard/data/config.json   # 预期: channel_keys_encrypted=25
    systemctl start switchyard.service          # 启动自动复查; key 有问题会 exit 1 并打日志, 绝不带明文跑

现状规模参考(25 channel_keys / 2 apikeys / 110 settings): 预期 `channel_keys_encrypted=25`; settings 里只有密钥类/内部项被加密(epay_key、stripe_secret_key、github_token、auth_secret、metrics_token 等已填值的), 其余 ~100 个业务配置项保持明文(它们不是凭据); api_keys(2)由单向哈希流程自理。准确数字以 `secret status` 的 dry-run 为准。

## 5. 当前覆盖范围(明确边界, 不夸大)

**已落库加密(信封)**: settings.value 中的密钥类/内部设置项(共 11 键: github_token、alert_telegram_bot_token、alert_email_password、epay_key、alipay_private_key、stripe_secret_key、stripe_webhook、creem_api_key、creem_webhook、auth_secret、metrics_token)与 channel_keys.key(渠道上游凭据)。API Key(api_keys)是**单向哈希**, 与信封加密是两条线(哈希不可逆, 不在本迁移范围)。

**未覆盖 —— 已知残余风险(不要声称它们被加密)**:
- 渠道 custom_header: 可能装 Authorization / 自定义凭据头, 明文落库;
- 渠道 channel_proxy 与全局设置 proxy_url: 代理 URL 可能含 user:pass, 明文落库;
- 渠道 base_url: user:pass@host 形态的内嵌凭据明文落库;
- 渠道 param_override: 可能夹带凭据参数, 明文落库。

这些列当前只受数据库文件权限与备份封装保护(backup.passphrase 有值时备份文件是 OCTOBK1 加密封装; 裸库文件本身不加密)。若这些位置会放真凭据, 请按后续项扩展 serializer 覆盖, 或改为只放非敏感配置、凭据统一进 channel_keys。

**启动验证(强保证)**: 每次 start 与 secret migrate 都会把 settings.value 与 channel_keys.key 里出现的**每一个信封**逐条用当前主密钥验开(包括落在非密钥行里的信封), 错误 KEK 在启动这一步就 exit 1 并打日志, 不必等有流量才发现。启动日志的可见凭证: secret at-rest verified: encrypted_rows=N newly_encrypted=M ...

## 6. 备份 / 恢复与换钥

**备份 HTTP 导出(/api/v1/setting/export)返回的 JSON 是解密后的明文快照, 不是加密备份** —— 不能声称导出天然加密。当前由 step-up 二次确认闸 + 传输通道保护该端点; 落盘/外传必须另行经备份加密封装或受控通道。

- VACUUM INTO 物理备份(data.db 快照): 密文绑定 master.key, **异机恢复必须带同一份 master.key**。
- JSON 导出(备份封装 OCTOBK1 内的 dump): 解密后的明文快照 —— 跨实例恢复靠它(导入按目标机 key 重新加密), 但裸 dump 文件即凭据清单, 只允许在 passphrase 封装内流转。
- 换钥: 备份 → `secret migrate` 无需操作(旧信封换钥打不开会 fail closed)→ 正确路径是 导出(明文) → 换 master.key → 导入(新钥重加密)。直接换 key 后启动会明确报错而不是静默放行。

## 7. 部署前隔离副本演练(生产 DB 拷到 /tmp, 不碰生产)

secret migrate/status 都不启动后台任务：migrate 执行 InitDB（含 API Key schema 迁移）后做信封迁移；status 通过 SQLite 只读连接验证/计数，不执行任何 schema 迁移。verify-migration 只跑迁移。start 会启动后台任务（备份/更新检查等）；演练必须使用清除远端备份及告警目标的独立配置，不能直接带生产配置启动。

1) 取一致副本(WAL 安全; 直接 cp 在服务运行时可能截断 WAL):
    sqlite3 /opt/switchyard/data/data.db ".backup /tmp/rehearsal/data.db"
   (或停服后 cp data.db /tmp/rehearsal/data.db)

2) migrate-verify(dry-run, 零写入; 逐条验开副本里每个信封 + 报告将加密行数, KEK 不对在这里就报错):
    SWITCHYARD_KEK_FILE=/opt/switchyard/keys/master.key \
    /opt/switchyard/bin/switchyard secret status --config /opt/switchyard/data/config.json --database /tmp/rehearsal/data.db

3) 在副本上真跑迁移(只改副本; 日志先打 'secret migration target: ... path=/tmp/rehearsal/data.db' 可核对):
    SWITCHYARD_KEK_FILE=/opt/switchyard/keys/master.key \
    /opt/switchyard/bin/switchyard secret migrate --config /opt/switchyard/data/config.json --database /tmp/rehearsal/data.db

4) 隔离端口完整启动演练(副本库 + 独立端口):
    SWITCHYARD_KEK_FILE=/opt/switchyard/keys/master.key \
    /opt/switchyard/bin/switchyard start --config /opt/switchyard/data/config.json --database /tmp/rehearsal/data.db --port 18081

安全契约: secret 命令的 --database 只接受隔离克隆件(与 verify-migration 同一校验: 拒绝 /opt/switchyard/data 下任何路径、软链、硬链、非 SQLite 文件), 指向生产库直接 exit 1; 不给 --database 时操作 config 的 database.path(即生产, 正式迁移路径)。start 的 --database/--port 是普通覆盖, 不做克隆限制(生产 start 本来就要跑生产库)。

演练注意: 步骤 4 的实例后台任务照常跑 —— 若 backup.remote_url / 告警 webhook 已配置, 演练会推真实备份/告警; 演练前用一份去掉 remote_url 的 config 副本, 或演练后核对异地目录没有演练产物。

## 8. backup passphrase(备份封装口令)—— 单列管理

backup.passphrase 与主密钥是两回事, 别混:
- **位置**: 只在 config.json 的 backup.passphrase 或环境变量 SWITCHYARD_BACKUP_PASSPHRASE; **不入库**, 因此不在 DB 静态加密(信封)覆盖范围里 —— 它保护的是备份文件(OCTOBK1 封装), 不是数据库行。
- **与主密钥的关系**: 信封加密防的是『DB 文件泄漏』(没有 master.key 解不开 settings.value / channel_keys.key —— auth_secret 不落明文, 拿不到 JWT 签名密钥就伪造不了 admin 身份、绕不过 step-up); backup passphrase 防的是『备份文件外泄』。两道防线各自独立, 都要做。
- **注意 config.json 自身含该口令**: config.json 的任何副本/外传要当凭据对待(权限 600, 不进代码库, 不明文贴工单)。改用 SWITCHYARD_BACKUP_PASSPHRASE 环境变量(EnvironmentFile, 600)可让 config.json 不含口令。
- **轮换**: 改口令只影响之后新生成的备份封装; 旧备份文件用旧口令, 保留或按需重打包。口令丢失 = 旧备份不可解。
