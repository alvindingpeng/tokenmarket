#!/usr/bin/env python3
"""Read-only health/version gate; does not send upstream requests or create bills."""
import argparse
import json
import sys
import urllib.request

def validate_health(payload, expected):
    if not isinstance(payload, dict):
        raise ValueError("health response is not an object")
    for field in ("status", "db"):
        if payload.get(field) != "ok":
            raise ValueError(f"{field} is not ok")
    if payload.get("version") != expected:
        raise ValueError(f"version mismatch: expected {expected}, got {payload.get('version')}")
    return {field: payload[field] for field in ("status", "db", "version")}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="https://tt.xjj.se")
    parser.add_argument("--expected-version", default="v1.4.3")
    args = parser.parse_args()
    url = args.base_url.rstrip("/") + "/healthz"
    try:
        with urllib.request.urlopen(url, timeout=15) as response:
            if response.status != 200:
                raise ValueError(f"HTTP {response.status}")
            payload = json.loads(response.read(65537))
        print(json.dumps(validate_health(payload, args.expected_version), ensure_ascii=False))
    except Exception as exc:
        print(f"Release verification failed: {exc}", file=sys.stderr)
        return 1
    return 0

if __name__ == "__main__":
    sys.exit(main())
