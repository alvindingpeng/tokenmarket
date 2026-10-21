#!/usr/bin/env python3
"""
部署新版本到 tt.xjj.se 生产服务器

安全发布流程：
1. 检查服务状态和流量
2. 设置流量暂停标志
3. 等待活跃请求排空
4. 停止服务
5. 备份数据库
6. 部署新二进制
7. 验证健康检查
8. 恢复流量
"""

import subprocess
import sys
import time
import hashlib
import json
from pathlib import Path

SERVER = "tt.xjj.se"
USER = "root"
PASSWORD = "kLaRns0425"
SERVICE_NAME = "switchyard.service"
BINARY_PATH = "/usr/local/bin/switchyard"
BACKUP_DIR = "/var/backups/switchyard"

def run_ssh(command, check=True):
    """在远程服务器执行命令"""
    ssh_cmd = ["sshpass", "-p", PASSWORD, "ssh", f"{USER}@{SERVER}", command]
    result = subprocess.run(ssh_cmd, capture_output=True, text=True, check=check)
    return result

def check_service_status():
    """检查服务状态"""
    print("📊 检查服务状态...")
    result = run_ssh("curl -s http://127.0.0.1:8088/healthz")
    healthz = json.loads(result.stdout)
    
    print(f"  状态: {healthz.get('status', 'unknown')}")
    print(f"  数据库: {healthz.get('db', 'unknown')}")
    print(f"  活跃请求: {healthz.get('active_requests', 'unknown')}")
    print(f"  流量暂停: {healthz.get('traffic_paused', False)}")
    
    return healthz

def pause_traffic():
    """暂停流量"""
    print("⏸️  暂停流量...")
    run_ssh("curl -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true'")
    time.sleep(2)

def wait_for_drain(max_wait=30):
    """等待活跃请求排空"""
    print("⏳ 等待活跃请求排空...")
    start = time.time()
    
    while time.time() - start < max_wait:
        healthz = check_service_status()
        active = healthz.get('active_requests', 0)
        
        if active == 0:
            print("  ✓ 已排空")
            return True
        
        print(f"  等待中... (剩余 {active} 个请求)")
        time.sleep(2)
    
    print("  ⚠️  超时，但继续部署")
    return False

def stop_service():
    """停止服务"""
    print("🛑 停止服务...")
    run_ssh(f"systemctl stop {SERVICE_NAME}")
    time.sleep(2)
    
    result = run_ssh(f"systemctl is-active {SERVICE_NAME}", check=False)
    if result.stdout.strip() != "inactive":
        print("  ⚠️  服务未完全停止")
        return False
    
    print("  ✓ 服务已停止")
    return True

def backup_database():
    """备份数据库"""
    print("💾 备份数据库...")
    timestamp = time.strftime("%Y%m%d-%H%M%S")
    backup_file = f"{BACKUP_DIR}/switchyard-{timestamp}.db"
    
    run_ssh(f"mkdir -p {BACKUP_DIR}")
    run_ssh(f"cp /var/lib/switchyard/switchyard.db {backup_file}")
    run_ssh(f"sqlite3 {backup_file} 'PRAGMA integrity_check;'")
    
    print(f"  ✓ 已备份到 {backup_file}")
    return backup_file

def deploy_binary(local_path):
    """部署二进制文件"""
    print("📦 部署二进制...")
    
    # 计算本地文件哈希
    with open(local_path, 'rb') as f:
        local_hash = hashlib.sha256(f.read()).hexdigest()
    print(f"  本地 SHA-256: {local_hash}")
    
    # 上传
    scp_cmd = [
        "sshpass", "-p", PASSWORD,
        "scp", local_path, f"{USER}@{SERVER}:{BINARY_PATH}.new"
    ]
    subprocess.run(scp_cmd, check=True)
    
    # 验证远程哈希
    result = run_ssh(f"sha256sum {BINARY_PATH}.new")
    remote_hash = result.stdout.split()[0]
    print(f"  远程 SHA-256: {remote_hash}")
    
    if local_hash != remote_hash:
        raise Exception("哈希不匹配！上传失败")
    
    # 备份旧版本并替换
    run_ssh(f"cp {BINARY_PATH} {BINARY_PATH}.old")
    run_ssh(f"mv {BINARY_PATH}.new {BINARY_PATH}")
    run_ssh(f"chmod +x {BINARY_PATH}")
    
    print("  ✓ 二进制已部署")

def start_service():
    """启动服务"""
    print("▶️  启动服务...")
    run_ssh(f"systemctl start {SERVICE_NAME}")
    time.sleep(3)
    
    result = run_ssh(f"systemctl is-active {SERVICE_NAME}", check=False)
    if result.stdout.strip() != "active":
        print("  ❌ 服务启动失败")
        return False
    
    print("  ✓ 服务已启动")
    return True

def verify_health():
    """验证健康状态"""
    print("🔍 验证健康状态...")
    time.sleep(2)
    
    try:
        healthz = check_service_status()
        
        if healthz.get('status') != 'ok' or healthz.get('db') != 'ok':
            print("  ❌ 健康检查失败")
            return False
        
        print("  ✓ 健康检查通过")
        return True
    except Exception as e:
        print(f"  ❌ 健康检查失败: {e}")
        return False

def resume_traffic():
    """恢复流量"""
    print("▶️  恢复流量...")
    run_ssh("curl -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false'")
    time.sleep(1)
    
    healthz = check_service_status()
    if not healthz.get('traffic_paused', True):
        print("  ✓ 流量已恢复")
        return True
    
    print("  ⚠️  流量恢复失败")
    return False

def rollback():
    """回滚到旧版本"""
    print("🔄 回滚到旧版本...")
    run_ssh(f"systemctl stop {SERVICE_NAME}")
    run_ssh(f"mv {BINARY_PATH}.old {BINARY_PATH}")
    run_ssh(f"systemctl start {SERVICE_NAME}")
    time.sleep(3)
    
    if verify_health():
        resume_traffic()
        print("  ✓ 回滚成功")
    else:
        print("  ❌ 回滚后健康检查仍然失败！需要人工介入")

def main():
    if len(sys.argv) < 2:
        print("用法: python3 deploy.py <binary_path>")
        sys.exit(1)
    
    local_binary = sys.argv[1]
    if not Path(local_binary).exists():
        print(f"错误: 找不到文件 {local_binary}")
        sys.exit(1)
    
    print("🚀 开始部署到 tt.xjj.se")
    print(f"   二进制: {local_binary}")
    print()
    
    try:
        # 预检查
        check_service_status()
        
        # 暂停流量并排空
        pause_traffic()
        wait_for_drain()
        
        # 停止服务
        if not stop_service():
            raise Exception("服务停止失败")
        
        # 备份数据库
        backup_database()
        
        # 部署新版本
        deploy_binary(local_binary)
        
        # 启动服务
        if not start_service():
            rollback()
            sys.exit(1)
        
        # 验证健康
        if not verify_health():
            rollback()
            sys.exit(1)
        
        # 恢复流量
        if not resume_traffic():
            print("⚠️  流量恢复失败，但服务正常运行")
        
        print()
        print("✅ 部署成功！")
        
    except Exception as e:
        print(f"❌ 部署失败: {e}")
        print("尝试回滚...")
        rollback()
        sys.exit(1)

if __name__ == "__main__":
    main()
