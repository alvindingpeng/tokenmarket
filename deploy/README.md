# 部署工具

零依赖部署自动化（纯 Python 标准库，用 pty 模拟 expect 输入密码，无需 sshpass/paramiko）。

## 首次准备环境（新服务器）

1. 安装 Go 工具链（版本须匹配 go.mod）：
   ```bash
   python3 deploy/ssh_auto.py <host> '<password>' deploy/install_go.sh 320
   ```
2. 克隆源码到 `/opt/switchyard/src/switchyard` 并配置 remote 含 PAT（供 git pull）。

## 二进制构建（必须注入版本信息）

> ⚠️ **禁止裸 `go build`**：不注入 ldflags 时 `conf.Version="dev"`、前端 `VITE_APP_VERSION` 缺失，
> 「设置 > 信息」会因前后端版本不同源弹红色版本错误告警（详见 `docs/release-versioning.md`）。

**推荐路径（本地构建 + 上传，一条命令出完整产物）：**

```bash
bash scripts/build-local.sh -o /tmp/switchyard-vX.Y.Z    # 前端+后端同源注入版本
python3 deploy/scp_auto.py <host> '<pwd>' /tmp/switchyard-vX.Y.Z /opt/switchyard/bin/switchyard.new
# swap_deploy.sh 留在服务器上执行(需先上传一次), 版本号经 wrapper 传参 —— 见下节
```

`build-local.sh` 自动完成：`scripts/version.sh` 取版本 → 前端 `VITE_APP_VERSION` 构建（产物 `static/out/`）→ 后端 `-ldflags` 注入 `conf.Version/Commit/BuildTime` + `-tags=jsoniter`。

**服务器端构建（备选，pull_build.sh 已含版本注入）：** 前端产物 `static/out/` 不进 git（`//go:embed all:out` 需要它），需先本地 `cd web && pnpm run build` → `tar -czf /tmp/static_out.tgz -C static out` → `scp_auto.py` 上传 → `ssh_auto.py ... deploy/pull_build.sh 540`。

## 替换部署

```bash
# 现场改过名 / 想带期望版本时, 用一层 wrapper 显式传参 ——
# ssh_auto.py 是把**文件内容**当远端命令执行, 它的 argv[4] 是超时, 不是脚本参数:
# 直接写 `ssh_auto.py <host> <pwd> deploy/swap_deploy.sh 120 v1.5.20` 里的 v1.5.20 会被静默忽略。
cat >/tmp/swap.sh <<'EOS'
bash /root/swap_deploy.sh v1.5.20
EOS
python3 deploy/ssh_auto.py <host> '<pwd>' /tmp/swap.sh 300
```

自动：停服 → 备份 `bin/switchyard.prev-vN` → 原子替换 → 启动 → 验证（systemd 状态 + 数据库列 + 启动日志）。
`systemctl start` 失败也会走回滚（否则 `set -e` 会把新二进制留在原地、服务却停着）。
同名备份不覆盖：已存在 `switchyard.prev-v1.5.19` 时会追加时间戳，避免备份名与内容不符。

## 验证

`verify_db.sh`：用 Python sqlite3 模块验证迁移列（服务器无 sqlite3 CLI 时适用）。

## 服务器布局

```
/opt/switchyard/
├── bin/switchyard          # 运行二进制（备份: switchyard.prev-vN）
├── data/config.json        # 服务配置（SQLite: data/data.db）
├── data/backups/           # 自动备份
├── keys/                   # 密钥
└── src/switchyard/         # 源码（git clone，供编译）
/usr/local/go/              # Go 工具链（go1.26.4，与 go.mod 对齐）
```

systemd 服务: `switchyard.service`，监听 127.0.0.1:8088，nginx 反代 443（tt.xjj.se）。

## systemd 服务安装与启动限流

仓库中的 [switchyard.service](switchyard.service) 是从生产 unit 反向固化的副本。安装/更新:

