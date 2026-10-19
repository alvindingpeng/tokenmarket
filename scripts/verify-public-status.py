#!/usr/bin/env python3
"""Anonymous release smoke; never sends a model inference request."""
import json
import sys
import urllib.request

base = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8088").rstrip("/")
with urllib.request.urlopen(base + "/healthz", timeout=10) as response:
    assert response.status == 200
with urllib.request.urlopen(base + "/status", timeout=10) as response:
    html = response.read().decode()
    assert "text/html" in response.headers.get("Content-Type", "")
    assert "/api/v1/public/channel/stats" in html
with urllib.request.urlopen(base + "/api/v1/public/channel/stats", timeout=10) as response:
    data = json.load(response)["data"]
assert data["window_seconds"] == 900
assert data["refresh_seconds"] == 15
assert isinstance(data["models"], list)
forbidden = {"channel_name", "channel_id", "user_id", "base_url", "key", "client_ip", "supply_price", "error"}
for row in data["models"]:
    assert str(row["channel_code"]).isdigit()
    assert not forbidden.intersection(row)
    assert len(row["history"]) == 15
    for key in ("success_rate", "cache_read_ratio"):
        assert row[key] is None or 0 <= row[key] <= 1
print(json.dumps({"base": base, "public_models": len(data["models"]),
                  "channels": len({r["channel_code"] for r in data["models"]}),
                  "generated_at": data["generated_at"], "anonymous": True}, ensure_ascii=False))
