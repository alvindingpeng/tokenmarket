#!/usr/bin/env python3
"""
自动化部署脚本 - 使用 paramiko 进行 SSH 连接
"""

import sys
import time
import hashlib
from pathlib import Path

try:
    import paramiko
except ImportError:
    print("❌ 需要安装 paramiko: pip3 install paramiko")
    sys.exit(1)

SERVER = "tt.xjj.se"
USER = "root"
PASSWORD = "kLaRns0425"
PORT = 22
BINARY_PATH = "/usr/local/bin/switchyard"
SERVICE_NAME = "switchyard.service"
BACKUP_DIR = "/var/backups/switchyard"
LOCAL_BINARY = "/srv/deepseek-harness/workspaces/octopus/build/switchyard-local"

def ssh_exec(ssh, command):
    """执行 SSH 命令并返回输出"""
    stdin, stdout, stderr = ssh.exec_command(command)
    exit_code = stdout.channel.recv_exit_status()
    output = stdout.read().decode('utf-8')
    error = stderr.read().decode('utf-8')
    return exit_code, output, error

def main():
    if not Path(LOCAL_BINARY).exists():
        print(f"❌ 找不到本地二进制: {LOCAL_BINARY}")
        sys.exit(1)
    
    # 计算本地哈希
    with open(LOCAL_BINARY, 'rb') as f:
        sha256_local = hashlib.sha256(f.read()).hexdigest()
    
    print("🚀 开始部署到", SERVER)
    print()
    print("📦 本地二进制:")
    print(f"   路径: {LOCAL_BINARY}")
    print(f"   SHA-256: {sha256_local}")
    print()
    
    # 连接 SSH
    print("🔌 连接服务器...")
    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    
    try:
        ssh.connect(SERVER, PORT, USER, PASSWORD, timeout=10)
        print("   ✓ 已连接")
        print()
    except Exception as e:
        print(f"❌ 连接失败: {e}")
        sys.exit(1)
    
    try:
        # 1. 检查服务状态
        print("📊 检查服务状态...")
        code, out, err = ssh_exec(ssh, "curl -s http://127.0.0.1:8088/healthz")
        if code == 0:
            print(out)
        print()
        
        # 2. 上传二进制
        print("📤 上传二进制...")
        sftp = ssh.open_sftp()
        sftp.put(LOCAL_BINARY, f"{BINARY_PATH}.new")
        sftp.close()
        print("   ✓ 上传完成")
        print()
        
        # 3. 验证哈希
        print("🔍 验证文件完整性...")
        code, out, err = ssh_exec(ssh, f"sha256sum {BINARY_PATH}.new")
        sha256_remote = out.split()[0]
        if sha256_local != sha256_remote:
            print(f"❌ 哈希不匹配！")
            print(f"   本地: {sha256_local}")
            print(f"   远程: {sha256_remote}")
            sys.exit(1)
        print("   ✓ 哈希验证通过")
        print()
        
        # 4. 暂停流量
        print("⏸️  暂停流量...")
        ssh_exec(ssh, "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true'")
        time.sleep(2)
        print()
        
        # 5. 停止服务
        print("🛑 停止服务...")
        ssh_exec(ssh, f"systemctl stop {SERVICE_NAME}")
        time.sleep(2)
        print("   ✓ 服务已停止")
        print()
        
        # 6. 备份
        print("💾 备份...")
        timestamp = time.strftime("%Y%m%d-%H%M%S")
        ssh_exec(ssh, f"mkdir -p {BACKUP_DIR}")
        ssh_exec(ssh, f"cp {BINARY_PATH} {BACKUP_DIR}/switchyard-{timestamp} 2>/dev/null || true")
        ssh_exec(ssh, f"cp /var/lib/switchyard/switchyard.db {BACKUP_DIR}/switchyard-{timestamp}.db")
        print(f"   ✓ 已备份到 {BACKUP_DIR}/switchyard-{timestamp}")
        print()
        
        # 7. 部署
        print("🚢 部署新版本...")
        ssh_exec(ssh, f"mv {BINARY_PATH}.new {BINARY_PATH}")
        ssh_exec(ssh, f"chmod +x {BINARY_PATH}")
        print("   ✓ 二进制已部署")
        print()
        
        # 8. 启动服务
        print("▶️  启动服务...")
        ssh_exec(ssh, f"systemctl start {SERVICE_NAME}")
        time.sleep(3)
        print("   ✓ 服务已启动")
        print()
        
        # 9. 验证健康
        print("🔍 验证健康状态...")
        code, out, err = ssh_exec(ssh, "curl -s http://127.0.0.1:8088/healthz")
        print(out)
        
        if '"status":"ok"' not in out or '"db":"ok"' not in out:
            print()
            print("❌ 健康检查失败！正在回滚...")
            ssh_exec(ssh, f"systemctl stop {SERVICE_NAME}")
            ssh_exec(ssh, f"cp {BACKUP_DIR}/switchyard-{timestamp} {BINARY_PATH}")
            ssh_exec(ssh, f"systemctl start {SERVICE_NAME}")
            print("   ✓ 已回滚")
            sys.exit(1)
        print()
        
        # 10. 恢复流量
        print("▶️  恢复流量...")
        ssh_exec(ssh, "curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false'")
        print()
        
        # 11. 确认版本
        print("🎉 部署成功！")
        code, out, err = ssh_exec(ssh, f"{BINARY_PATH} version")
        print()
        print(out)
        
    except Exception as e:
        print(f"❌ 部署失败: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
    finally:
        ssh.close()

if __name__ == "__main__":
    main()
