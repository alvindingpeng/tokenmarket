import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release", Path(__file__).with_name("production-financial-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class ReleaseSafetyTest(unittest.TestCase):
    def test_drain_fails_closed(self):
        for health in (None, {}, {"active_requests": 1}, {"status": "ok"}):
            with patch.object(release, "health", return_value=health):
                with self.assertRaises(RuntimeError): release.require_drained()
        with patch.object(release, "health", return_value={"active_requests": 0}):
            release.require_drained()

    def test_explicit_pause_confirmation_required(self):
        import sys
        with patch.object(sys, 'argv', ['release', '--candidate', '/missing', '--sha256', 'bad', '--audit-script', '/missing']), patch.object(release.os, 'geteuid', return_value=0):
            with self.assertRaisesRegex(SystemExit, 'pause upstream ingress'):
                release.main()

    def test_failed_install_never_restarts_old_binary(self):
        metadata = {}
        with patch.object(release, 'command') as command:
            release.leave_failed_install_stopped(metadata)
            command.assert_called_once_with('systemctl', 'stop', release.SERVICE)
        self.assertIn('service stopped', metadata['result'])
        self.assertIn('database never overwritten', metadata['rollback_blocked'])

    def test_pending_manual_unknown_hold(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/"copy.db"
            with sqlite3.connect(path) as db:
                self.assertEqual(release.recovery_holds(path),0)
                db.execute("CREATE TABLE billing_settlement_tasks(status TEXT)")
                db.executemany("INSERT INTO billing_settlement_tasks VALUES (?)", [(s,) for s in ("pending","retry","manual","completed","unknown")])
                db.commit()
                self.assertEqual(release.recovery_holds(path),4)

if __name__ == "__main__": unittest.main()
