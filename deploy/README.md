# 部署工具

零依赖部署自动化（纯 Python 标准库，用 pty 模拟 expect 输入密码，无需 sshpass/paramiko）。

## 首次准备环境（新服务器）

1. 安装 Go 工具链（版本须匹配 go.mod）：
   ```bash
   python3 deploy/ssh_auto.py <host> '<password>' deploy/install_go.sh 320
   ```
2. 克隆源码到 `/opt/switchyard/src/switchyard` 并配置 remote 含 PAT（供 git pull）。

## 二进制构建（两段式）

前端产物 `static/out/` 不进 git（`//go:embed all:out` 需要它）：

1. 本地构建前端：`cd web && pnpm run build`（产物输出到 `../static/out/`）
2. 打包上传：`tar -czf /tmp/static_out.tgz -C static out` → `python3 deploy/scp_auto.py <host> '<pwd>' /tmp/static_out.tgz /tmp/static_out.tgz`
3. 服务器拉取并编译：`python3 deploy/ssh_auto.py <host> '<pwd>' deploy/pull_build.sh 540`

## 替换部署

```bash
python3 deploy/ssh_auto.py <host> '<pwd>' deploy/swap_deploy.sh 120
```

自动：停服 → 备份 `bin/switchyard.prev-vN` → 原子替换 → 启动 → 验证（systemd 状态 + 数据库列 + 启动日志）。

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
