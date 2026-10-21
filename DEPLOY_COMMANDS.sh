#!/bin/bash
set -e

# 配置
BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"

echo "🔍 验证上传的文件..."
sha256sum ${BINARY_PATH}.new
echo ""

echo "📊 当前服务状态:"
systemctl status ${SERVICE_NAME} --no-pager | head -5 || true
curl -s http://127.0.0.1:8088/healthz 2>/dev/null || echo "健康检查不可用"
echo ""

echo "⏸️  停止服务..."
systemctl stop ${SERVICE_NAME}
sleep 2
echo "✓ 服务已停止"
echo ""

echo "💾 备份..."
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p ${BACKUP_DIR}
[ -f ${BINARY_PATH} ] && cp ${BINARY_PATH} ${BACKUP_DIR}/switchyard-${TIMESTAMP} && echo "✓ 二进制已备份"
[ -f /var/lib/switchyard/switchyard.db ] && cp /var/lib/switchyard/switchyard.db ${BACKUP_DIR}/switchyard-${TIMESTAMP}.db && echo "✓ 数据库已备份"
echo "✓ 备份位置: ${BACKUP_DIR}/switchyard-${TIMESTAMP}"
echo ""

echo "🚢 部署新版本..."
mv ${BINARY_PATH}.new ${BINARY_PATH}
chmod +x ${BINARY_PATH}
echo "✓ 二进制已部署"
echo ""

echo "▶️  启动服务..."
systemctl start ${SERVICE_NAME}
sleep 3
echo "✓ 服务已启动"
echo ""

echo "🔍 验证健康状态..."
HEALTHZ=$(curl -s http://127.0.0.1:8088/healthz)
echo "${HEALTHZ}" | jq . 2>/dev/null || echo "${HEALTHZ}"

STATUS=$(echo "${HEALTHZ}" | jq -r '.status' 2>/dev/null || echo "unknown")
DB_STATUS=$(echo "${HEALTHZ}" | jq -r '.db' 2>/dev/null || echo "unknown")

if [ "${STATUS}" != "ok" ] || [ "${DB_STATUS}" != "ok" ]; then
    echo ""
    echo "❌ 健康检查失败！正在回滚..."
    systemctl stop ${SERVICE_NAME}
    cp ${BACKUP_DIR}/switchyard-${TIMESTAMP} ${BINARY_PATH}
    systemctl start ${SERVICE_NAME}
    echo "✓ 已回滚"
    exit 1
fi
echo ""

echo "🎉 部署成功！"
${BINARY_PATH} version
echo ""
echo "查看日志: journalctl -u ${SERVICE_NAME} -f"
