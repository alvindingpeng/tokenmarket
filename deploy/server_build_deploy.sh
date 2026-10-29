#!/bin/bash
set -e
export PATH="/usr/local/go/bin:$PATH"
export GOPROXY=https://goproxy.cn,direct

cd /opt/switchyard/src/switchyard

echo "== GIT PULL =="
git fetch origin master
git reset --hard origin/master
git log --oneline -3

echo ""
echo "== BUILD FRONTEND =="
cd web
pnpm run build 2>&1 | tail -15

echo ""
echo "== BUILD BACKEND =="
cd ..
bash scripts/build-local.sh -o /opt/switchyard/bin/switchyard.new 2>&1 | tail -20

echo ""
echo "== VERIFY =="
ls -lh /opt/switchyard/bin/switchyard.new
/opt/switchyard/bin/switchyard.new version || true

echo ""
echo "== STOP SERVICE =="
systemctl stop switchyard

echo "== BACKUP =="
cp -f /opt/switchyard/bin/switchyard /opt/switchyard/bin/switchyard.prev-v12

echo "== REPLACE =="
mv -f /opt/switchyard/bin/switchyard.new /opt/switchyard/bin/switchyard
chmod +x /opt/switchyard/bin/switchyard

echo "== START SERVICE =="
systemctl start switchyard
sleep 5

echo "== STATUS =="
systemctl is-active switchyard
curl -s http://127.0.0.1:8088/healthz | head -20

echo ""
echo "== LOGS (last 30 lines) =="
journalctl -u switchyard --since "1 minute ago" --no-pager | tail -30

echo ""
echo "DEPLOY_DONE"
