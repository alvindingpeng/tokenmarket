#!/usr/bin/env python3
"""Post-release security verification for the switchyard security batch-2 release.

Runs on the production host as root AFTER the release is live (default target: the local
service at http://127.0.0.1:8088 and the live database /opt/switchyard/data/data.db).
Everything it does is read-only or provably side-effect free:

  * both SQLite databases are opened read-only (URI mode=ro) -- this tool never writes a row;
  * the only HTTP calls are GET /api/v1/apikey/login (real API key authentication), GET
    /api/v1/apikey/stats (response leak check), GET /api/v1/user/status (plain session) and
    POST /api/v1/user/manage/create with an empty JSON object {} -- that endpoint is gated by the
    step-up middleware, so the probe must be answered 428 before any handler runs and cannot
    create anything; the users table is re-counted afterwards to prove nothing changed.

What it verifies
  1. up to --key-count (default 2) keys retained from the pre-release backup (matched by
     SHA-256 fingerprint against the live api_keys table) really authenticate against
     /api/v1/apikey/login; a retained key that is disabled or expired must be REJECTED
     (401/403) -- rejection is the expected outcome there, not 200;
  2. a freshly generated random key must be rejected with 401;
  3. /api/v1/apikey/stats answers normally and its payload carries no api_key / key_hash
     field anywhere -- only the key_prefix / key_last4 mask;
  4. high-risk management endpoints require step-up: an admin session WITHOUT X-Step-Up-Token
     must get 428 + error_code=step_up_required from POST /api/v1/user/manage/create, while the
     ordinary GET /api/v1/user/status keeps working with the same session.

The admin session is a temporary JWT signed in memory for five minutes from the pre-release
backup's settings.auth_secret, bound to one active admin (id / role / token_version) read from
the live database -- the exact claim shape of internal/server/auth/auth.go (HS256, uid/role/ver,
issuer = conf.APP_NAME = "switchyard"). The token is never stored, never printed and expires
after 5 minutes; it is used for exactly two calls (the read endpoint and the step-up-gated probe
with an empty body). Use --skip-stepup to skip signing entirely; the run then reports the safe
manual alternative instead.

Evidence contract: stdout carries ONE JSON object with counts, field names, statuses and
booleans only. It NEVER contains API keys, SHA-256 fingerprints, the auth secret, session
tokens, envelope ciphertext or credential headers -- every string that could be printed goes
through Redactor first (defense in depth; see test_verify_security_batch2.py, which asserts it).

Optional cross-decryption of recoverable credentials is intentionally not implemented here:
opening sysec$1$ envelopes needs the AES-256-GCM master key and this verifier deliberately never
loads key material. The safe equivalent is documented in the emitted evidence.

Usage:
  python3 scripts/verify-security-batch2.py --before-db /root/switchyard-recovery/<point>/pre-upgrade.db
  python3 scripts/verify-security-batch2.py --before-db <old.db> --after-db <db> --base-url http://127.0.0.1:8088

Exit codes: 0 = every check passed (warnings allowed unless --fail-on-warn), 1 = at least one
check failed (or warned with --fail-on-warn), 2 = the run itself was impossible (bad arguments,
unreadable database, missing legacy plaintext column). JSON evidence is printed in every case.
"""

import argparse
import base64
import hashlib
import hmac
import json
import re
import secrets
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

# internal/conf/const.go APP_NAME: the issuer every login JWT must carry.
APP_NAME = "switchyard"
DEFAULT_AFTER_DB = "/opt/switchyard/data/data.db"
DEFAULT_BASE_URL = "http://127.0.0.1:8088"
TEMP_JWT_TTL_SECONDS = 300  # 5 minutes, in-memory only
STEP_UP_ERROR_CODE = "step_up_required"
STEP_UP_EXPECTED_STATUS = 428
# Response fields that must never appear in an API payload (model.APIKey: key_hash is json:"-",
# api_key is only ever returned once, at creation time).
FORBIDDEN_RESPONSE_FIELDS = ("api_key", "key_hash")
# Permitted label/mask fields of the stats payload; their values are the mask, never the secret.
PERMITTED_MASK_FIELDS = ("name", "key_prefix", "key_last4")

