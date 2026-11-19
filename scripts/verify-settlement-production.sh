#!/usr/bin/env bash
# 日结只读检查：不调用生成接口，不修改结算状态或资金。
set -euo pipefail
BASE="${BASE_URL:-http://127.0.0.1:8088}"
DB="${DB_PATH:-/opt/switchyard/data/data.db}"
health=$(curl --fail --silent --show-error --max-time 15 "$BASE/healthz")
python3 - "$health" <<'HEALTH'
import json, sys
health = json.loads(sys.argv[1])
assert health.get("status") == "ok" and health.get("db") == "ok", "unhealthy service or database"
print(json.dumps(health))
HEALTH
python3 - "$DB" <<'PY'
import datetime
import os
import sqlite3
import sys
from pathlib import Path
with sqlite3.connect(Path(sys.argv[1]).resolve().as_uri() + '?mode=ro', uri=True) as c:
    today = datetime.date.today()
    tables = {r[0] for r in c.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    for offset in range(7, 0, -1):
        date = today - datetime.timedelta(days=offset)
        key = date.strftime('%Y%m%d')
        start = date.isoformat()
        end = (date + datetime.timedelta(days=1)).isoformat()
        source = c.execute('SELECT COUNT(*), COUNT(DISTINCT owner_id) FROM billing_records WHERE owner_id > 0 AND created_at >= ? AND created_at < ?', (start, end)).fetchone()
        statements = c.execute('SELECT COUNT(*), COALESCE(SUM(request_count),0), COALESCE(SUM(gross_revenue),0), COALESCE(SUM(refund_deduction),0), COALESCE(SUM(net_revenue),0) FROM settlement_statements WHERE period=?', (key,)).fetchone()
        completed = None
        if 'settlement_runs' in tables:
            completed = c.execute('SELECT COUNT(*) FROM settlement_runs WHERE period=?', (key,)).fetchone()[0]
        if os.environ.get('REQUIRE_CHECKPOINTS') == '1':
            assert completed == 1, f'{key}: missing successful checkpoint'
        print('period=', key, 'billing_count=', source[0], 'owners=', source[1], 'statement_count=', statements[0], 'statement_requests=', statements[1], 'gross=', statements[2], 'refund=', statements[3], 'net=', statements[4], 'checkpoint=', completed)
    # 审核定稿的全字段摘要可在部署前后对比；不输出人员/备注内容。
    import hashlib
    audited = c.execute("SELECT * FROM settlement_statements WHERE status='audited' ORDER BY id").fetchall()
    print('audited_count=', len(audited), 'audited_sha256=', hashlib.sha256(repr(audited).encode()).hexdigest())
    print('checkpoint_tables=', ','.join(sorted(t for t in tables if 'settlement' in t)))
PY
printf 'SETTLEMENT_READONLY_VERIFY_DONE\n'
