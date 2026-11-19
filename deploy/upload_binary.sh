#!/bin/bash
# 通过 ssh + base64 传输二进制（避免 scp）
set -e
BINARY="$1"
REMOTE="$2"
PASSWORD="$3"

[ -f "$BINARY" ] || { echo "Binary not found: $BINARY"; exit 1; }

SIZE=$(stat -c%s "$BINARY")
echo "Uploading $BINARY ($SIZE bytes)..."

# base64 编码并通过 ssh 传输
base64 "$BINARY" | python3 deploy/ssh_auto.py "$REMOTE" "$PASSWORD" /dev/stdin 300 <<'EOF'
base64 -d > /opt/switchyard/bin/switchyard.new
chmod +x /opt/switchyard/bin/switchyard.new
ls -lh /opt/switchyard/bin/switchyard.new
echo "UPLOAD_OK"
EOF