SAFE_STEPUP_ALTERNATIVE = (
    "Safe alternative to the temporary in-memory JWT: on the production host, log in as the "
    "admin with the real password (POST /api/v1/user/login or the admin UI), keep the returned "
    "auth cookie, and replay POST /api/v1/user/manage/create with the empty body {} and NO "
    "X-Step-Up-Token header; the expected answer is HTTP 428 with error_code=step_up_required. "
    "The temporary-JWT mode of this tool is safe for the same reasons: the token is signed in "
    "memory only (never stored or printed), expires after 5 minutes, is bound to one active "
    "admin id/role/token_version taken from the live database, and is used for exactly one read "
    "call plus one step-up-gated probe carrying an empty JSON body, so no side effect can run. "
    "Before probing, make sure the default admin password has been changed -- otherwise the "
    "default-password gate answers 403 ahead of the step-up gate and the 428 check stays "
    "unverified."
)

CREDENTIAL_CONSISTENCY_NOTE = (
    "Optional before/after decryption comparison of recoverable credentials is intentionally not "
    "implemented in this stdlib-only tool: opening sysec envelope ciphertext requires the AES-256-GCM "
    "master key (KEK) and this verifier never loads or handles key material. Safe equivalent: "
    "run the candidate binary 'switchyard secret migrate' (idempotent; already-encrypted rows are "
    "only verified) or 'switchyard secret status' against an isolated clone of each database with "
    "the production KEK wired through SWITCHYARD_KEK_FILE -- both fail closed when any envelope "
    "cannot be opened by the current master key. security-batch2-compare.py already proves the "
    "credential rows themselves survived the migration."
)


class VerifySetupError(RuntimeError):
    """The run itself is impossible (bad input); the caller reports JSON evidence and exits 2."""


