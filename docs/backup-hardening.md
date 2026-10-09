# 备份加密 / 异地推送 / 校验和

## 概述
自动备份从「本地明文快照」升级为可选加密 + 必写校验和 + 可选 WebDAV 异地推送, 推送失败自动进告警闭环。

## 加密格式(版本 1)

```
header = "OCTOBK1\n"(8) + salt(16) + iv(16)
body   = AES-256-CTR(passphrase派生密钥, iv, 明文)
tail   = HMAC-SHA256(macKey, header + 密文)   // 32 字节, 加密后认证
```

- 密钥派生: PBKDF2-HMAC-SHA256, 20 万轮, 派生 64 字节拆为 encKey/macKey。
- 口令来源仅两处: `config.json` 的 `backup.passphrase`, 或环境变量 `OCTOPUS_BACKUP_PASSPHRASE`(env 优先)。
- **口令绝不入库**: 备份文件包含整个数据库, 口令入库等于把钥匙锁进要送走的保险箱。
- 解密先验 MAC 再落明文; 错口令/篡改一律失败且不产出文件。

## 校验和

每份备份旁路写 `<产物>.sha256`(`sha256sum` 标准格式)。 保留策略删除备份时同删校验和。 恢复前先 `octopus backup verify`。

## 异地推送(WebDAV)

- 设置 `backup_remote` 开启后, 每次备份把产物与 `.sha256` 逐一 `PUT` 到 `backup.remote_url`。
- 凭据在 `config.json` 的 `backup.remote_user` / `backup.remote_password`(同样不入库)。
- 每文件最多 3 次尝试(间隔 1s/2s), 60s 超时; 目录须预先创建。
- 推送失败: 本地备份仍算成功, `remote` 字段记 `failed`, 并 `RaiseAlert("backup_failed")` 进 Webhook 闭环。

## 配置示例(config.json)

```json
{
  "server": { "host": "0.0.0.0", "port": 8080 },
  "log": { "level": "info" },
  "database": { "type": "sqlite", "path": "data/data.db" },
  "backup": {
    "passphrase": "换成长口令",
    "remote_url": "https://dav.example.com/octopus/",
    "remote_user": "octopus",
    "remote_password": "换成长口令"
  }
}
```

## 设置开关(管理后台 > 运营可靠性)

| 键 | 默认 | 说明 |
|---|---|---|
| `backup_encrypt` | false | 备份加密; 开启但未配置口令 → 备份判失败并告警 |
| `backup_remote` | false | 异地推送; 开启但未配置地址 → 记 failed 并告警 |

## 恢复流程

```bash
# 1) 先验校验和(异地取回的备份尤其必做)
octopus backup verify --file backup-20261008-181556.db.enc

# 2) 加密备份先解密(口令来自 config/env)
octopus backup decrypt --in backup-20261008-181556.db.enc --out restored.db

# 3) 校验解出的库
octopus backup verify --file restored.db

# 4) 停服换库
systemctl stop octopus-multiuser
mv /opt/octopus-multiuser/data/data.db /opt/octopus-multiuser/data/data.db.bak
mv restored.db /opt/octopus-multiuser/data/data.db
systemctl start octopus-multiuser
```

## 非 SQLite(MySQL/PostgreSQL)

进程内一致性快照仅支持 SQLite(VACUUM INTO)。 MySQL/PG 请由 DBA 做离线备份(如 mysqldump / pg_dump + WAL 归档), 本模块不导出全量数据; 备份目录仍会生成空记录提示, 不影响本地文件备份开关。

## 验证(E2E 28/28)
`octopus-e2e-backup.py`: 加密产物生成、明文产物删除、SHA-256 一致、WebDAV 收到 2 个 PUT(含 Basic 认证)、CLI 解密成功、`backup verify` 通过、错口令失败且无输出、远端失联时本地备份成功 + `remote=failed` + `backup_failed` 告警。
