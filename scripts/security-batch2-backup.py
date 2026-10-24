#!/usr/bin/env python3
"""Create an offline, consistent recovery point on the production host."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument('--root', type=Path, default=Path('/opt/switchyard'))
a = p.parse_args()
root = a.root.resolve()
state = subprocess.run(['systemctl', 'show', 'switchyard.service', '-p', 'MainPID', '--value'], check=True, capture_output=True, text=True).stdout.strip()
if state != '0':
    raise SystemExit('Stop switchyard.service before creating the recovery point')
os.umask(0o077)
dest = root / 'data/releases' / time.strftime('%Y%m%d-%H%M%S-security-batch2')
dest.mkdir(parents=True, mode=0o700, exist_ok=False)
shutil.copy2(root / 'bin/switchyard', dest / 'switchyard.previous')
shutil.copy2(root / 'data/config.json', dest / 'config.json')
source = sqlite3.connect((root / 'data/data.db').as_uri() + '?mode=ro', uri=True)
target = sqlite3.connect(dest / 'pre-upgrade.db')
try:
    source.backup(target)
    if target.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
        raise SystemExit('Backup integrity check failed')
    if target.execute('PRAGMA foreign_key_check').fetchone() is not None:
        raise SystemExit('Backup foreign key check failed')
finally:
    target.close()
    source.close()
for file in dest.iterdir():
    file.chmod(0o600)
unit = subprocess.run(['systemctl', 'cat', 'switchyard.service'], check=True, capture_output=True, text=True).stdout
(dest / 'unit.txt').write_text(unit)
hashes = {}
for name in ('pre-upgrade.db', 'switchyard.previous', 'config.json', 'unit.txt'):
    hash_state = hashlib.sha256()
    with (dest / name).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            hash_state.update(block)
    hashes[name] = hash_state.hexdigest()
metadata = {'database_sha256': hashes['pre-upgrade.db'], 'file_sha256': hashes, 'result': 'backup-created', 'database_restored': False}
(dest / 'backup.json').write_text(json.dumps(metadata, indent=2) + '\n')
print(json.dumps({'recovery_directory': str(dest), **metadata}))
