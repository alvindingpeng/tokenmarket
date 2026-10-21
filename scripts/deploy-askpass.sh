#!/bin/bash
set -euo pipefail

export DISPLAY=:0
export SSH_ASKPASS="$(pwd)/scripts/askpass.sh"
export SSH_ASKPASS_REQUIRE=force

SERVER="202.6.204.216"
USER="root"
BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"
LOCAL_BINARY="build/switchyard-local"

echo "🚀 开始部署..."
echo ""

# 使用 setsid 强制使用 SSH_ASKPASS
echo "📤 上传二进制..."
setsid ssh -o StrictHostKeyChecking=no "${USER}@${SERVER}" "cat > ${BINARY_PATH}.new" < "${LOCAL_BINARY}"
echo "✓ 上传完成"
echo ""

echo "🚢 执行部署..."
setsid ssh -o StrictHostKeyChecking=no "${USER}@${SERVER}" 'bash -s' << 'ENDSSH'
set -e

BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"

echo "停止服务..."
systemctl stop ${SERVICE_NAME}

echo "备份..."
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p ${BACKUP_DIR}
[ -f ${BINARY_PATH} ] && cp ${BINARY_PATH} ${BACKUP_DIR}/switchyard-${TIMESTAMP}

echo "部署..."
mv ${BINARY_PATH}.new ${BINARY_PATH}
chmod +x ${BINARY_PATH}

echo "启动..."
systemctl start ${SERVICE_NAME}
sleep 3

echo "验证..."
curl -s http://127.0.0.1:8088/healthz

${BINARY_PATH} version
ENDSSH

echo ""
echo "✅ 部署完成！"
