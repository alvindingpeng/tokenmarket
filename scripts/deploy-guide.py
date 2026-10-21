#!/usr/bin/env python3
"""
纯 Python SSH 部署脚本 - 提供手动部署指南
"""

import sys
from pathlib import Path

SERVER = "202.6.204.216"
USER = "root"
PASSWORD = "kLaRns0425"
BINARY_PATH = "/usr/local/bin/switchyard"
SERVICE_NAME_VAR = "switchyard.service"
BACKUP_DIR = "/var/backups/switchyard"
LOCAL_BINARY = "/srv/deepseek-harness/workspaces/octopus/build/switchyard-local"

def main():
    print("🚀 Switchyard 部署指南")
    print(f"服务器: {SERVER}")
    print("")
    
    if not Path(LOCAL_BINARY).exists():
        print(f"❌ 找不到: {LOCAL_BINARY}")
        sys.exit(1)
    
    print("=" * 60)
    print("请按以下步骤手动部署")
    print("=" * 60)
    print("")
    print(f"密码: {PASSWORD}")
    print("")
    print("步骤 1: 上传二进制")
    print(f"scp {LOCAL_BINARY} {USER}@{SERVER}:{BINARY_PATH}.new")
    print("")
    print("步骤 2: 连接服务器并执行部署脚本")
    print(f"ssh {USER}@{SERVER}")

if __name__ == "__main__":
    main()
