import hashlib
import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('audit', Path(__file__).with_name('audit-financial-history.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AuditTest(unittest.TestCase):
    def test_missing_path_is_not_created(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'absent.db'
            with self.assertRaises(FileNotFoundError):
                module.audit(path)
            self.assertFalse(path.exists())

    def test_missing_schema_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'incomplete.db'
            with sqlite3.connect(path) as conn:
                conn.execute('CREATE TABLE placeholder(id INTEGER)')
            before = hashlib.sha256(path.read_bytes()).hexdigest()
            with self.assertRaises(sqlite3.OperationalError):
                module.audit(path)
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), before)

    def test_read_only_evidence_and_detection(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'history.db'
            with sqlite3.connect(path) as conn:
                conn.executescript("""
CREATE TABLE users(id INTEGER, frozen REAL, withdraw_frozen REAL);
CREATE TABLE billing_records(id INTEGER, request_id INTEGER, user_id INTEGER,user_cost REAL,owner_revenue REAL,platform_revenue REAL,owner_id INTEGER,created_at TEXT);
CREATE TABLE balance_reservations(user_id INTEGER,amount REAL,settled INTEGER);
CREATE TABLE balance_ledgers(user_id INTEGER,request_id INTEGER,kind TEXT,amount REAL,frozen REAL);
CREATE TABLE disputes(id INTEGER);
CREATE TABLE withdrawals(id INTEGER);
CREATE TABLE settlement_statements(id INTEGER,owner_id INTEGER,period TEXT,status TEXT,request_count INTEGER,gross_revenue REAL,refund_deduction REAL,net_revenue REAL);
CREATE TABLE relay_requests(id INTEGER,user_id INTEGER,started_at TEXT,status TEXT,prompt_tokens INTEGER,completion_tokens INTEGER);
CREATE TABLE billing_reconciles(day TEXT,status TEXT,records INTEGER,missing INTEGER,duplicated INTEGER,stale_hold INTEGER,detail TEXT);
INSERT INTO users VALUES(1,-2,0);
INSERT INTO billing_records VALUES(1,10,1,3,1,1,1,'2026-10-16 10:00:00+08:00'),(2,10,1,2,1,1,1,'2026-10-16 12:00:00+08:00');
INSERT INTO settlement_statements VALUES(1,1,'20261016','settled',1,1,0,1);
INSERT INTO settlement_statements VALUES(2,2,'20261016','audited',0,0,0,0);
INSERT INTO billing_records VALUES(3,11,3,1,1,0,3,'2026-10-17 00:01:00+08:00');
INSERT INTO settlement_statements VALUES(3,3,'20261017','audited',1,1,0,1);
INSERT INTO balance_ledgers VALUES(1,10,'settle',1,-1),(1,10,'release',1,-1);
INSERT INTO relay_requests VALUES(20,1,'2026-10-14','success',1,0);
INSERT INTO billing_reconciles VALUES('20261008','mismatch',147,321,0,0,'original evidence');
""")
            before = hashlib.sha256(path.read_bytes()).hexdigest()
            report = module.audit(path)
            self.assertTrue(report['read_only'])
            self.assertEqual(len(report['daily_statement_source_comparison']), 1)
            self.assertEqual(report['daily_statement_source_comparison'][0]['source_requests'], 2)
            self.assertEqual(report['daily_statement_source_comparison'][0]['source_gross'], 2)
            for name in ('negative_holds','duplicate_billing_requests','duplicate_terminal_ledgers','billing_split_mismatches','hold_mismatches','currently_unmapped_usage'):
                self.assertEqual(len(report[name]), 1, name)
            self.assertEqual(report['recorded_reconciliation_exceptions'][0]['missing'], 321)
            self.assertIn('preserve the original exception', report['recorded_reconciliation_exceptions'][0]['coverage_warning'])
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), before)


if __name__ == '__main__':
    unittest.main()
