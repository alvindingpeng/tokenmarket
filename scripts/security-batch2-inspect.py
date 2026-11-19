#!/usr/bin/env python3
"""Read-only post-migration checks; prints counts only, never credentials."""
import argparse
import json
import sqlite3
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('database', type=Path)
p.add_argument('--require-hashed-api-keys', action='store_true')
a = p.parse_args()
c = sqlite3.connect(a.database.resolve().as_uri() + '?mode=ro', uri=True)
result = {'integrity': c.execute('PRAGMA quick_check').fetchone()[0]}
for table in ('users', 'api_keys', 'channel_keys', 'billing_records', 'balance_ledgers'):
    result[table] = c.execute('SELECT COUNT(*) FROM ' + table).fetchone()[0]
result['foreign_key_violations'] = len(c.execute('PRAGMA foreign_key_check').fetchall())
result['negative_frozen'] = c.execute('SELECT COUNT(*) FROM users WHERE frozen < -0.000000001').fetchone()[0]
if a.require_hashed_api_keys:
    columns = {row[1] for row in c.execute('PRAGMA table_info(api_keys)')}
    result['api_key_plaintext_column'] = 'api_key' in columns
    result['api_key_hash_schema'] = 'key_hash' in columns
    result['invalid_api_key_hashes'] = 0
    result['duplicate_api_key_hashes'] = 0
    if 'key_hash' in columns:
        result['invalid_api_key_hashes'] = c.execute(
            "SELECT COUNT(*) FROM api_keys WHERE key_hash IS NULL OR length(key_hash) != 64 OR key_hash GLOB '*[^0-9a-f]*'"
        ).fetchone()[0]
        result['duplicate_api_key_hashes'] = c.execute(
            'SELECT count(*) FROM (SELECT key_hash FROM api_keys GROUP BY key_hash HAVING count(*) > 1)'
        ).fetchone()[0]
print(json.dumps(result, ensure_ascii=False))
if a.require_hashed_api_keys and (result['api_key_plaintext_column'] or not result['api_key_hash_schema'] or result['invalid_api_key_hashes'] or result['duplicate_api_key_hashes']):
    raise SystemExit(1)
if result['integrity'] != 'ok' or result['foreign_key_violations'] or result['negative_frozen']:
    raise SystemExit(1)
