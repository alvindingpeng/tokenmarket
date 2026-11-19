"""Isolated checks for batch-2 deployment tools; never access production."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent


class SecurityToolsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'bin').mkdir()
        (self.root / 'data').mkdir()
        (self.root / 'bin/switchyard').write_bytes(b'previous-binary')
        (self.root / 'data/config.json').write_text('{}')
        self.db = sqlite3.connect(self.root / 'data/data.db')
        self.addCleanup(self.db.close)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('CREATE TABLE users (id INTEGER PRIMARY KEY, frozen REAL)')
        self.db.execute('INSERT INTO users VALUES (1, 0)')
        for table in ('api_keys', 'channel_keys', 'billing_records', 'balance_ledgers'):
            self.db.execute('CREATE TABLE ' + table + ' (id INTEGER PRIMARY KEY)')
            self.db.execute('INSERT INTO ' + table + ' VALUES (1)')
        self.db.commit()
        fakebin = self.root / 'fakebin'
        fakebin.mkdir()
        stub = fakebin / 'systemctl'
        stub.write_text('#!/bin/sh\nif [ "$1" = show ]; then echo "${TEST_MAIN_PID:-0}"; else echo "[Service]"; fi\n')
        stub.chmod(0o700)
        self.env = dict(os.environ, PATH=str(fakebin) + os.pathsep + os.environ.get('PATH', ''))

    def run_tool(self, name, *args, env=None):
        return subprocess.run([sys.executable, str(SCRIPTS / name), *map(str, args)], capture_output=True, text=True, env=env or self.env)

    def test_backup_includes_wal_and_has_private_permissions(self):
        result = self.run_tool('security-batch2-backup.py', '--root', self.root)
        self.assertEqual(result.returncode, 0, result.stderr)
        directory = Path(json.loads(result.stdout)['recovery_directory'])
        with sqlite3.connect(directory / 'pre-upgrade.db') as backup:
            self.assertEqual(backup.execute('SELECT COUNT(*) FROM users').fetchone()[0], 1)
        self.assertEqual((directory.stat().st_mode & 0o777), 0o700)
        for file in directory.iterdir():
            self.assertEqual(file.stat().st_mode & 0o777, 0o600, str(file))

    def test_backup_refuses_running_service(self):
        result = self.run_tool('security-batch2-backup.py', '--root', self.root, env=dict(self.env, TEST_MAIN_PID='42'))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'data/releases').exists())

    def test_backup_refuses_foreign_key_violations(self):
        self.db.execute('CREATE TABLE orphan (user_id INTEGER REFERENCES users(id))')
        self.db.execute('INSERT INTO orphan VALUES (999)')
        self.db.commit()
        result = self.run_tool('security-batch2-backup.py', '--root', self.root)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('foreign key check failed', result.stderr)
        self.assertFalse(list((self.root / 'data/releases').glob('*/backup.json')))

    def test_inspection_requires_hash_schema_and_valid_fingerprints(self):
        args = ('security-batch2-inspect.py', self.root / 'data/data.db', '--require-hashed-api-keys')
        self.assertNotEqual(self.run_tool(*args).returncode, 0)
        self.db.execute("ALTER TABLE api_keys ADD COLUMN key_hash TEXT DEFAULT ''")
        self.db.commit()
        self.assertNotEqual(self.run_tool(*args).returncode, 0)
        self.db.execute('UPDATE api_keys SET key_hash=?', ('a' * 64,))
        self.db.commit()
        self.assertEqual(self.run_tool(*args).returncode, 0)
        self.db.execute('ALTER TABLE api_keys ADD COLUMN api_key TEXT')
        self.db.commit()
        self.assertNotEqual(self.run_tool(*args).returncode, 0)

    def test_inspection_rejects_duplicate_fingerprint_ownership(self):
        self.db.execute("ALTER TABLE api_keys ADD COLUMN key_hash TEXT")
        self.db.execute("UPDATE api_keys SET key_hash=?", ('a' * 64,))
        self.db.execute("INSERT INTO api_keys (id, key_hash) VALUES (?, ?)", (999, 'a' * 64))
        self.db.commit()
        result = self.run_tool('security-batch2-inspect.py', self.root / 'data/data.db', '--require-hashed-api-keys')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(json.loads(result.stdout)['duplicate_api_key_hashes'], 1)

    def test_migration_comparison_detects_balance_and_key_changes(self):
        import hashlib
        self.db.execute("ALTER TABLE api_keys ADD COLUMN api_key TEXT")
        self.db.execute("UPDATE api_keys SET api_key='legacy-secret'")
        self.db.execute("ALTER TABLE channel_keys ADD COLUMN key TEXT")
        self.db.execute("UPDATE channel_keys SET key='upstream-secret'")
        self.db.execute("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
        self.db.execute("INSERT INTO settings VALUES ('auth_secret','session-signing-secret')")
        self.db.commit()
        target = self.root / "migrated.db"
        with sqlite3.connect(target) as after:
            self.db.backup(after)
            after.execute("ALTER TABLE api_keys ADD COLUMN key_hash TEXT")
            after.execute("UPDATE api_keys SET key_hash=?", (hashlib.sha256(b"legacy-secret").hexdigest(),))
            after.execute("ALTER TABLE api_keys DROP COLUMN api_key")
            after.execute("UPDATE channel_keys SET key='sysec$1$test'")
            after.execute("UPDATE settings SET value='sysec$1$test'")
        args = ("security-batch2-compare.py", self.root / "data/data.db", target)
        self.assertEqual(self.run_tool(*args).returncode, 0)
        with sqlite3.connect(target) as after:
            after.execute("UPDATE users SET frozen=10")
        self.assertNotEqual(self.run_tool(*args).returncode, 0)
        with sqlite3.connect(target) as after:
            after.execute("UPDATE users SET frozen=0")
            after.execute("UPDATE api_keys SET key_hash=?", ("a" * 64,))
        self.assertNotEqual(self.run_tool(*args).returncode, 0)

    def test_compaction_requires_stop_and_migrated_schema(self):
        args = ("security-batch2-compact.py", self.root / "data/data.db", "--service-stopped")
        self.assertNotEqual(self.run_tool(*args, env=dict(self.env, TEST_MAIN_PID="42")).returncode, 0)
        self.assertNotEqual(self.run_tool(*args).returncode, 0)
        self.db.execute("ALTER TABLE api_keys ADD COLUMN key_hash TEXT")
        self.db.execute("ALTER TABLE channel_keys ADD COLUMN key TEXT")
        self.db.execute("UPDATE channel_keys SET key='plaintext'")
        self.db.commit()
        self.assertNotEqual(self.run_tool(*args).returncode, 0)
        self.db.execute("UPDATE channel_keys SET key='sysec$1$test'")
        self.db.commit()
        result = self.run_tool(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["compacted"])

    def test_inspection_rejects_negative_frozen(self):
        result = self.run_tool('security-batch2-inspect.py', self.root / 'data/data.db')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.db.execute('UPDATE users SET frozen=-1')
        self.db.commit()
        result = self.run_tool('security-batch2-inspect.py', self.root / 'data/data.db')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(json.loads(result.stdout)['negative_frozen'], 1)


if __name__ == '__main__':
    unittest.main()
