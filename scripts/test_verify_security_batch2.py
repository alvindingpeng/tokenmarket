"""Isolated unit tests for verify-security-batch2.py; never access production.

Everything runs against temporary SQLite files and a mock HTTP server bound to 127.0.0.1 on an
ephemeral port. The suite asserts the leak-free evidence contract: no API key, fingerprint, auth
secret, session token or envelope ciphertext may ever appear in stdout/stderr/evidence output.
"""
import base64
import hashlib
import hmac
import http.server
import importlib.util
import json
import re
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
SCRIPT = SCRIPTS / "verify-security-batch2.py"

spec = importlib.util.spec_from_file_location("verify_security_batch2", SCRIPT)
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)

AUTH_SECRET = "unit-test-auth-secret-0123456789abcdef"
KEY_VALID = "sk-switchyard-VALID00000000000000000000000000000000000001"
KEY_DISABLED = "sk-switchyard-DISAB00000000000000000000000000000000000002"
KEY_EXPIRED = "sk-switchyard-EXPIR00000000000000000000000000000000000003"
KEY_EXTRA = "sk-switchyard-EXTRA00000000000000000000000000000000000004"


def b64decode(segment):
    return base64.urlsafe_b64decode(segment + "=" * (-len(segment) % 4))


def verify_jwt(token):
    """Independent HS256 verification of the temporary session token (test-side oracle)."""
    try:
        header_b64, payload_b64, signature_b64 = token.split(".")
    except ValueError:
        return None, None
    signing_input = (header_b64 + "." + payload_b64).encode("ascii")
    expected = hmac.new(AUTH_SECRET.encode("utf-8"), signing_input, hashlib.sha256).digest()
    if not hmac.compare_digest(b64decode(signature_b64), expected):
        return None, None
    return json.loads(b64decode(header_b64)), json.loads(b64decode(payload_b64))


class MockConfig(dict):
    """Behaviour switches for the mock service."""


class MockHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):  # keep test output clean
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode("utf-8", "replace") if length else ""
        self.server.requests.append({
            "method": self.command,
            "path": self.path,
            "authorization": self.headers.get("Authorization", ""),
            "x_api_key": self.headers.get("x-api-key", ""),
            "cookie": self.headers.get("Cookie", ""),
            "step_up": self.headers.get("X-Step-Up-Token", ""),
            "content_type": self.headers.get("Content-Type", ""),
            "body": body,
        })
        if self.path == "/api/v1/apikey/login":
            self.answer(self.login_outcome())
        elif self.path == "/api/v1/apikey/stats":
            if self.login_outcome()[0] == 200:
                status, payload = self.server.config.get("stats", (200, default_stats_payload()))
                self.answer((status, payload))
            else:
                self.answer((401, {"code": 401, "message": "Authentication failed"}))
        elif self.path == "/api/v1/user/status":
            self.answer(self.session_outcome("user_status"))
        elif self.path == "/api/v1/user/manage/create":
            self.answer(self.session_outcome("create"))
        else:
            self.answer((404, {"code": 404, "message": "not found"}))

    def bearer(self):
        authorization = self.headers.get("Authorization", "")
        if authorization.startswith("Bearer "):
            return authorization[len("Bearer "):]
        return self.headers.get("x-api-key", "")

    def login_outcome(self):
        key = self.bearer()
        return self.server.config.get("login", {}).get(key, self.server.config.get("login_default", (401, {"code": 401, "message": "Authentication failed"})))

    def session_outcome(self, which):
        secret = self.server.config.get("jwt_secret")
        if secret:
            token = ""
            for part in self.headers.get("Cookie", "").split(";"):
                name, _, value = part.strip().partition("=")
                if name == "auth":
                    token = value
            header, payload = verify_jwt(token)
            self.server.claims.append({"header": header, "payload": payload, "token": token})
            if header is None:
                self.server.jwt_errors.append("signature or shape rejected")
                return (401, {"code": 401, "message": "Authentication failed"})
            for name, expected in (self.server.config.get("jwt_expect") or {}).items():
                if payload.get(name) != expected:
                    self.server.jwt_errors.append("claim %s mismatch" % name)
                    return (401, {"code": 401, "message": "Authentication failed"})
        return self.server.config.get(which, (200, {"code": 200, "message": "success"}))

    def answer(self, outcome):
        status, payload = outcome
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def default_stats_payload():
    return {
        "code": 200,
        "message": "success",
        "data": {
            "stats": {"api_key_id": 1, "input_token": 5, "output_token": 7, "request_success": 3},
            "info": {
                "id": 1,
                "user_id": 1,
                "name": "retained",
                "key_prefix": "sk-switchyar",
                "key_last4": "0001",
                "enabled": True,
                "expire_at": 0,
                "supported_models": ["gpt-test"],
                "routing_preference": "inherit",
            },
        },
    }


