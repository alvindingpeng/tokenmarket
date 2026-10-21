#!/bin/bash
# 手动部署脚本 - 需要手动输入 SSH 密码
# 
# 使用方法:
#   bash scripts/deploy-manual.sh
#
# 该脚本会生成详细的部署命令，你需要手动在服务器上执行

set -euo pipefail

readonly SERVER="tt.xjj.se"
readonly USER="root"
readonly BINARY_PATH="/usr/local/bin/switchyard"
readonly SERVICE_NAME="switchyard.service"
readonly BACKUP_DIR="/var/backups/switchyard"
readonly LOCAL_BINARY="build/switchyard-local"

if [ ! -f "${LOCAL_BINARY}" ]; then
    echo "❌ 找不到二进制文件: ${LOCAL_BINARY}"
    echo "   请先运行: bash scripts/build-local.sh"
    exit 1
fi

echo "🚀 部署到 ${SERVER}"
echo ""
echo "本地二进制: ${LOCAL_BINARY}"
SHA256_LOCAL=$(sha256sum "${LOCAL_BINARY}" | cut -d' ' -f1)
echo "SHA-256: ${SHA256_LOCAL}"
echo ""

# 1. 上传二进制
echo "📦 步骤 1: 上传二进制文件"
echo "   执行: scp ${LOCAL_BINARY} ${USER}@${SERVER}:${BINARY_PATH}.new"
echo ""
scp "${LOCAL_BINARY}" "${USER}@${SERVER}:${BINARY_PATH}.new"
echo "   ✓ 上传完成"
echo ""

# 2. 在服务器上执行部署
echo "🔧 步骤 2: 在服务器上执行以下命令部署:"
echo ""
cat << 'DEPLOY_COMMANDS'
# 验证上传的文件
sha256sum /usr/local/bin/switchyard.new

# 暂停流量（如果 API 可用）
curl -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true' 2>/dev/null || echo "跳过流量暂停"

# 等待 2 秒让请求排空
sleep 2

# 停止服务
systemctl stop switchyard.service

# 备份当前版本和数据库
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p /var/backups/switchyard
cp /usr/local/bin/switchyard /var/backups/switchyard/switchyard-${TIMESTAMP}
cp /var/lib/switchyard/switchyard.db /var/backups/switchyard/switchyard-${TIMESTAMP}.db

# 部署新版本
mv /usr/local/bin/switchyard.new /usr/local/bin/switchyard
chmod +x /usr/local/bin/switchyard

# 启动服务
systemctl start switchyard.service

# 等待服务启动
sleep 3

# 检查服务状态
systemctl status switchyard.service

# 检查健康状态
curl -s http://127.0.0.1:8088/healthz | jq .

# 恢复流量
curl -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false'

# 确认版本
/usr/local/bin/switchyard version
DEPLOY_COMMANDS

echo ""
echo "现在连接到服务器："
echo "   ssh ${USER}@${SERVER}"
echo ""

# 自动打开 SSH 会话
ssh "${USER}@${SERVER}"
