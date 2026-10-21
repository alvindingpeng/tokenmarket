#!/usr/bin/env python3
"""Read-only SQLite financial evidence report; never computes or applies balance fixes."""
import argparse
from contextlib import closing
import datetime as dt
import json
from pathlib import Path
import sqlite3


def audit(path):
    # mode=ro prevents accidental creation; query_only is defense in depth.
    uri = Path(path).resolve(strict=True).as_uri() + '?mode=ro'
    with closing(sqlite3.connect(uri, uri=True, timeout=10)) as conn:
        conn.row_factory = sqlite3.Row
        conn.execute('PRAGMA query_only=ON')
        conn.execute('BEGIN')  # all evidence comes from one consistent read transaction
        def rows(sql):
            return [dict(row) for row in conn.execute(sql)]
        report = {
            'generated_at': dt.datetime.now(dt.timezone.utc).isoformat(),
            'source': str(Path(path).resolve()),
            'read_only': True,
            'limitations': [
                'Run on a verified backup for durable evidence; live reports are point-in-time only.',
                'No opening balance is assumed; ledger totals must not be used to reset account balances.',
                'Missing retained request logs do not prove historical reconciliation discrepancies are resolved.',
                'No money changes, statement regeneration, reconciliation overwrites or schema migrations are performed.',
            ],
        }
        report['counts'] = {table: conn.execute('SELECT COUNT(*) FROM ' + table).fetchone()[0]
            for table in ('users', 'billing_records', 'balance_reservations', 'balance_ledgers',
                          'disputes', 'withdrawals', 'settlement_statements', 'relay_requests')}
        report['negative_holds'] = rows('SELECT id AS user_id, frozen, withdraw_frozen FROM users WHERE frozen < -0.000001 OR withdraw_frozen < -0.000001')
        report['duplicate_billing_requests'] = rows('SELECT request_id, COUNT(*) AS entries FROM billing_records WHERE request_id <> 0 GROUP BY request_id HAVING COUNT(*) > 1')
        report['duplicate_terminal_ledgers'] = rows("SELECT user_id, request_id, COUNT(*) AS entries FROM balance_ledgers WHERE request_id <> 0 AND kind IN ('settle','release') GROUP BY user_id, request_id HAVING COUNT(*) > 1")
        report['billing_split_mismatches'] = rows('SELECT id, request_id, user_id, user_cost, owner_revenue, platform_revenue FROM billing_records WHERE ABS(user_cost - owner_revenue - platform_revenue) > 0.000001')
        report['hold_mismatches'] = rows('SELECT u.id AS user_id, u.frozen, COALESCE(r.expected,0) AS expected_open_holds FROM users u LEFT JOIN (SELECT user_id, SUM(amount) AS expected FROM balance_reservations WHERE settled = 0 GROUP BY user_id) r ON r.user_id=u.id WHERE ABS(u.frozen - COALESCE(r.expected,0)) > 0.000001')
        report['request_coverage'] = rows('SELECT MIN(started_at) AS earliest_retained, MAX(started_at) AS latest_retained, COUNT(*) AS retained FROM relay_requests')[0]
        report['currently_unmapped_usage'] = rows('SELECT r.id AS request_id, r.user_id, r.started_at, r.status FROM relay_requests r WHERE COALESCE(r.prompt_tokens,0)+COALESCE(r.completion_tokens,0)>0 AND NOT EXISTS (SELECT 1 FROM billing_records b WHERE b.request_id=r.id)')
        report['recorded_reconciliation_exceptions'] = rows("SELECT day, status, records, missing, duplicated, stale_hold, detail FROM billing_reconciles WHERE status <> 'balanced' ORDER BY day")
        earliest = report['request_coverage']['earliest_retained']
        for exception in report['recorded_reconciliation_exceptions']:
            day = exception['day']
            iso_day = day[:4] + '-' + day[4:6] + '-' + day[6:8]
            exception['coverage_warning'] = (
                'Retained request logs do not cover this whole day; preserve the original exception and recover archival evidence.'
                if earliest is None or iso_day <= earliest[:10] else
                'Retained logs exist, but completeness still requires backup/archive verification.')
        report['statements'] = rows('SELECT id, owner_id, period, status, request_count, gross_revenue, refund_deduction, net_revenue FROM settlement_statements ORDER BY period,owner_id')
        # Daily periods use stored business-local date strings, not SQLite UTC conversion.
        # Refund cases require approval-day evidence; do not guess from billing status alone.
        report['daily_statement_source_comparison'] = rows("""
            SELECT s.id, s.owner_id, s.period, s.status,
                   s.request_count AS stored_requests, COUNT(b.id) AS source_requests,
                   s.gross_revenue AS stored_gross, COALESCE(SUM(b.owner_revenue),0) AS source_gross
            FROM settlement_statements s LEFT JOIN billing_records b
              ON b.owner_id=s.owner_id AND REPLACE(SUBSTR(b.created_at,1,10),'-','')=s.period
            WHERE LENGTH(s.period)=8
            GROUP BY s.id
            HAVING s.request_count <> COUNT(b.id)
                OR ABS(s.gross_revenue-COALESCE(SUM(b.owner_revenue),0)) > 0.000001
        """)
        report['statement_comparison_policy'] = 'Source differences are proposals only, not corrections. Refund deduction and monthly periods require separate approval-day evidence; audited statements must not be overwritten.'
        report['ledger_summary'] = rows('SELECT kind, COUNT(*) AS entries, SUM(amount) AS amount, SUM(frozen) AS frozen FROM balance_ledgers GROUP BY kind ORDER BY kind')
        conn.rollback()
        return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', required=True, help='verified SQLite snapshot (preferred), or read-only live path')
    args = parser.parse_args()
    try:
        print(json.dumps(audit(args.database), ensure_ascii=False, indent=2, allow_nan=False))
    except (OSError, sqlite3.Error, ValueError) as error:
        parser.exit(1, 'audit failed (no corrections applied): ' + str(error) + '\n')


if __name__ == '__main__':
    main()
