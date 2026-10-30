"""Synthetic SQLite range-query benchmark. Never connects to production."""
import json
import sqlite3
import statistics
import time


def main():
    con = sqlite3.connect(":memory:")
    con.execute("CREATE TABLE billing_records(user_id INTEGER, created_at TEXT, user_cost REAL)")
    con.executemany(
        "INSERT INTO billing_records VALUES(?,?,?)",
        ((i % 10, "2025-%02d-%02d 12:00:00" % ((i // 28) % 12 + 1, i % 28 + 1), 0.125)
         for i in range(300_000)),
    )
    con.execute("CREATE INDEX old_user ON billing_records(user_id)")
    con.execute("CREATE INDEX old_date ON billing_records(created_at)")
    sql = ("SELECT COUNT(*), SUM(user_cost) FROM billing_records "
           "WHERE user_id=? AND created_at>=? AND created_at<?")
    args = (1, "2025-10-01", "2025-10-08")
    results = {}
    for label in ("single-column", "composite"):
        if label == "composite":
            con.execute("CREATE INDEX new_user_date ON billing_records(user_id, created_at)")
        plan = list(con.execute("EXPLAIN QUERY PLAN " + sql, args))
        con.execute(sql, args).fetchone()  # Both measurements use a warmed connection.
        times = []
        for _ in range(20):
            start = time.perf_counter()
            result = con.execute(sql, args).fetchone()
            times.append((time.perf_counter() - start) * 1000)
        results[label] = dict(rows=result[0], cost=result[1], median_ms=statistics.median(times), plan=plan)
    assert results["single-column"]["rows"] == results["composite"]["rows"] > 0
    assert results["single-column"]["cost"] == results["composite"]["cost"]
    assert "new_user_date" in str(results["composite"]["plan"])
    print(json.dumps(results, indent=2))
    con.close()


if __name__ == "__main__":
    main()
