set -e
cd /opt/switchyard/bin
[ -s switchyard.new ] || { echo "NO_NEW_BINARY"; exit 1; }
echo "== NEW BINARY =="
ls -lh switchyard.new
echo "== STOP SERVICE =="
systemctl stop switchyard
sleep 1
cp -f switchyard switchyard.prev-v12
chmod 755 switchyard.new
mv -f switchyard.new switchyard
echo "== START SERVICE =="
systemctl start switchyard
sleep 6
echo "== STATUS =="
systemctl is-active switchyard || true
systemctl status switchyard --no-pager | head -8 || true
echo "== HEALTH CHECK =="
curl -s --max-time 5 http://127.0.0.1:8088/health || echo "(no /health)"
curl -s --max-time 5 http://127.0.0.1:8088/api/v1/status | head -c 400 || echo "(no /api/v1/status)"
echo ""
echo "== STARTUP LOGS =="
journalctl -u switchyard --since "3 minutes ago" --no-pager | tail -70
echo "== SQLITE: probe columns =="
command -v sqlite3 >/dev/null 2>&1 && sqlite3 /opt/switchyard/data/data.db "PRAGMA table_info(channel_keys);" | grep -E 'probe' || echo "NO_SQLITE3_CLI_OR_COLS"
echo "== SQLITE: cost columns =="
command -v sqlite3 >/dev/null 2>&1 && sqlite3 /opt/switchyard/data/data.db "PRAGMA table_info(relay_requests);" | grep -E 'supply_cost|user_charge|profit' || echo "NO_SQLITE3_CLI_OR_COLS"
echo "== SQLITE: quality weight settings =="
command -v sqlite3 >/dev/null 2>&1 && sqlite3 /opt/switchyard/data/data.db "SELECT key, value FROM settings WHERE key LIKE 'quality_weight%';" || echo "NO_SQLITE3_CLI"
echo "DEPLOY_VERIFY_DONE"