class Redactor:
    """Strips secret material from every string this tool may print.

    Two layers: literal values that were actually loaded (old API keys, the auth secret, signed
    tokens, generated probe keys) and generic patterns (envelope ciphertext, key-shaped strings,
    JWTs, 64-hex fingerprints, credential headers). Evidence is built from booleans, counts and
    field names only -- this is the belt-and-braces layer.
    """

    _PATTERNS = (
        (re.compile(r"sysec\$[^\s\"']+"), "<redacted-envelope>"),
        (re.compile(r"sk-[A-Za-z0-9_-]{8,}"), "<redacted-api-key>"),
        (re.compile(r"\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b"), "<redacted-jwt>"),
        (re.compile(r"\b[0-9a-fA-F]{64}\b"), "<redacted-fingerprint>"),
        (re.compile(r"(?i)\b(authorization|x-api-key|x-step-up-token|cookie)\s*[:=]\s*\S+"),
         lambda match: match.group(1) + ": <redacted>"),
    )

    def __init__(self):
        self._literals = []

    def register(self, *values):
        for value in values:
            if isinstance(value, str) and len(value) >= 4 and value not in self._literals:
                self._literals.append(value)

    def scrub(self, text):
        out = str(text)
        for literal in self._literals:
            out = out.replace(literal, "<redacted>")
        for pattern, repl in self._PATTERNS:
            out = pattern.sub(repl, out)
        return out

    def scrub_json(self, value):
        if isinstance(value, dict):
            return {str(key): self.scrub_json(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.scrub_json(item) for item in value]
        if isinstance(value, str):
            return self.scrub(value)
        return value


# ---------------------------------------------------------------- database access (read-only)


def open_database(path, label):
    """Open a SQLite file strictly read-only; nothing here may ever write."""
    target = Path(path)
    if not target.is_file():
        raise VerifySetupError("%s is not a readable file: %s" % (label, target))
    try:
        connection = sqlite3.connect(target.resolve().as_uri() + "?mode=ro", uri=True)
        connection.row_factory = sqlite3.Row
        connection.execute("SELECT 1 FROM sqlite_master LIMIT 1").fetchone()
    except sqlite3.Error as err:
        raise VerifySetupError("%s cannot be opened read-only: %s" % (label, err))
    return connection


def table_columns(connection, table):
    try:
        return [row[1] for row in connection.execute("PRAGMA table_info(" + table + ")")]
    except sqlite3.Error:
        return []


def row_get(row, key, default=None):
    if row is None:
        return default
    try:
        value = row[key]
    except (IndexError, KeyError):
        return default
    return default if value is None else value


def fingerprint(plaintext):
    """SHA-256 fingerprint exactly as internal/model/apikey.go APIKeyFingerprint computes it.

    Internal use only: fingerprints are never printed (see Redactor).
    """
    return hashlib.sha256(plaintext.strip().encode("utf-8")).hexdigest()


class LegacyKey(object):
    """One pre-release api_keys row. repr must never echo the plaintext."""

    __slots__ = ("key_id", "name", "plaintext", "enabled", "expire_at", "live")

    def __init__(self, key_id, name, plaintext, enabled, expire_at):
        self.key_id = key_id
        self.name = name
        self.plaintext = plaintext
        self.enabled = enabled
        self.expire_at = expire_at
        self.live = None  # filled in by select_test_keys

    def __repr__(self):
        return "<LegacyKey id=%s name=%r plaintext=<redacted>>" % (self.key_id, self.name)


class LiveKey(object):
    __slots__ = ("key_id", "key_hash", "name", "enabled", "expire_at")

    def __init__(self, key_id, key_hash, name, enabled, expire_at):
        self.key_id = key_id
        self.key_hash = key_hash
        self.name = name
        self.enabled = enabled
        self.expire_at = expire_at

    def __repr__(self):
        return "<LiveKey id=%s name=%r hash=<redacted>>" % (self.key_id, self.name)


def load_legacy_api_keys(connection, redactor):
    """Read the old plaintext API keys; they are what /api/v1/apikey/login is exercised with."""
    columns = table_columns(connection, "api_keys")
    if not columns:
        raise VerifySetupError("the before backup has no api_keys table")
    if "api_key" not in columns:
        raise VerifySetupError(
            "the before backup's api_keys table has no plaintext api_key column; the pre-release "
            "keys cannot be recovered from it, so real key authentication cannot be exercised"
        )
    keys = []
    for row in connection.execute("SELECT * FROM api_keys"):
        plaintext = row_get(row, "api_key", "") or ""
        if not isinstance(plaintext, str) or not plaintext.strip():
            continue
        key = LegacyKey(
            key_id=row_get(row, "id"),
            name=str(row_get(row, "name", "") or ""),
            plaintext=plaintext.strip(),
            enabled=bool(row_get(row, "enabled", True)),
            expire_at=int(row_get(row, "expire_at", 0) or 0),
        )
        redactor.register(key.plaintext, fingerprint(key.plaintext))
        keys.append(key)
    return keys


def load_live_api_keys(connection):
    columns = table_columns(connection, "api_keys")
    if "key_hash" not in columns:
        raise VerifySetupError("the after database's api_keys table has no key_hash column")
    keys = []
    for row in connection.execute("SELECT * FROM api_keys"):
        keys.append(LiveKey(
            key_id=row_get(row, "id"),
            key_hash=str(row_get(row, "key_hash", "") or ""),
            name=str(row_get(row, "name", "") or ""),
            enabled=bool(row_get(row, "enabled", True)),
            expire_at=int(row_get(row, "expire_at", 0) or 0),
        ))
    return keys


def key_state(live, now):
    expired = live is not None and live.expire_at > 0 and live.expire_at < now
    enabled = live is None or live.enabled
    if not enabled and expired:
        return "disabled+expired"
    if not enabled:
        return "disabled"
    if expired:
        return "expired"
    return "enabled"


def select_test_keys(old_keys, live_keys, limit, now):
    """Pick the retained keys to exercise: prefer one that must authenticate and one that must be
    rejected (disabled/expired), then fill the remaining slots in id order.

    A legacy key counts as retained only when the live table holds the SAME credential, i.e. the
    SHA-256 fingerprint of the old plaintext equals the live key_hash (fingerprints stay internal).
    """
    by_hash = {}
    for live in live_keys:
        if live.key_hash:
            by_hash.setdefault(live.key_hash, live)
    by_id = {live.key_id: live for live in live_keys}
    retained = []
    for old in old_keys:
        digest = fingerprint(old.plaintext)
        live = by_hash.get(digest)
        if live is None:
            candidate = by_id.get(old.key_id)
            # Same row id but a different credential means the key was replaced -> not retained.
            live = candidate if candidate is not None and candidate.key_hash == digest else None
        if live is None:
            continue
        old.live = live
        retained.append(old)

    def accepts(key):
        return key_state(key.live, now) == "enabled"

    chosen = []
    for group in ([key for key in retained if accepts(key)], [key for key in retained if not accepts(key)]):
        if group and len(chosen) < limit:
            chosen.append(group[0])
    for key in sorted(retained, key=lambda item: (item.key_id is None, item.key_id)):
        if len(chosen) >= limit:
            break
        if key not in chosen:
            chosen.append(key)
    return retained, chosen


def read_setting_value(connection, key):
    if not table_columns(connection, "settings"):
        return None
    row = connection.execute("SELECT value FROM settings WHERE key = ?", (key,)).fetchone()
    value = row_get(row, "value")
    return value if isinstance(value, str) else None


def read_active_admin(connection):
    columns = table_columns(connection, "users")
    if "role" not in columns or "status" not in columns:
        raise VerifySetupError("the after database's users table lacks role/status columns")
    row = connection.execute(
        "SELECT id, username, role, status, token_version FROM users "
        "WHERE role = 'admin' AND status = 'active' ORDER BY id LIMIT 1"
    ).fetchone()
    if row is None:
        return None
    return {
        "id": int(row_get(row, "id", 0)),
        "username": str(row_get(row, "username", "") or ""),
        "role": str(row_get(row, "role", "") or ""),
        "status": str(row_get(row, "status", "") or ""),
        "token_version": int(row_get(row, "token_version", 0) or 0),
    }


def users_fingerprint(connection):
    """Cheap, secret-free snapshot used to prove a probe changed nothing."""
    row = connection.execute("SELECT COUNT(*), COALESCE(MAX(id), 0) FROM users").fetchone()
    return {"count": int(row[0]), "max_id": int(row[1])}


# ------------------------------------------------------------------- temporary session (HS256)


def b64url(raw):
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def sign_temp_jwt(secret, user_id, role, token_version, ttl_seconds=TEMP_JWT_TTL_SECONDS, now=None):
    """Sign the exact login-JWT shape of internal/server/auth/auth.go, in memory only.

    Claims: uid / role / ver plus iss=conf.APP_NAME, iat, nbf, exp; HS256 over the settings
    auth_secret. The caller must never print the result.
    """
    issued = int(time.time()) if now is None else int(now)
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {
        "uid": int(user_id),
        "role": str(role),
        "ver": int(token_version),
        "iss": APP_NAME,
        "iat": issued,
        "nbf": issued,
        "exp": issued + int(ttl_seconds),
    }
    signing_input = (
        b64url(json.dumps(header, separators=(",", ":"), sort_keys=True).encode("utf-8"))
        + "."
        + b64url(json.dumps(payload, separators=(",", ":"), sort_keys=True).encode("utf-8"))
    )
    signature = hmac.new(secret.encode("utf-8"), signing_input.encode("ascii"), hashlib.sha256).digest()
    return signing_input + "." + b64url(signature)


# ------------------------------------------------------------------------------ HTTP probing


def http_call(url, method="GET", headers=None, body=None, timeout=10.0):
    """One HTTP call; returns (status, body_text) and never raises on an HTTP status."""
    request = urllib.request.Request(url, data=body, method=method, headers=dict(headers or {}))
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.getcode(), response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        payload = err.read().decode("utf-8", "replace") if err.fp is not None else ""
        return err.code, payload


def parse_envelope(body):
    try:
        value = json.loads(body)
    except ValueError:
        return None
    return value if isinstance(value, dict) else None


def envelope_message(envelope):
    if not isinstance(envelope, dict):
        return ""
    message = envelope.get("message")
    return message if isinstance(message, str) else ""


def find_forbidden_fields(node, path="$"):
    """Paths of response fields named api_key / key_hash, at any depth. Names only, never values."""
    hits = []
    if isinstance(node, dict):
        for key, value in node.items():
            here = path + "." + str(key)
            if str(key).lower() in FORBIDDEN_RESPONSE_FIELDS:
                hits.append(here)
            hits.extend(find_forbidden_fields(value, here))
    elif isinstance(node, list):
        for index, value in enumerate(node):
            hits.extend(find_forbidden_fields(value, path + "[%d]" % index))
    return hits


def secret_pattern_hits(node, key_name=None):
    """Values that look like secret material outside the permitted mask/label fields."""
    hits = []
    if isinstance(node, dict):
        for key, value in node.items():
            hits.extend(secret_pattern_hits(value, str(key).lower()))
    elif isinstance(node, list):
        for value in node:
            hits.extend(secret_pattern_hits(value, key_name))
    elif isinstance(node, str):
        if key_name in PERMITTED_MASK_FIELDS:
            return hits
        if re.search(r"\bsk-[A-Za-z0-9_-]{8,}\b", node):
            hits.append("api key material")
        elif re.search(r"\b[0-9a-fA-F]{64}\b", node):
            hits.append("fingerprint")
    return sorted(set(hits))


# ------------------------------------------------------------------------------------ checks


def check_api_key_login(base_url, key, now, timeout):
    """Real authentication of one retained key against GET /api/v1/apikey/login."""
    state = key_state(key.live, now)
    expect_authenticate = state == "enabled"
    expected_status = "authenticate (200)" if expect_authenticate else "reject (401/403)"
    entry = {
        "check": "apikey_login_retained_key",
        "key_id": key.key_id,
        "key_name": key.name,
        "key_state": state,
        "expected": expected_status,
    }
    try:
        status, body = http_call(
            base_url.rstrip("/") + "/api/v1/apikey/login",
            headers={"Authorization": "Bearer " + key.plaintext, "Accept": "application/json"},
            timeout=timeout,
        )
    except (urllib.error.URLError, OSError, ValueError) as err:
        entry.update(status="fail", detail="GET /api/v1/apikey/login unreachable: " + type(err).__name__)
        return entry
    entry["http_status"] = status
    if expect_authenticate:
        if status == 200:
            entry.update(status="pass", detail="retained key authenticated against /api/v1/apikey/login")
        else:
            entry.update(status="fail", detail="retained enabled key was not accepted (HTTP %d)" % status)
    else:
        if status in (401, 403):
            entry.update(status="pass", detail="disabled/expired key was rejected as expected (HTTP %d)" % status)
        elif status == 200:
            entry.update(
                status="fail",
                detail="a disabled/expired key authenticated (HTTP 200); rejection was expected, not 200",
            )
        else:
            entry.update(status="fail", detail="unexpected answer for a disabled/expired key (HTTP %d)" % status)
    entry["response_message"] = envelope_message(parse_envelope(body))
    return entry


def check_random_wrong_key(base_url, timeout):
    """A key nobody ever issued must be rejected with 401."""
    alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
    wrong_key = "sk-" + APP_NAME + "-" + "".join(secrets.choice(alphabet) for _ in range(48))
    entry = {"check": "random_wrong_api_key_rejected", "expected": "reject (401)"}
    try:
        status, body = http_call(
            base_url.rstrip("/") + "/api/v1/apikey/login",
            headers={"Authorization": "Bearer " + wrong_key, "Accept": "application/json"},
            timeout=timeout,
        )
    except (urllib.error.URLError, OSError, ValueError) as err:
        entry.update(status="fail", detail="GET /api/v1/apikey/login unreachable: " + type(err).__name__)
        return entry
    entry["http_status"] = status
    if status == 401:
        entry.update(status="pass", detail="random wrong key rejected with 401")
    elif status == 200:
        entry.update(status="fail", detail="a random wrong key authenticated (HTTP 200)")
    else:
        entry.update(status="fail", detail="random wrong key answered HTTP %d, expected 401" % status)
    entry["response_message"] = envelope_message(parse_envelope(body))
    return entry


def check_apikey_stats(base_url, key, now, timeout):
    """GET /api/v1/apikey/stats must work and carry no api_key / key_hash -- only the mask."""
    entry = {"check": "apikey_stats_response_no_secret_fields", "key_id": key.key_id}
    try:
        status, body = http_call(
            base_url.rstrip("/") + "/api/v1/apikey/stats",
            headers={"Authorization": "Bearer " + key.plaintext, "Accept": "application/json"},
            timeout=timeout,
        )
    except (urllib.error.URLError, OSError, ValueError) as err:
        entry.update(status="fail", detail="GET /api/v1/apikey/stats unreachable: " + type(err).__name__)
        return entry
    entry["http_status"] = status
    if status != 200:
        entry.update(status="fail", detail="GET /api/v1/apikey/stats answered HTTP %d" % status)
        return entry
    envelope = parse_envelope(body)
    if not isinstance(envelope, dict):
        entry.update(status="fail", detail="stats response is not a JSON envelope")
        return entry
    data = envelope.get("data") if isinstance(envelope.get("data"), dict) else {}
    info = data.get("info") if isinstance(data.get("info"), dict) else {}
    forbidden = find_forbidden_fields(envelope)
    mask_fields = sorted(
        name for name in ("key_prefix", "key_last4")
        if isinstance(info.get(name), str) and info.get(name) != ""
    )
    leaks = secret_pattern_hits(envelope)
    for label, needle in (("api key material", key.plaintext.strip()),
                          ("api key fingerprint", fingerprint(key.plaintext))):
        if needle and needle in body:
            leaks.append(label)
    entry.update({
        "forbidden_fields": forbidden,
        "mask_fields_present": mask_fields,
        "secret_pattern_hits": sorted(set(leaks)),
    })
    problems = []
    if forbidden:
        problems.append("response carries forbidden fields: " + ", ".join(forbidden))
    if not info:
        problems.append("stats payload has no info object")
    elif mask_fields != ["key_last4", "key_prefix"]:
        problems.append("info does not carry the key_prefix/key_last4 mask only")
    if leaks:
        problems.append("response body carries secret material: " + ", ".join(sorted(set(leaks))))
    if problems:
        entry.update(status="fail", detail="; ".join(problems))
    else:
        entry.update(
            status="pass",
            detail="stats payload carries the mask only: no api_key/key_hash field and no secret material",
        )
    return entry


def check_stepup_gate(args, evidence, redactor, before, after, timeout):
    """Admin session without step-up must get 428 from the high-risk endpoint."""
    checks = evidence["checks"]

    def skipped(reason):
        checks.append({
            "check": "high_risk_user_create_requires_stepup",
            "status": "skip",
            "detail": reason + " " + SAFE_STEPUP_ALTERNATIVE,
        })

    if args.skip_stepup:
        skipped("Step-up probes were skipped on request (--skip-stepup); no session was signed.")
        return

    secret = read_setting_value(before, "auth_secret")
    if secret is None or secret == "":
        skipped("The before backup carries no settings.auth_secret, so no session can be signed.")
        return
    if secret.startswith("sysec$"):
        skipped(
            "The before backup's settings.auth_secret is envelope-encrypted; decrypting it needs "
            "the master key, which this tool deliberately never loads."
        )
        return
    redactor.register(secret)

    admin = read_active_admin(after)
    if admin is None:
        skipped("The after database has no active admin user to bind a session to.")
        return

    token = sign_temp_jwt(secret, admin["id"], admin["role"], admin["token_version"], TEMP_JWT_TTL_SECONDS)
    redactor.register(token)
    cookie = "auth=" + token
    base = args.base_url.rstrip("/")

    # 1. the ordinary read endpoint must accept the plain session (no step-up involved).
    try:
        status, body = http_call(base + "/api/v1/user/status", headers={"Cookie": cookie}, timeout=timeout)
    except (urllib.error.URLError, OSError, ValueError) as err:
        checks.append({
            "check": "admin_temp_session",
            "status": "warn",
            "detail": "GET /api/v1/user/status unreachable: " + type(err).__name__,
        })
        skipped("The temporary session could not be established, so the step-up gate was not probed.")
        return
    session_check = {
        "check": "admin_temp_session",
        "http_status": status,
        "session": {
            "user_id": admin["id"],
            "role": admin["role"],
            "token_version": admin["token_version"],
            "ttl_seconds": TEMP_JWT_TTL_SECONDS,
            "issuer": APP_NAME,
            "signing": "HS256 in memory from the before backup's settings.auth_secret",
        },
    }
    if status == 200:
        session_check.update(status="pass", detail="temporary in-memory admin session accepted")
        checks.append(session_check)
        checks.append({
            "check": "user_status_plain_session",
            "http_status": status,
            "status": "pass",
            "detail": "GET /api/v1/user/status answers 200 with the plain session, no step-up needed",
        })
    else:
        reason = (
            "the temporary session was rejected (HTTP %d): the auth_secret in the before backup "
            "does not match the running service (rotated after the release?), so the step-up gate "
            "cannot be probed this way" % status
        )
        session_check.update(status="warn", detail=reason)
        checks.append(session_check)
        skipped("No usable admin session, so the step-up gate was not probed: " + reason + ".")
        return

    # 2. the high-risk write must demand step-up. The body is the empty object {} and the request
    #    carries no X-Step-Up-Token, so a correctly gated service answers 428 before any handler.
    before_snapshot = users_fingerprint(after)
    try:
        status, body = http_call(
            base + "/api/v1/user/manage/create",
            method="POST",
            headers={"Cookie": cookie, "Content-Type": "application/json", "Accept": "application/json"},
            body=b"{}",
            timeout=timeout,
        )
    except (urllib.error.URLError, OSError, ValueError) as err:
        checks.append({
            "check": "high_risk_user_create_requires_stepup",
            "status": "warn",
            "detail": "POST /api/v1/user/manage/create unreachable: " + type(err).__name__,
        })
        return
    envelope = parse_envelope(body)
    error_code = envelope.get("error_code") if isinstance(envelope, dict) else None
    entry = {
        "check": "high_risk_user_create_requires_stepup",
        "http_status": status,
        "error_code": error_code if isinstance(error_code, str) else None,
        "request_body": "{}",
        "step_up_header_sent": False,
    }
    if status == STEP_UP_EXPECTED_STATUS and error_code == STEP_UP_ERROR_CODE:
        entry.update(status="pass", detail="POST /api/v1/user/manage/create answered 428 step_up_required without step-up")
    elif status == STEP_UP_EXPECTED_STATUS:
        entry.update(status="warn", detail="428 answered but error_code is not step_up_required")
    elif status == 401:
        entry.update(status="warn", detail="session became invalid before the probe (HTTP 401)")
    elif status == 403:
        entry.update(
            status="warn",
            detail="request was refused before the step-up gate (HTTP 403: default admin password or "
                   "role gate); change the default admin password and rerun to verify the 428 gate. "
                   + SAFE_STEPUP_ALTERNATIVE,
        )
    elif status == 415:
        entry.update(status="fail", detail="server rejected the JSON content type (HTTP 415) instead of the step-up gate")
    else:
        entry.update(
            status="fail",
            detail="high-risk endpoint answered HTTP %d without step-up; expected 428 %s" % (status, STEP_UP_ERROR_CODE),
        )
    checks.append(entry)

    after_snapshot = users_fingerprint(after)
    unchanged = before_snapshot == after_snapshot
    checks.append({
        "check": "stepup_probe_no_side_effects",
        "status": "pass" if unchanged else "fail",
        "users_before": before_snapshot,
        "users_after": after_snapshot,
        "detail": "users table unchanged by the probes" if unchanged else "users table changed during the probe run",
    })


# --------------------------------------------------------------------------------- evidence


def base_evidence(args):
    return {
        "tool": "verify-security-batch2.py",
        "purpose": "post-release security verification of the security batch-2 release",
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "base_url": args.base_url,
        "before_db": str(args.before_db),
        "after_db": str(args.after_db),
        "key_count": args.key_count,
        "read_only_databases": True,
        "checks": [],
    }


def summarize(evidence):
    counts = {"pass": 0, "fail": 0, "warn": 0, "skip": 0}
    for check in evidence["checks"]:
        status = check.get("status", "skip")
        counts[status] = counts.get(status, 0) + 1
    if counts["fail"]:
        overall = "fail"
    elif counts["warn"]:
        overall = "pass_with_warnings"
    else:
        overall = "pass"
    evidence["summary"] = counts
    evidence["overall"] = overall
    return overall, counts


def run_verification(args, evidence, redactor):
    now = int(time.time())
    before = open_database(args.before_db, "before backup")
    after = open_database(args.after_db, "after database")
    checks = evidence["checks"]

    old_keys = load_legacy_api_keys(before, redactor)
    live_keys = load_live_api_keys(after)
    retained, chosen = select_test_keys(old_keys, live_keys, args.key_count, now)
    evidence["api_keys"] = {
        "legacy_rows_with_plaintext": len(old_keys),
        "retained_in_after_db": len(retained),
        "requested": args.key_count,
    }

    if not old_keys:
        checks.append({
            "check": "legacy_plaintext_keys_available",
            "status": "fail",
            "detail": "the before backup holds no usable plaintext API keys; retained-key authentication could not be exercised",
        })
    else:
        tested = len(chosen)
        coverage = {
            "check": "retained_api_key_coverage",
            "legacy_rows_with_plaintext": len(old_keys),
            "retained_in_after_db": len(retained),
            "requested": args.key_count,
            "tested": tested,
        }
        if tested >= args.key_count:
            coverage.update(status="pass", detail="%d retained keys selected for live authentication" % tested)
        else:
            coverage.update(
                status="warn",
                detail="only %d retained key(s) available in the after database, %d requested; "
                       "authentication was exercised on those that exist" % (tested, args.key_count),
            )
        checks.append(coverage)

    for key in chosen:
        checks.append(check_api_key_login(args.base_url, key, now, args.timeout))

    checks.append(check_random_wrong_key(args.base_url, args.timeout))

    stats_key = next((key for key in chosen if key_state(key.live, now) == "enabled"), None)
    if stats_key is None:
        checks.append({
            "check": "apikey_stats_response_no_secret_fields",
            "status": "skip",
            "detail": "no enabled retained key was available to authenticate against /api/v1/apikey/stats",
        })
    else:
        checks.append(check_apikey_stats(args.base_url, stats_key, now, args.timeout))

    check_stepup_gate(args, evidence, redactor, before, after, args.timeout)

    checks.append({
        "check": "recoverable_credentials_decrypt_consistency",
        "status": "skip",
        "detail": CREDENTIAL_CONSISTENCY_NOTE,
    })

    before.close()
    after.close()


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description="Post-release security verification for the security batch-2 release "
                    "(read-only databases, leak-free JSON evidence).",
    )
    parser.add_argument("--before-db", required=True, type=Path,
                        help="pre-release backup database (old api_keys still carry the plaintext keys)")
    parser.add_argument("--after-db", type=Path, default=Path(DEFAULT_AFTER_DB),
                        help="live database to compare against (default: %(default)s)")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL,
                        help="service under test (default: %(default)s)")
    parser.add_argument("--key-count", type=int, default=2,
                        help="how many retained keys to authenticate (default: %(default)s)")
    parser.add_argument("--timeout", type=float, default=10.0,
                        help="per-request HTTP timeout in seconds (default: %(default)s)")
    parser.add_argument("--skip-stepup", action="store_true",
                        help="do not sign the temporary admin JWT; report the safe manual alternative instead")
    parser.add_argument("--evidence-out", type=Path, default=None,
                        help="also write the JSON evidence to this file (mode 0600)")
    parser.add_argument("--fail-on-warn", action="store_true",
                        help="exit non-zero on warnings as well as failures")
    args = parser.parse_args(argv)
    if args.key_count < 1:
        parser.error("--key-count must be at least 1")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    return args


def main(argv=None):
    args = parse_args(argv)
    redactor = Redactor()
    evidence = base_evidence(args)
    try:
        run_verification(args, evidence, redactor)
        overall, counts = summarize(evidence)
        if overall == "fail" or (args.fail_on_warn and counts["warn"]):
            code = 1
        else:
            code = 0
    except VerifySetupError as err:
        evidence["error"] = str(err)
        evidence["overall"] = "error"
        code = 2
    except Exception as err:  # never let a raw traceback (which could embed values) reach the console
        evidence["error"] = "%s: %s" % (type(err).__name__, err)
        evidence["overall"] = "error"
        code = 2

    payload = redactor.scrub_json(evidence)
    text = json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True)
    print(text)
    if args.evidence_out is not None:
        args.evidence_out.write_text(text + "\n")
        args.evidence_out.chmod(0o600)
    summary = payload.get("summary", {})
    sys.stderr.write(
        "verify-security-batch2: overall=%s pass=%s fail=%s warn=%s skip=%s\n"
        % (payload.get("overall"), summary.get("pass", 0), summary.get("fail", 0),
           summary.get("warn", 0), summary.get("skip", 0))
    )
    return code


if __name__ == "__main__":
    sys.exit(main())