def default_stats_payload_leaking():
    payload = default_stats_payload()
    payload["data"]["info"]["api_key"] = KEY_VALID
    payload["data"]["info"]["key_hash"] = hashlib.sha256(KEY_VALID.encode()).hexdigest()
    return payload


class MockServer(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, config):
        super().__init__(("127.0.0.1", 0), MockHandler)
        self.config = MockConfig(config)
        self.requests = []
        self.claims = []
        self.jwt_errors = []


def sha256_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class VerifySecurityBatch2Test(unittest.TestCase):
    maxDiff = None

    # ------------------------------------------------------------------ fixtures

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.before = self.root / "before.db"
        self.after = self.root / "after.db"
        self.secrets = [AUTH_SECRET, KEY_VALID, KEY_DISABLED, KEY_EXPIRED, KEY_EXTRA]
        self.make_before([
            (1, "retained-valid", KEY_VALID),
            (2, "retained-disabled", KEY_DISABLED),
            (3, "retained-expired", KEY_EXPIRED),
        ])
        self.make_after([
            (1, "retained-valid", KEY_VALID, 1, 0),
            (2, "retained-disabled", KEY_DISABLED, 0, 0),
            (3, "retained-expired", KEY_EXPIRED, 1, int(time.time()) - 3600),
        ])
        self.server = MockServer(self.default_config())
        self.addCleanup(self.server.server_close)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.server.shutdown)
        self.base_url = "http://127.0.0.1:%d" % self.server.server_address[1]

    def default_config(self):
        return {
            "login": {KEY_VALID: (200, {"code": 200, "message": "success"})},
            "login_default": (401, {"code": 401, "message": "Authentication failed"}),
            "stats": (200, default_stats_payload()),
            "user_status": (200, {"code": 200, "message": "success", "data": {"id": 1, "role": "admin"}}),
            "create": (428, {"code": 428, "message": "Step-up verification required", "error_code": "step_up_required"}),
            "jwt_secret": AUTH_SECRET,
            "jwt_expect": {"uid": 1, "role": "admin", "ver": 7, "iss": "switchyard"},
        }

    def make_before(self, keys, auth_secret=AUTH_SECRET, plaintext_column=True):
        if self.before.exists():
            self.before.unlink()
        connection = sqlite3.connect(self.before)
        connection.execute(
            "CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, role TEXT, status TEXT, token_version INTEGER)"
        )
        columns = "id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT"
        columns += ", api_key TEXT" if plaintext_column else ", key_hash TEXT"
        columns += ", enabled INTEGER, expire_at INTEGER"
        connection.execute("CREATE TABLE api_keys (" + columns + ")")
        connection.execute("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
        for key_id, name, plaintext in keys:
            secret_column = "api_key" if plaintext_column else "key_hash"
            value = plaintext if plaintext_column else hashlib.sha256(plaintext.encode()).hexdigest()
            connection.execute(
                "INSERT INTO api_keys (id, user_id, name, " + secret_column + ", enabled, expire_at) VALUES (?,?,?,?,1,0)",
                (key_id, 1, name, value),
            )
        if auth_secret is not None:
            connection.execute("INSERT INTO settings VALUES ('auth_secret', ?)", (auth_secret,))
        connection.commit()
        connection.close()

    def make_after(self, rows, admin=(1, "root", "admin", "active", 7)):
        if self.after.exists():
            self.after.unlink()
        connection = sqlite3.connect(self.after)
        connection.execute(
            "CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, role TEXT, status TEXT, token_version INTEGER)"
        )
        connection.execute(
            "CREATE TABLE api_keys (id INTEGER PRIMARY KEY, user_id INTEGER, name TEXT, key_hash TEXT,"
            " key_prefix TEXT, key_last4 TEXT, enabled INTEGER, expire_at INTEGER)"
        )
        for key_id, name, plaintext, enabled, expire_at in rows:
            connection.execute(
                "INSERT INTO api_keys VALUES (?,?,?,?,?,?,?,?)",
                (key_id, 1, name, hashlib.sha256(plaintext.encode()).hexdigest(),
                 plaintext[:12], plaintext[-4:], enabled, expire_at),
            )
        if admin is not None:
            connection.execute("INSERT INTO users VALUES (?,?,?,?,?)", admin)
            connection.execute("INSERT INTO users VALUES (?,?,?,?,?)", (2, "plain", "user", "active", 0))
        connection.commit()
        connection.close()

    # ------------------------------------------------------------------- running

    def run_tool(self, *extra, base_url=None):
        command = [
            sys.executable, str(SCRIPT),
            "--before-db", str(self.before),
            "--after-db", str(self.after),
            "--base-url", base_url or self.base_url,
        ]
        command.extend(extra)
        return subprocess.run(command, capture_output=True, text=True)

    def evidence_of(self, result):
        return json.loads(result.stdout)

    def checks_by_name(self, evidence):
        return {check["check"]: check for check in evidence["checks"]}

    def assert_no_leaks(self, *texts):
        for text in texts:
            self.assertNotRegex(text, r"sk-[A-Za-z0-9_-]{8,}", "API key material leaked")
            self.assertNotRegex(text, r"\b[0-9a-fA-F]{64}\b", "fingerprint leaked")
            self.assertNotRegex(text, r"sysec\$", "envelope ciphertext leaked")
            self.assertNotRegex(text, r"\beyJ[A-Za-z0-9_-]{6,}\.", "JWT leaked")
            for secret in self.secrets:
                self.assertNotIn(secret, text, "secret literal leaked")

    def recorded_tokens(self):
        tokens = []
        for request in self.server.requests:
            for part in request["cookie"].split(";"):
                name, _, value = part.strip().partition("=")
                if name == "auth" and value:
                    tokens.append(value)
        return tokens

    # ------------------------------------------------------------------ unit tests

    def test_sign_temp_jwt_matches_auth_go_contract(self):
        now = 1_700_000_000
        token = verify.sign_temp_jwt(AUTH_SECRET, 41, "admin", 7, now=now)
        self.assertEqual(len(token.split(".")), 3)
        header, payload = verify_jwt(token)
        self.assertEqual(header, {"alg": "HS256", "typ": "JWT"})
        self.assertEqual(payload["uid"], 41)
        self.assertEqual(payload["role"], "admin")
        self.assertEqual(payload["ver"], 7)
        self.assertEqual(payload["iss"], "switchyard")
        self.assertEqual(payload["iat"], now)
        self.assertEqual(payload["nbf"], now)
        self.assertEqual(payload["exp"] - payload["iat"], 300)  # five minutes only
        self.assertNotIn(AUTH_SECRET, token)

    def test_redactor_strips_keys_hashes_envelopes_jwt_and_headers(self):
        redactor = verify.Redactor()
        redactor.register("literal-secret-value")
        jwt = verify.sign_temp_jwt(AUTH_SECRET, 1, "admin", 7)
        dirty = (
            "literal-secret-value " + KEY_VALID + " "
            + hashlib.sha256(KEY_VALID.encode()).hexdigest() + " "
            + "sysec$1$keyid$YWJj$ZGVm$Z2hp$amts$bW5v " + jwt + " "
            + "Cookie: auth=" + jwt + " Authorization: Bearer " + KEY_VALID
        )
        clean = redactor.scrub(dirty)
        self.assert_no_leaks(clean)
        nested = redactor.scrub_json({"outer": [{"inner": dirty}]})
        self.assert_no_leaks(json.dumps(nested))

    def test_select_test_keys_prefers_one_authenticating_and_one_rejected(self):
        now = 1_700_000_000
        old = [
            verify.LegacyKey(1, "a", KEY_VALID, True, 0),
            verify.LegacyKey(2, "b", KEY_DISABLED, False, 0),
            verify.LegacyKey(3, "c", KEY_EXPIRED, True, now - 10),
        ]
        live = [
            verify.LiveKey(1, hashlib.sha256(KEY_VALID.encode()).hexdigest(), "a", True, 0),
            verify.LiveKey(2, hashlib.sha256(KEY_DISABLED.encode()).hexdigest(), "b", False, 0),
            verify.LiveKey(3, hashlib.sha256(KEY_EXPIRED.encode()).hexdigest(), "c", True, now - 10),
        ]
        retained, chosen = verify.select_test_keys(old, live, 2, now)
        self.assertEqual(len(retained), 3)
        self.assertEqual([key.key_id for key in chosen], [1, 2])
        self.assertEqual(verify.key_state(chosen[0].live, now), "enabled")
        self.assertEqual(verify.key_state(chosen[1].live, now), "disabled")
        self.assertNotIn(KEY_VALID, repr(chosen[0]))
        _, single = verify.select_test_keys(old, live, 1, now)
        self.assertEqual([key.key_id for key in single], [1])

    def test_select_test_keys_ignores_replaced_credentials(self):
        now = 1_700_000_000
        old = [verify.LegacyKey(1, "a", KEY_VALID, True, 0)]
        live = [verify.LiveKey(1, hashlib.sha256(KEY_EXTRA.encode()).hexdigest(), "a", True, 0)]
        retained, chosen = verify.select_test_keys(old, live, 2, now)
        self.assertEqual(retained, [])
        self.assertEqual(chosen, [])

    def test_find_forbidden_fields_locates_nested_api_key_and_key_hash(self):
        node = {"data": {"info": {"key_prefix": "sk", "key_hash": "x"}, "list": [{"api_key": "y"}]}}
        hits = verify.find_forbidden_fields(node)
        self.assertEqual(sorted(hits), ["$.data.info.key_hash", "$.data.list[0].api_key"])
        self.assertEqual(verify.secret_pattern_hits({"info": {"key_prefix": "sk-switchyar", "name": KEY_VALID[:20]}}), [])
        self.assertIn("api key material", verify.secret_pattern_hits({"info": {"note": KEY_VALID}}))

    # ----------------------------------------------------------------- end-to-end

    def test_full_run_passes_with_leak_free_evidence(self):
        before_hash, after_hash = sha256_file(self.before), sha256_file(self.after)
        evidence_out = self.root / "evidence.json"
        result = self.run_tool("--evidence-out", str(evidence_out))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(evidence["overall"], "pass")
        self.assertEqual(evidence["summary"]["fail"], 0)
        self.assertEqual(evidence["summary"]["warn"], 0)
        checks = self.checks_by_name(evidence)

        login_checks = [c for c in evidence["checks"] if c["check"] == "apikey_login_retained_key"]
        self.assertEqual(len(login_checks), 2)
        by_state = {c["key_state"]: c for c in login_checks}
        self.assertEqual(by_state["enabled"]["status"], "pass")
        self.assertEqual(by_state["enabled"]["expected"], "authenticate (200)")
        self.assertEqual(by_state["disabled"]["status"], "pass")
        self.assertEqual(by_state["disabled"]["expected"], "reject (401/403)")
        self.assertEqual(checks["random_wrong_api_key_rejected"]["status"], "pass")
        self.assertEqual(checks["random_wrong_api_key_rejected"]["http_status"], 401)

        stats_check = checks["apikey_stats_response_no_secret_fields"]
        self.assertEqual(stats_check["status"], "pass")
        self.assertEqual(stats_check["forbidden_fields"], [])
        self.assertEqual(stats_check["mask_fields_present"], ["key_last4", "key_prefix"])
        self.assertEqual(stats_check["secret_pattern_hits"], [])

        self.assertEqual(checks["admin_temp_session"]["status"], "pass")
        self.assertEqual(checks["user_status_plain_session"]["http_status"], 200)
        stepup = checks["high_risk_user_create_requires_stepup"]
        self.assertEqual(stepup["status"], "pass")
        self.assertEqual(stepup["http_status"], 428)
        self.assertEqual(stepup["error_code"], "step_up_required")
        self.assertEqual(stepup["request_body"], "{}")
        self.assertIs(stepup["step_up_header_sent"], False)
        self.assertEqual(checks["stepup_probe_no_side_effects"]["status"], "pass")

        create_calls = [r for r in self.server.requests if r["path"] == "/api/v1/user/manage/create"]
        self.assertEqual(len(create_calls), 1)
        self.assertEqual(create_calls[0]["body"], "{}")
        self.assertEqual(create_calls[0]["step_up"], "")
        self.assertTrue(create_calls[0]["content_type"].startswith("application/json"))
        self.assertFalse(self.server.jwt_errors)

        self.assert_no_leaks(result.stdout, result.stderr)
        for token in self.recorded_tokens():
            self.assertNotIn(token, result.stdout + result.stderr)
        self.assert_no_leaks(evidence_out.read_text())
        self.assertEqual(evidence_out.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads(evidence_out.read_text()), evidence)
        # read-only contract: neither database changed by a single byte
        self.assertEqual(sha256_file(self.before), before_hash)
        self.assertEqual(sha256_file(self.after), after_hash)

    def test_temp_jwt_binds_active_admin_with_five_minute_ttl(self):
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.server.claims), 2)  # user/status + manage/create
        for seen in self.server.claims:
            header, payload = seen["header"], seen["payload"]
            self.assertEqual(header["alg"], "HS256")
            self.assertEqual(payload["uid"], 1)
            self.assertEqual(payload["role"], "admin")
            self.assertEqual(payload["ver"], 7)
            self.assertEqual(payload["iss"], "switchyard")
            self.assertEqual(payload["exp"] - payload["iat"], 300)
            self.assertGreaterEqual(payload["iat"], int(time.time()) - 60)
        self.assertFalse(self.server.jwt_errors)

    def test_disabled_key_accepted_by_server_fails(self):
        self.server.config["login"][KEY_DISABLED] = (200, {"code": 200, "message": "success"})
        result = self.run_tool()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(evidence["overall"], "fail")
        by_state = {c["key_state"]: c for c in evidence["checks"] if c["check"] == "apikey_login_retained_key"}
        self.assertEqual(by_state["disabled"]["status"], "fail")
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_expired_key_rejection_is_the_expected_outcome(self):
        # only the expired key is retained: rejection must count as a pass, not a failure
        self.make_before([(3, "retained-expired", KEY_EXPIRED)])
        self.make_after([(3, "retained-expired", KEY_EXPIRED, 1, int(time.time()) - 3600)])
        self.server.config["login"][KEY_EXPIRED] = (401, {"code": 401, "message": "API key has expired"})
        result = self.run_tool("--skip-stepup")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        expired = [c for c in evidence["checks"] if c["check"] == "apikey_login_retained_key"]
        self.assertEqual(len(expired), 1)
        self.assertEqual(expired[0]["key_state"], "expired")
        self.assertEqual(expired[0]["status"], "pass")
        self.assertEqual(expired[0]["expected"], "reject (401/403)")

    def test_random_wrong_key_accepted_fails(self):
        self.server.config["login_default"] = (200, {"code": 200, "message": "success"})
        result = self.run_tool()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(self.checks_by_name(evidence)["random_wrong_api_key_rejected"]["status"], "fail")
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_stats_leaking_api_key_or_key_hash_fails_without_reprinting(self):
        self.server.config["stats"] = (200, default_stats_payload_leaking())
        result = self.run_tool()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        stats_check = self.checks_by_name(evidence)["apikey_stats_response_no_secret_fields"]
        self.assertEqual(stats_check["status"], "fail")
        self.assertEqual(stats_check["forbidden_fields"], ["$.data.info.api_key", "$.data.info.key_hash"])
        self.assertIn("api key material", stats_check["secret_pattern_hits"])
        self.assertIn("fingerprint", stats_check["secret_pattern_hits"])
        # the leaked values themselves must not be echoed anywhere
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_stats_without_mask_is_a_failure(self):
        payload = default_stats_payload()
        del payload["data"]["info"]["key_last4"]
        self.server.config["stats"] = (200, payload)
        result = self.run_tool()
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        stats_check = self.checks_by_name(self.evidence_of(result))["apikey_stats_response_no_secret_fields"]
        self.assertEqual(stats_check["status"], "fail")

    def test_missing_stepup_gate_fails(self):
        for status, payload in (
            (200, {"code": 200, "message": "success", "data": {"id": 9}}),
            (400, {"code": 400, "message": "invalid username"}),
        ):
            with self.subTest(status=status):
                self.server.config["create"] = (status, payload)
                result = self.run_tool()
                self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                evidence = self.evidence_of(result)
                stepup = self.checks_by_name(evidence)["high_risk_user_create_requires_stepup"]
                self.assertEqual(stepup["status"], "fail")
                self.assertEqual(stepup["http_status"], status)
                self.assert_no_leaks(result.stdout, result.stderr)

    def test_default_password_gate_is_reported_as_unverified_warning(self):
        self.server.config["create"] = (403, {"code": 403, "message": "change the default admin password first"})
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        stepup = self.checks_by_name(self.evidence_of(result))["high_risk_user_create_requires_stepup"]
        self.assertEqual(stepup["status"], "warn")
        self.assertIn("428", stepup["detail"])
        self.assertIn("step_up_required", stepup["detail"])

    def test_rotated_auth_secret_warns_and_documents_safe_alternative(self):
        self.make_before([(1, "retained-valid", KEY_VALID)], auth_secret="another-secret-9876543210abcdef")
        self.secrets.append("another-secret-9876543210abcdef")
        self.server.config["user_status"] = (401, {"code": 401, "message": "Authentication failed"})
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        checks = self.checks_by_name(self.evidence_of(result))
        self.assertEqual(checks["admin_temp_session"]["status"], "warn")
        self.assertEqual(checks["high_risk_user_create_requires_stepup"]["status"], "skip")
        self.assertIn("428", checks["high_risk_user_create_requires_stepup"]["detail"])
        create_calls = [r for r in self.server.requests if r["path"] == "/api/v1/user/manage/create"]
        self.assertEqual(create_calls, [])
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_skip_stepup_signs_no_session_and_documents_safe_alternative(self):
        result = self.run_tool("--skip-stepup")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        stepup = self.checks_by_name(evidence)["high_risk_user_create_requires_stepup"]
        self.assertEqual(stepup["status"], "skip")
        self.assertIn("step_up_required", stepup["detail"])
        self.assertIn("428", stepup["detail"])
        self.assertIn("in memory", stepup["detail"])
        self.assertIn("5 minutes", stepup["detail"])
        user_calls = [r for r in self.server.requests if r["path"].startswith("/api/v1/user/")]
        self.assertEqual(user_calls, [])  # nothing signed, nothing probed
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_envelope_auth_secret_skips_stepup_without_loading_key_material(self):
        self.make_before([(1, "retained-valid", KEY_VALID)], auth_secret="sysec$1$keyid$YWJj$ZGVm$Z2hp$amts$bW5v")
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        stepup = self.checks_by_name(evidence)["high_risk_user_create_requires_stepup"]
        self.assertEqual(stepup["status"], "skip")
        self.assertIn("envelope-encrypted", stepup["detail"])
        self.assertIn("master key", stepup["detail"])
        self.assertEqual([r for r in self.server.requests if r["path"].startswith("/api/v1/user/")], [])
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_missing_auth_secret_skips_stepup_with_reason(self):
        self.make_before([(1, "retained-valid", KEY_VALID)], auth_secret=None)
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        stepup = self.checks_by_name(self.evidence_of(result))["high_risk_user_create_requires_stepup"]
        self.assertEqual(stepup["status"], "skip")
        self.assertIn("auth_secret", stepup["detail"])

    def test_short_retained_set_warns_and_fail_on_warn_exits_nonzero(self):
        self.make_before([(1, "retained-valid", KEY_VALID)])
        self.make_after([(1, "retained-valid", KEY_VALID, 1, 0)])
        result = self.run_tool()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(evidence["overall"], "pass_with_warnings")
        coverage = self.checks_by_name(evidence)["retained_api_key_coverage"]
        self.assertEqual(coverage["status"], "warn")
        self.assertEqual(coverage["tested"], 1)
        strict = self.run_tool("--fail-on-warn")
        self.assertEqual(strict.returncode, 1, strict.stdout + strict.stderr)

    def test_unreachable_service_fails_cleanly_without_leaks(self):
        result = self.run_tool(base_url="http://127.0.0.1:1")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(evidence["overall"], "fail")
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_missing_legacy_plaintext_column_exits_two(self):
        self.make_before([(1, "retained-valid", KEY_VALID)], plaintext_column=False)
        result = self.run_tool()
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        evidence = self.evidence_of(result)
        self.assertEqual(evidence["overall"], "error")
        self.assertIn("plaintext api_key column", evidence["error"])
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_absent_before_database_exits_two(self):
        self.before.unlink()
        result = self.run_tool()
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertEqual(self.evidence_of(result)["overall"], "error")
        self.assert_no_leaks(result.stdout, result.stderr)

    def test_evidence_always_reports_the_optional_credential_check(self):
        result = self.run_tool("--skip-stepup")
        evidence = self.evidence_of(result)
        optional = self.checks_by_name(evidence)["recoverable_credentials_decrypt_consistency"]
        self.assertEqual(optional["status"], "skip")
        self.assertIn("switchyard secret migrate", optional["detail"])


if __name__ == "__main__":
    unittest.main()
