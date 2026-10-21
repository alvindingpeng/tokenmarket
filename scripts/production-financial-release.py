#!/usr/bin/env python3
"""Run on the authorized production host as root. No embedded SSH credentials.
Creates a stopped-service SQLite backup; never auto-rolls back an incompatible binary.
Does not correct balances or regenerate statements. Requires an approved candidate.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time
import urllib.request
from contextlib import closing

BIN = Path('/opt/switchyard/bin/switchyard')
CONFIG = Path('/opt/switchyard/data/config.json')
DATABASE = Path('/opt/switchyard/data/data.db')
SERVICE = 'switchyard'


def sha256(path):
    h = hashlib.sha256()
    with open(path, 'rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout.strip()


def health():
    with urllib.request.urlopen('http://127.0.0.1:8088/healthz', timeout=5) as response:
        body = json.load(response)
    return body if body.get('status') == 'ok' and body.get('db') == 'ok' else None


def await_health():
    for _ in range(30):
        try:
            result = health()
            if result and command('systemctl', 'is-active', SERVICE) == 'active':
                return result
        except Exception:
            pass
        time.sleep(1)
    raise RuntimeError('service health did not recover within 30 attempts')


def audit(script, output):
    result = subprocess.run(['python3', str(script), '--database', str(DATABASE)],
                            check=True, capture_output=True, text=True)
    data = json.loads(result.stdout)
    output.write_text(json.dumps(data, ensure_ascii=False, indent=2))
    return {name: len(data[name]) for name in (
        'negative_holds', 'duplicate_billing_requests', 'duplicate_terminal_ledgers',
        'billing_split_mismatches', 'hold_mismatches')}


def recovery_holds(database):
    with closing(sqlite3.connect(database.as_uri() + '?mode=ro', uri=True)) as connection:
        exists = connection.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='billing_settlement_tasks'").fetchone()
        if not exists:
            return 0
        return connection.execute("SELECT COUNT(*) FROM billing_settlement_tasks WHERE status != 'completed'").fetchone()[0]


def leave_failed_install_stopped(metadata):
    command('systemctl', 'stop', SERVICE)
    metadata['result'] = 'failed-after-install; service stopped; manual compatible recovery required'
    metadata['rollback_blocked'] = 'old binary cannot safely interpret recovery tasks; database never overwritten'


def require_drained():
    current = health()
    if not current or current.get('active_requests') != 0:
        raise RuntimeError('active requests must be zero; stop new traffic and drain before release')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--candidate', type=Path, required=True)
    p.add_argument('--sha256', required=True)
    p.add_argument('--audit-script', type=Path, required=True)
    p.add_argument('--traffic-paused', action='store_true', help='operator confirms ingress paused and drained')
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit('requires explicitly authorized root execution')
    if not args.traffic_paused:
        raise SystemExit('pause upstream ingress and pass --traffic-paused; health alone cannot prevent new requests')
    os.umask(0o077)
    if sha256(args.candidate) != args.sha256:
        raise SystemExit('candidate checksum mismatch')
    config = json.loads(CONFIG.read_text())
    dbconf = config.get('database', {})
    if dbconf.get('type', 'sqlite') != 'sqlite':
        raise SystemExit('this release procedure requires SQLite')
    dbpath = Path(dbconf.get('path', 'data/data.db'))
    if not dbpath.is_absolute():
        dbpath = Path('/opt/switchyard') / dbpath
    if dbpath.resolve() != DATABASE.resolve():
        raise SystemExit('production database path differs from verified target')
    if not health():
        raise SystemExit('pre-release health failed')
    unit = command('systemctl', 'show', SERVICE, '-p', 'ExecStart', '-p', 'WorkingDirectory')
    if str(BIN) not in unit or 'WorkingDirectory=/opt/switchyard' not in unit:
        raise SystemExit('service identity mismatch')
    release = Path('/opt/switchyard/data/releases') / time.strftime('%Y%m%d-%H%M%S-financial')
    release.mkdir(parents=True, mode=0o700, exist_ok=False)
    metadata = {'candidate_sha256': args.sha256, 'unit': unit,
                'old_version': command(str(BIN), 'version'),
                'candidate_version': command(str(args.candidate.resolve()), 'version'),
                'historical_corrections_applied': False}
    (release / 'unit.txt').write_text(command('systemctl', 'cat', SERVICE))
    shutil.copy2(CONFIG, release / 'config.json')
    shutil.copy2(BIN, release / 'switchyard.previous')
    metadata['previous_sha256'] = sha256(release / 'switchyard.previous')
    stopped = False
    installed = False
    try:
        require_drained()
        command('systemctl', 'stop', SERVICE)
        stopped = True
        if command('systemctl', 'show', SERVICE, '-p', 'MainPID', '--value') != '0':
            raise RuntimeError('service still owns a running process')
        # Backup API incorporates WAL atomically; never copy only the live main file.
        backup = release / 'data.db'
        with closing(sqlite3.connect(DATABASE.as_uri() + '?mode=ro', uri=True)) as source:
            with closing(sqlite3.connect(backup)) as target:
                source.backup(target)
        with closing(sqlite3.connect(backup.as_uri() + '?mode=ro', uri=True)) as verified:
            if verified.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
                raise RuntimeError('backup integrity check failed')
            if verified.execute('PRAGMA foreign_key_check').fetchone() is not None:
                raise RuntimeError('backup foreign-key check failed')
            for table in ('users', 'billing_records', 'balance_ledgers', 'balance_reservations'):
                verified.execute('SELECT COUNT(*) FROM ' + table).fetchone()
        metadata['database_sha256'] = sha256(backup)
        (release / 'data.db.sha256').write_text(metadata['database_sha256'] + '  data.db\n')
        # Restore rehearsal on a separate copy, never replace production to test recovery.
        # Verifier deliberately rejects every path under the live production data directory.
        import tempfile
        rehearsal_dir = Path(tempfile.mkdtemp(prefix='switchyard-migration-', dir='/var/tmp'))
        os.chmod(rehearsal_dir, 0o700)
        restored = rehearsal_dir / 'restore-rehearsal.db'
        shutil.copyfile(backup, restored)
        with closing(sqlite3.connect(restored.as_uri() + '?mode=ro', uri=True)) as check:
            if check.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
                raise RuntimeError('restore rehearsal failed')
        if sha256(restored) != metadata['database_sha256']:
            raise RuntimeError('restore rehearsal checksum mismatch')
        metadata['migration_rehearsal'] = command(str(args.candidate.resolve()), 'verify-migration', '--database', str(restored.resolve()))
        metadata['recovery_holds_before'] = recovery_holds(backup)
        for suffix in ('', '-wal', '-shm'):
            Path(str(restored) + suffix).unlink(missing_ok=True)
        rehearsal_dir.rmdir()
        before = audit(args.audit_script, release / 'audit-before.json')
        metadata['before_invariants'] = before
        if any(before.values()):
            raise RuntimeError('pre-release financial invariant violation requires review')
        stage = BIN.with_name('switchyard.financial-stage')
        shutil.copyfile(args.candidate, stage)
        os.chmod(stage, BIN.stat().st_mode & 0o777)
        os.chown(stage, BIN.stat().st_uid, BIN.stat().st_gid)
        if sha256(stage) != args.sha256:
            raise RuntimeError('staged binary checksum mismatch')
        os.replace(stage, BIN)
        installed = True
        command('systemctl', 'start', SERVICE)
        stopped = False
        metadata['health'] = await_health()
        if sha256(BIN) != args.sha256:
            raise RuntimeError('installed binary checksum mismatch')
        after = audit(args.audit_script, release / 'audit-after.json')
        metadata['after_invariants'] = after
        if any(after.values()):
            raise RuntimeError('post-release financial invariant violation')
        metadata['result'] = 'deployed'
    except Exception as error:
        metadata['error'] = str(error)
        if installed:
            leave_failed_install_stopped(metadata)
            stopped = False  # Deliberately leave stopped: do not restart an incompatible old binary.
        else:
            metadata['result'] = 'not-installed'
        if stopped:
            command('systemctl', 'start', SERVICE)
            stopped = False
            metadata['rollback_health'] = await_health()
        raise
    finally:
        (release / 'release.json').write_text(json.dumps(metadata, ensure_ascii=False, indent=2))
        print(json.dumps({'release_directory': str(release), **metadata}, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
