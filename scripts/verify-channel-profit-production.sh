#!/usr/bin/env bash
# 渠道利润分析只读生产验收(P1/v1.4.0)。
# 只读: 不创建渠道、不改账务、不触发生成动作; 只查版本、鉴权闸门、索引与账单规模。
set -uo pipefail

DB="${DB_PATH:-/opt/switchyard/data/data.db}"
BASE="${BASE_URL:-http://127.0.0.1:8088}"
BIN="${BIN_PATH:-/opt/switchyard/bin/switchyard}"

echo "== 版本 =="
"$BIN" --version 2>/dev/null | head -5 || true

echo "== 健康 =="
curl -sS "$BASE/healthz" || echo "healthz failed"
echo

echo "== 匿名闸门(应为 401) =="
code=$(curl -sS -o /dev/null -w "%{http_code}" "$BASE/api/v1/channel/profit/1")
echo "GET /api/v1/channel/profit/1 anonymous=$code"

# 服务器上没有 sqlite3 CLI, 用 python3 只读连接(与 verify-webhooks-production.sh 一致)。
python3 - "$DB" <<'PY'
import sqlite3
import sys

path = sys.argv[1]
connection = sqlite3.connect('file:' + path + '?mode=ro', uri=True)

print('== 索引 ==')
for (name,) in connection.execute(
    "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='billing_records' ORDER BY name"
):
    print(' ', name)

print('== 账单规模 ==')
rows = connection.execute('SELECT COUNT(*) FROM billing_records').fetchone()[0]
print('  billing_rows=', rows)
PY

echo "CHANNEL_PROFIT_READONLY_VERIFY_DONE"
