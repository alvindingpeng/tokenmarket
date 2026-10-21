#!/bin/bash
# 一次性 SSH key 设置脚本
# 需要手动执行一次来建立无密码登录

PUBLIC_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGngZwPpaFxQuHW9NK1lL93i2HHdFfQTRxj/GXD48S3a dsh@deployment"
SERVER="tt.xjj.se"
USER="root"

echo "🔑 设置 SSH key 认证到 ${SERVER}"
echo ""
echo "请运行以下命令（需要输入密码 kLaRns0425）:"
echo ""
echo "ssh-copy-id -i ~/.ssh/id_ed25519.pub ${USER}@${SERVER}"
echo ""
echo "或者手动添加公钥到服务器："
echo ""
echo "ssh ${USER}@${SERVER}"
echo "mkdir -p ~/.ssh && chmod 700 ~/.ssh"
echo "echo '${PUBLIC_KEY}' >> ~/.ssh/authorized_keys"
echo "chmod 600 ~/.ssh/authorized_keys"
echo ""