```bash
sudo install -m 0644 deploy/switchyard.service /etc/systemd/system/switchyard.service
sudo systemctl daemon-reload
sudo systemctl enable --now switchyard
```

Unit 使用 StartLimitIntervalSec=300 / StartLimitBurst=10: 确定性故障(例如迁移报错)若无限重启会浪费资源并掩盖告警, 限流后停在 failed 可见; 5 分钟内仍允许 10 次启动以覆盖瞬时抖动。KEK 文件环境变量继续由安全批次 drop-in 管理, 不写进基础 unit。

## 健康检查、部署验证与回滚

唯一有效的存活探针是 `/healthz`; `/health` 与 `/api/v1/status` 不存在。部署脚本解析 /healthz JSON, 检查 `status=db=ok` 并输出 version/uptime。可将期望版本作为 `swap_deploy.sh` 的第一个参数(需经 wrapper, 见上节 `ssh_auto.py` 的参数说明), 例如:

```bash
# 远端: bash /root/swap_deploy.sh v1.5.20
```

健康检查、systemd active 状态或版本断言失败时, 脚本会恢复替换前的 `switchyard.prev-vN`, 再启动并探活**旧版本**（回滚时把期望版本换成替换前读出的旧版本号, 否则每次成功的回滚都会误报 `rollback health check failed`）, 最后以非零状态报告 `DEPLOY_ROLLED_BACK`。本地演练可设 `SWITCHYARD_BIN_DIR`、`SWITCHYARD_HEALTH_URL` 与 `SWITCHYARD_DRY_RUN=1`; dry-run 不调用 systemctl（只打印命令）、不修改二进制, 也**不做健康检查** —— 此刻响应 `/healthz` 的是现行进程, 拿它当"新二进制健康"的证据是假信号。

## 外部看门狗

安装 watchdog 脚本及 unit/timer:

```bash
sudo install -d -m 0755 /opt/switchyard/deploy
sudo install -m 0755 deploy/switchyard-watchdog.sh /opt/switchyard/deploy/switchyard-watchdog.sh
sudo install -m 0644 deploy/switchyard-watchdog.service /etc/systemd/system/switchyard-watchdog.service
sudo install -m 0644 deploy/switchyard-watchdog.timer /etc/systemd/system/switchyard-watchdog.timer
sudo systemctl daemon-reload
sudo systemctl enable --now switchyard-watchdog.timer
# 手动单次运行/查看
sudo systemctl start switchyard-watchdog.service
sudo tail -n 20 /opt/switchyard/data/watchdog-failures.log
```

看门狗每 60 秒探测一次 /healthz。服务 active 但探针连续失败 3 次且距上次重启超过 10 分钟时, 会尝试重启主服务。日志固定写入 `/opt/switchyard/data/watchdog-failures.log`, 每行严格为 `RFC3339本地时间 状态 明细`; 状态仅 `fail`、`ok`、`restart`。成功时只在前一状态非 ok 时记一行, 每次失败记一行; restart mtime 存在 `watchdog-last-restart`。此行格式是 Go 侧读取方的契约, Go 服务会在下次启动时读取并告警, 不要随意改字段/状态/顺序。

手动演练可用 `SWITCHYARD_HEALTH_URL` 覆盖探针 URL、`SWITCHYARD_WATCHDOG_LOG` 将日志改到临时文件、`SWITCHYARD_WATCHDOG_RESTART_FILE` 将重启时间戳改到临时路径, 并设置 `SWITCHYARD_WATCHDOG_DRY_RUN=1` 禁止真实重启。部署脚本启动后最多探测 `SWITCHYARD_HEALTH_RETRIES` 次(默认 20), 间隔为 `SWITCHYARD_HEALTH_INTERVAL` 秒(默认 1); 两者可在本地演练覆盖。安装前必须先创建 `/opt/switchyard/deploy`：生产目录初始不存在时 `install` 复制会失败, 且 watchdog service 的 `ExecStart` 指向此路径。
