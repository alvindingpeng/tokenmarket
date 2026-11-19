#!/usr/bin/env bash
# 渠道利润分析只读验收。任一检查失败立即退出，避免输出误导性的成功标记。
set -euo pipefail

DB="${DB_PATH:-/opt/switchyard/data/data.db}"
BASE="${BASE_URL:-http://127.0.0.1:8088}"
BIN="${BIN_PATH:-/opt/switchyard/bin/switchyard}"

echo "== 版本 =="
"$BIN" version

echo "== 健康 =="
health=$(curl --fail --silent --show-error --max-time 15 "$BASE/healthz")
python3 - "$health" <<'PY'
import json
import sys
health = json.loads(sys.argv[1])
assert health.get('status') == 'ok', 'service unhealthy'
assert health.get('db') == 'ok', 'database unhealthy'
print(json.dumps(health))
PY

echo "== 匿名闸门(应为 401) =="
code=$(curl --silent --show-error --max-time 15 -o /dev/null -w "%{http_code}" "$BASE/api/v1/channel/profit/1")
echo "GET /api/v1/channel/profit/1 anonymous=$code"
test "$code" = 401

# 使用只读连接；必须核对索引列和顺序，而不只是输出索引名称。
python3 - "$DB" <<'PY'
import sqlite3
import sys
from pathlib import Path

with sqlite3.connect(Path(sys.argv[1]).resolve().as_uri() + '?mode=ro', uri=True) as connection:
    print('== 索引 ==')
    for name, expected in (
        ('idx_billing_records_channel_created', ['channel_id', 'created_at']),
        ('idx_billing_records_user_created', ['user_id', 'created_at']),
    ):
        columns = [row[2] for row in connection.execute('PRAGMA index_info("' + name + '")')]
        assert columns == expected, f'{name}: expected {expected}, got {columns}'
        print(name, columns)
    rows = connection.execute('SELECT COUNT(*) FROM billing_records').fetchone()[0]
    print('billing_rows=', rows)
PY

echo "CHANNEL_PROFIT_READONLY_VERIFY_DONE"
