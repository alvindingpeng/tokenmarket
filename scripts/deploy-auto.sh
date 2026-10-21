#!/bin/bash
# 自动化部署脚本
# 使用方法: DEPLOY_PASSWORD='your_password' bash scripts/deploy-auto.sh

set -euo pipefail

readonly SERVER="tt.xjj.se"
readonly USER="root"
readonly BINARY_PATH="/usr/local/bin/switchyard"
readonly SERVICE_NAME="switchyard.service"
readonly BACKUP_DIR="/var/backups/switchyard"
readonly LOCAL_BINARY="build/switchyard-local"

if [ -z "${DEPLOY_PASSWORD:-}" ]; then
    echo "❌ 请设置环境变量 DEPLOY_PASSWORD"
    echo "   用法: DEPLOY_PASSWORD='密码' bash scripts/deploy-auto.sh"
    exit 1
fi

if [ ! -f "${LOCAL_BINARY}" ]; then
    echo "❌ 找不到二进制文件: ${LOCAL_BINARY}"
    echo "   请先运行: bash scripts/build-local.sh"
    exit 1
fi

echo "🚀 开始自动部署到 ${SERVER}"
echo ""

# 计算本地文件哈希
SHA256_LOCAL=$(sha256sum "${LOCAL_BINARY}" | cut -d' ' -f1)
echo "📦 本地二进制: ${LOCAL_BINARY}"
echo "   SHA-256: ${SHA256_LOCAL}"
echo ""

# 辅助函数：执行远程命令
ssh_exec() {
    sshpass -p "${DEPLOY_PASSWORD}" ssh -o StrictHostKeyChecking=no "${USER}@${SERVER}" "$@"
}

# 辅助函数：上传文件
scp_upload() {
    sshpass -p "${DEPLOY_PASSWORD}" scp -o StrictHostKeyChecking=no "$@"
}

# 1. 检查服务状态
echo "📊 检查当前服务状态..."
ssh_exec "curl -s http://127.0.0.1:8088/healthz" || echo "   健康检查不可用"
echo ""

# 2. 上传二进制
echo "📤 上传二进制文件..."
scp_upload "${LOCAL_BINARY}" "${USER}@${SERVER}:${BINARY_PATH}.new"
echo "   ✓ 上传完成"
echo ""

# 3. 验证远程哈希
echo "🔍 验证文件完整性..."
SHA256_REMOTE=$(ssh_exec "sha256sum ${BINARY_PATH}.new | cut -d' ' -f1")
if [ "${SHA256_LOCAL}" != "${SHA256_REMOTE}" ]; then
    echo "❌ 哈希不匹配！"
    echo "   本地: ${SHA256_LOCAL}"
    echo "   远程: ${SHA256_REMOTE}"
    exit 1
fi
echo "   ✓ 哈希验证通过"
echo ""

# 4. 暂停流量
echo "⏸️  暂停流量..."
ssh_exec "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true'" 2>/dev/null || echo "   跳过（API 不可用）"
sleep 2
echo ""

# 5. 停止服务
echo "🛑 停止服务..."
ssh_exec "systemctl stop ${SERVICE_NAME}"
sleep 2
echo "   ✓ 服务已停止"
echo ""

# 6. 备份
echo "💾 备份当前版本..."
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
ssh_exec "mkdir -p ${BACKUP_DIR}"
ssh_exec "cp ${BINARY_PATH} ${BACKUP_DIR}/switchyard-${TIMESTAMP}" 2>/dev/null || echo "   无旧版本"
ssh_exec "cp /var/lib/switchyard/switchyard.db ${BACKUP_DIR}/switchyard-${TIMESTAMP}.db"
echo "   ✓ 已备份到 ${BACKUP_DIR}/switchyard-${TIMESTAMP}"
echo ""

# 7. 部署新版本
echo "🚢 部署新版本..."
ssh_exec "mv ${BINARY_PATH}.new ${BINARY_PATH}"
ssh_exec "chmod +x ${BINARY_PATH}"
echo "   ✓ 二进制已部署"
echo ""

# 8. 启动服务
echo "▶️  启动服务..."
ssh_exec "systemctl start ${SERVICE_NAME}"
sleep 3
echo "   ✓ 服务已启动"
echo ""

# 9. 验证健康状态
echo "🔍 验证健康状态..."
HEALTHZ=$(ssh_exec "curl -s http://127.0.0.1:8088/healthz")
echo "${HEALTHZ}" | jq . 2>/dev/null || echo "${HEALTHZ}"

STATUS=$(echo "${HEALTHZ}" | jq -r '.status' 2>/dev/null || echo "unknown")
if [ "${STATUS}" != "ok" ]; then
    echo ""
    echo "❌ 健康检查失败！正在回滚..."
    ssh_exec "systemctl stop ${SERVICE_NAME}"
    ssh_exec "cp ${BACKUP_DIR}/switchyard-${TIMESTAMP} ${BINARY_PATH}"
    ssh_exec "systemctl start ${SERVICE_NAME}"
    echo "   ✓ 已回滚"
    exit 1
fi
echo ""

# 10. 恢复流量
echo "▶️  恢复流量..."
ssh_exec "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false'" 2>/dev/null
echo ""

# 11. 确认版本
echo "🎉 部署成功！"
VERSION=$(ssh_exec "${BINARY_PATH} version")
echo ""
echo "${VERSION}"
echo ""
