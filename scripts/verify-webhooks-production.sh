#!/usr/bin/env bash
# Read-only production verification; never creates or sends a webhook.
set -euo pipefail
cd /opt/switchyard/src/switchyard
printf 'commit='; git rev-parse --short HEAD
systemctl is-active switchyard
curl --fail --silent --show-error http://127.0.0.1:8088/healthz
printf '\n'
for entry in 'GET list' 'POST ' 'PUT 1' 'DELETE 1' 'POST 1/test' 'GET 1/logs'; do
    method="${entry%% *}"
    suffix="${entry#* }"
    url="http://127.0.0.1:8088/api/v1/webhook"
    if [ -n "$suffix" ]; then url="$url/$suffix"; fi
    code=$(curl --silent --show-error -o /dev/null -w '%{http_code}' -X "$method" "$url")
    printf '%s %s anonymous=%s\n' "$method" "$suffix" "$code"
    test "$code" = 401
done
python3 - <<'PY'
import sqlite3
connection = sqlite3.connect('file:/opt/switchyard/data/data.db?mode=ro', uri=True)
names = {row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")}
assert 'webhooks' in names, 'webhook migration missing'
related = sorted(name for name in names if name.startswith('webhook'))
print('webhook tables:', ', '.join(related))
assert len(related) >= 2, 'durable delivery table missing'
# No row contents, URLs, secrets, or payloads are printed.
for table in related:
    print(table, 'rows=', connection.execute('SELECT COUNT(*) FROM "' + table + '"').fetchone()[0])
connection.close()
PY
printf 'WEBHOOK_READONLY_VERIFY_DONE\n'
