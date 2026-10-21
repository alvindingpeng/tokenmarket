#!/bin/bash
# 一键部署脚本 - 通过 stdin 传递密码
set -euo pipefail

SERVER="202.6.204.216"
USER="root"
BINARY_PATH="/usr/local/bin/switchyard"
SERVICE_NAME="switchyard.service"
BACKUP_DIR="/var/backups/switchyard"
LOCAL_BINARY="/srv/deepseek-harness/workspaces/octopus/build/switchyard-local"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}🚀 Switchyard v0.40.0 自动部署${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# 检查本地二进制
if [ ! -f "${LOCAL_BINARY}" ]; then
    echo -e "${RED}❌ 找不到本地二进制: ${LOCAL_BINARY}${NC}"
    exit 1
fi

SHA256_LOCAL=$(sha256sum "${LOCAL_BINARY}" | cut -d' ' -f1)
echo -e "${YELLOW}📦 本地二进制:${NC}"
echo "   路径: ${LOCAL_BINARY}"
echo "   SHA-256: ${SHA256_LOCAL}"
echo ""

echo -e "${YELLOW}🎯 目标服务器:${NC}"
echo "   地址: ${SERVER}"
echo "   用户: ${USER}"
echo ""

# 创建临时 expect 脚本
EXPECT_SCRIPT=$(mktemp)
trap "rm -f ${EXPECT_SCRIPT}" EXIT

cat > "${EXPECT_SCRIPT}" << 'EOF'
#!/usr/bin/expect -f
set timeout 30
set password "kLaRns0425"
set server [lindex $argv 0]
set user [lindex $argv 1]
set action [lindex $argv 2]

if {$action == "upload"} {
    set local_file [lindex $argv 3]
    set remote_path [lindex $argv 4]
    
    spawn scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $local_file $user@$server:$remote_path
    expect {
        "password:" { send "$password\r"; exp_continue }
        "Password:" { send "$password\r"; exp_continue }
        eof
    }
} else {
    set command [lindex $argv 3]
    
    spawn ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null $user@$server $command
    expect {
        "password:" { send "$password\r"; exp_continue }
        "Password:" { send "$password\r"; exp_continue }
        eof
    }
}
EOF

chmod +x "${EXPECT_SCRIPT}"

# 检查 expect 是否可用
if ! command -v expect &> /dev/null; then
    echo -e "${RED}❌ 需要 expect 工具${NC}"
    echo ""
    echo "请手动部署，步骤："
    echo "1. scp ${LOCAL_BINARY} ${USER}@${SERVER}:${BINARY_PATH}.new"
    echo "2. ssh ${USER}@${SERVER}"
    echo "3. 执行 MANUAL_DEPLOY_GUIDE.md 中的部署脚本"
    exit 1
fi

echo -e "${YELLOW}📤 步骤 1/6: 上传二进制文件...${NC}"
"${EXPECT_SCRIPT}" "${SERVER}" "${USER}" upload "${LOCAL_BINARY}" "${BINARY_PATH}.new"
echo -e "${GREEN}   ✓ 上传完成${NC}"
echo ""

echo -e "${YELLOW}🔍 步骤 2/6: 验证文件完整性...${NC}"
SHA256_REMOTE=$("${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "sha256sum ${BINARY_PATH}.new | cut -d' ' -f1" | tail -1)
if [ "${SHA256_LOCAL}" != "${SHA256_REMOTE}" ]; then
    echo -e "${RED}❌ 哈希不匹配！${NC}"
    echo "   本地: ${SHA256_LOCAL}"
    echo "   远程: ${SHA256_REMOTE}"
    exit 1
fi
echo -e "${GREEN}   ✓ 哈希验证通过${NC}"
echo ""

echo -e "${YELLOW}⏸️  步骤 3/6: 准备部署（暂停流量、停止服务）...${NC}"
"${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true' 2>/dev/null || true; sleep 2; systemctl stop ${SERVICE_NAME}; sleep 2" > /dev/null 2>&1
echo -e "${GREEN}   ✓ 服务已停止${NC}"
echo ""

echo -e "${YELLOW}💾 步骤 4/6: 备份当前版本...${NC}"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
"${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "mkdir -p ${BACKUP_DIR}; [ -f ${BINARY_PATH} ] && cp ${BINARY_PATH} ${BACKUP_DIR}/switchyard-${TIMESTAMP} || true; [ -f /var/lib/switchyard/switchyard.db ] && cp /var/lib/switchyard/switchyard.db ${BACKUP_DIR}/switchyard-${TIMESTAMP}.db || true" > /dev/null 2>&1
echo -e "${GREEN}   ✓ 已备份到 ${BACKUP_DIR}/switchyard-${TIMESTAMP}${NC}"
echo ""

echo -e "${YELLOW}🚢 步骤 5/6: 部署新版本并启动服务...${NC}"
"${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "mv ${BINARY_PATH}.new ${BINARY_PATH}; chmod +x ${BINARY_PATH}; systemctl start ${SERVICE_NAME}; sleep 3" > /dev/null 2>&1
echo -e "${GREEN}   ✓ 服务已启动${NC}"
echo ""

echo -e "${YELLOW}🔍 步骤 6/6: 验证部署...${NC}"
sleep 2
HEALTHZ=$("${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "curl -s http://127.0.0.1:8088/healthz" | grep -v "^spawn\|^password:\|^Warning" | tail -1)
echo "${HEALTHZ}"

if echo "${HEALTHZ}" | grep -q '"status":"ok"' && echo "${HEALTHZ}" | grep -q '"db":"ok"'; then
    echo ""
    echo -e "${GREEN}   ✓ 健康检查通过${NC}"
    
    # 恢复流量
    "${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false' 2>/dev/null || true" > /dev/null 2>&1
    
    # 获取版本信息
    VERSION=$("${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "${BINARY_PATH} version" | grep -v "^spawn\|^password:\|^Warning" | tail -3)
    
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}🎉 部署成功！${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "${VERSION}"
    echo ""
    echo "备份: ${BACKUP_DIR}/switchyard-${TIMESTAMP}"
else
    echo ""
    echo -e "${RED}❌ 健康检查失败！正在回滚...${NC}"
    "${EXPECT_SCRIPT}" "${SERVER}" "${USER}" exec "systemctl stop ${SERVICE_NAME}; cp ${BACKUP_DIR}/switchyard-${TIMESTAMP} ${BINARY_PATH}; systemctl start ${SERVICE_NAME}" > /dev/null 2>&1
    echo -e "${YELLOW}   ✓ 已回滚${NC}"
    exit 1
fi
