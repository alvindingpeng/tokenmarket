#!/usr/bin/env python3
"""Read-only legacy/migrated comparison. Never prints credentials or fingerprints."""
import argparse
import hashlib
import json
import sqlite3
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument("before", type=Path)
p.add_argument("after", type=Path)
a = p.parse_args()
def connect(path):
    conn = sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn
before, after = connect(a.before), connect(a.after)
checks = {}
for table in ("users", "api_keys", "channel_keys", "billing_records", "balance_ledgers", "balance_reservations", "stats_api_keys"):
    exists = before.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)).fetchone()
    if not exists:
        continue
    oldcols = [r[1] for r in before.execute("PRAGMA table_info(" + table + ")")]
    newcols = {r[1] for r in after.execute("PRAGMA table_info(" + table + ")")}
    excluded = {"api_key", "key_hash", "key_prefix", "key_last4"} if table == "api_keys" else {"key"} if table == "channel_keys" else set()
    columns = [c for c in oldcols if c not in excluded]
    checks[table + "_columns_preserved"] = all(c in newcols for c in columns)
    if not checks[table + "_columns_preserved"]:
        continue
    selected = ",".join('"' + c + '"' for c in columns)
    oldrows = [tuple(r) for r in before.execute("SELECT " + selected + " FROM " + table)]
    newrows = [tuple(r) for r in after.execute("SELECT " + selected + " FROM " + table)]
    # repr sorting handles nullable columns without leaking data to output.
    checks[table + "_rows_preserved"] = sorted(oldrows, key=repr) == sorted(newrows, key=repr)
checks["api_key_plaintext_column_removed"] = "api_key" not in {r[1] for r in after.execute("PRAGMA table_info(api_keys)")}
newkeys = {r["id"]: r for r in after.execute("SELECT id,key_hash FROM api_keys")}
checks["legacy_api_key_fingerprints_match"] = all(
    r["id"] in newkeys and hashlib.sha256(r["api_key"].strip().encode()).hexdigest() == newkeys[r["id"]]["key_hash"]
    for r in before.execute("SELECT id,api_key FROM api_keys")
)
oldsettings = {r["key"]: r["value"] for r in before.execute("SELECT key,value FROM settings")}
newsettings = {r["key"]: r["value"] for r in after.execute("SELECT key,value FROM settings")}
checks["settings_keys_preserved"] = oldsettings.keys() == newsettings.keys()
checks["settings_changed_only_to_envelopes"] = all(k in newsettings and (v == newsettings[k] or newsettings[k].startswith("sysec$1$")) for k,v in oldsettings.items())
checks["channel_secrets_enveloped"] = all(not r[0] or r[0].startswith("sysec$1$") for r in after.execute("SELECT key FROM channel_keys"))
print(json.dumps(checks, sort_keys=True))
raise SystemExit(0 if all(checks.values()) else 1)
