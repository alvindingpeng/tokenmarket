#!/usr/bin/env python3
"""Compare original financial rows against an isolated migrated SQLite clone (read-only)."""
import argparse
import hashlib
import json
from pathlib import Path
import sqlite3
from contextlib import closing

TABLES = ('users', 'billing_records', 'balance_ledgers', 'balance_reservations', 'settlement_statements', 'billing_reconciles')

def compare(original, migrated):
    results = {}
    with closing(sqlite3.connect(original.as_uri()+'?mode=ro', uri=True)) as a, closing(sqlite3.connect(migrated.as_uri()+'?mode=ro', uri=True)) as b:
        for table in TABLES:
            columns = [r[1] for r in a.execute('PRAGMA table_info('+table+')')]
            if not columns:
                raise RuntimeError('missing financial table '+table)
            query = 'SELECT '+','.join('"'+v+'"' for v in columns)+' FROM '+table+' ORDER BY rowid'
            def digest(connection):
                h = hashlib.sha256()
                for row in connection.execute(query):
                    h.update(repr(row).encode()); h.update(b'\n')
                return h.hexdigest()
            results[table] = digest(a) == digest(b)
        if b.execute('PRAGMA integrity_check').fetchall() != [('ok',)] or b.execute('PRAGMA foreign_key_check').fetchone() is not None:
            raise RuntimeError('migrated copy integrity failed')
    if not all(results.values()):
        raise RuntimeError('original financial rows changed: '+repr(results))
    return results

if __name__ == '__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--original',type=Path,required=True);p.add_argument('--migrated',type=Path,required=True)
    args=p.parse_args()
    print(json.dumps({'ok':True,'financial_rows_unchanged':compare(args.original.resolve(strict=True),args.migrated.resolve(strict=True))}))
