#!/bin/bash
set -euo pipefail

SERVER="202.6.204.216"
USER="root"
PASSWORD="kLaRns0425"
BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"
LOCAL_BINARY="/srv/deepseek-harness/workspaces/octopus/build/switchyard-local"

echo "🚀 开始部署到 ${SERVER}"
echo ""

# 生成临时 SSH key（如果不存在）
if [ ! -f ~/.ssh/deploy_key ]; then
    echo "🔑 生成临时部署密钥..."
    ssh-keygen -t ed25519 -f ~/.ssh/deploy_key -N "" -C "deploy@temp"
fi

# 尝试使用密码将 SSH key 复制到服务器
echo "📋 设置 SSH key 认证..."
echo ""
echo "请手动执行以下命令设置 SSH key（需要输入密码 kLaRns0425）:"
echo ""
echo "ssh-copy-id -i ~/.ssh/deploy_key.pub root@202.6.204.216"
echo ""
read -p "设置完成后按回车继续..."

# 现在使用 SSH key 进行部署
echo ""
echo "📤 上传二进制文件..."
scp -i ~/.ssh/deploy_key "${LOCAL_BINARY}" "${USER}@${SERVER}:${BINARY_PATH}.new"
echo "✓ 上传完成"
echo ""

# 执行部署脚本
echo "🚢 执行部署..."
ssh -i ~/.ssh/deploy_key "${USER}@${SERVER}" 'bash -s' << 'ENDSSH'
set -e

BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"

echo "🔍 验证文件..."
sha256sum ${BINARY_PATH}.new

echo ""
echo "📊 当前服务状态:"
curl -s http://127.0.0.1:8088/healthz 2>/dev/null || echo "健康检查不可用"
echo ""

echo "⏸️  暂停流量..."
curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true' 2>/dev/null || true
sleep 2

echo "🛑 停止服务..."
systemctl stop ${SERVICE_NAME}
sleep 2

echo "💾 备份..."
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p ${BACKUP_DIR}
[ -f ${BINARY_PATH} ] && cp ${BINARY_PATH} ${BACKUP_DIR}/switchyard-${TIMESTAMP}
[ -f /var/lib/switchyard/switchyard.db ] && cp /var/lib/switchyard/switchyard.db ${BACKUP_DIR}/switchyard-${TIMESTAMP}.db
echo "✓ 已备份到 ${BACKUP_DIR}/switchyard-${TIMESTAMP}"

echo "🚢 部署新版本..."
mv ${BINARY_PATH}.new ${BINARY_PATH}
chmod +x ${BINARY_PATH}

echo "▶️  启动服务..."
systemctl start ${SERVICE_NAME}
sleep 3

echo "🔍 验证健康状态..."
HEALTHZ=$(curl -s http://127.0.0.1:8088/healthz)
echo "${HEALTHZ}"

STATUS=$(echo "${HEALTHZ}" | grep -o '"status":"ok"' || echo "")
if [ -z "${STATUS}" ]; then
    echo ""
    echo "❌ 健康检查失败！回滚..."
    systemctl stop ${SERVICE_NAME}
    cp ${BACKUP_DIR}/switchyard-${TIMESTAMP} ${BINARY_PATH}
    systemctl start ${SERVICE_NAME}
    exit 1
fi

echo "▶️  恢复流量..."
curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false' 2>/dev/null || true

echo ""
echo "🎉 部署成功！"
${BINARY_PATH} version
ENDSSH

echo ""
echo "✅ 部署完成！"
