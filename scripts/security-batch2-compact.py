#!/usr/bin/env python3
"""Offline SQLite page/WAL compaction after verified credential migration.
Does not erase historical backups or guarantee physical media secure erasure.
"""
import argparse
import json
import sqlite3
import subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument("database", type=Path)
p.add_argument("--service-stopped", action="store_true", required=True)
a = p.parse_args()
state = subprocess.run(["systemctl", "show", "switchyard.service", "-p", "MainPID", "--value"], check=True, capture_output=True, text=True).stdout.strip()
if state != "0":
    raise SystemExit("Stop switchyard.service before compacting")
c = sqlite3.connect(a.database.resolve().as_uri() + "?mode=rw", uri=True, timeout=5)
try:
    columns = {r[1] for r in c.execute("PRAGMA table_info(api_keys)")}
    if "api_key" in columns or "key_hash" not in columns:
        raise SystemExit("Refusing to compact before API Key migration")
    if c.execute("SELECT count(*) FROM channel_keys WHERE key IS NOT NULL AND key != '' AND key NOT LIKE 'sysec$1$%'").fetchone()[0]:
        raise SystemExit("Refusing to compact while channel credentials remain plaintext")
    if c.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
        raise SystemExit("Integrity check failed")
    if c.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()[0] != 0:
        raise SystemExit("WAL checkpoint is busy")
    c.execute("PRAGMA secure_delete=ON")
    c.execute("VACUUM")
    if c.execute("PRAGMA wal_checkpoint(TRUNCATE)").fetchone()[0] != 0:
        raise SystemExit("Final WAL checkpoint is busy")
    if c.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
        raise SystemExit("Post-compaction integrity check failed")
    print(json.dumps({"compacted": True, "integrity": "ok", "physical_media_secure_erasure": False}))
finally:
    c.close()
