"""Isolated tests for scripts/deploy-security-batch2.py.

Every test runs against temporary directories and mocked command execution (or the real helper
tools on temporary databases). Nothing here reads or writes production paths, production services
or the network: the production layout is only ever constructed as a value, never operated on.
"""

import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

SCRIPTS = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "deploy_security_batch2", SCRIPTS / "deploy-security-batch2.py")
deploy = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(deploy)

EXPECTED_VERSION = "v9.9.9-security-batch2"
LEGACY_API_KEY = "sk-legacy-secret-0001"
LEGACY_CHANNEL_KEY = "upstream-secret-0001"
MIGRATE_LOG = (
    "secret master key ready (key ID test-key-id)\n"
    "secret migration done: settings_encrypted=3 channel_keys_encrypted=2 "
    "already_encrypted=1 empty_skipped=4\n"
)
COMPARE_OK = json.dumps({
    "users_rows_preserved": True, "api_keys_columns_preserved": True,
    "legacy_api_key_fingerprints_match": True, "settings_changed_only_to_envelopes": True,
    "channel_secrets_enveloped": True,
})
INSPECT_OK = json.dumps({
    "integrity": "ok", "users": 1, "api_keys": 1, "channel_keys": 1, "billing_records": 0,
    "balance_ledgers": 0, "foreign_key_violations": 0, "negative_frozen": 0,
    "api_key_plaintext_column": False, "api_key_hash_schema": True, "invalid_api_key_hashes": 0,
})
COMPACT_OK = json.dumps({"compacted": True, "integrity": "ok", "physical_media_secure_erasure": False})
SITE_TEXT = (
    "server {\n"
    "    server_name prod.example.com;\n"
    "    location / {\n"
    "        proxy_pass http://127.0.0.1:8088;\n"
    "    }\n"
    "}\n"
)


class FakeRunner:
    """Scripted command executor: records every call, answers from a small dispatch table."""

    def __init__(self, *, candidate, backup_json, probe_codes=None, failures=None,
                 tool_outputs=None, migrate_output=MIGRATE_LOG):
        self.candidate = str(candidate)
        self.backup_json = backup_json
        self.probe_codes = list(probe_codes or ("503", "200"))
        self.failures = failures or {}
        self.migrate_output = migrate_output
        self.migrate_count = 0
        self.tool_outputs = {
            "security-batch2-backup.py": backup_json,
            "security-batch2-compare.py": COMPARE_OK,
            "security-batch2-inspect.py": INSPECT_OK,
            "security-batch2-compact.py": COMPACT_OK,
        }
        self.tool_outputs.update(tool_outputs or {})
        self.calls = []
        self.commands = []

    def __call__(self, args, env=None, cwd=None):
        args = [str(a) for a in args]
        joined = " ".join(args)
        self.calls.append((tuple(args), dict(env) if env else None, str(cwd) if cwd else None))
        self.commands.append(joined)
        for needle, result in self.failures.items():
            if needle in joined:
                return result
        if args[0] == "curl":
            code = self.probe_codes.pop(0) if len(self.probe_codes) > 1 else self.probe_codes[0]
            return deploy.Completed(0, code)
        if args[0] == "nginx":
            return deploy.Completed(0)
        if args[0] == "systemctl":
            if args[1] == "show":
                return deploy.Completed(0, "0")
            if args[1] == "is-active":
                return deploy.Completed(0, "active")
            return deploy.Completed(0)
        if args[0] == self.candidate and args[1:3] == ["secret", "migrate"]:
            self.migrate_count += 1
            output = self.migrate_output if self.migrate_count == 1 else "secret migration done: settings_encrypted=0 channel_keys_encrypted=0 already_encrypted=6 empty_skipped=4"
            return deploy.Completed(0, "", output) # Go logs to stderr
        if args[0] == self.candidate and args[1:3] == ["secret", "status"]:
            return deploy.Completed(0, "", "secret status (key ID test): would_encrypt=0 already_encrypted=6 empty_skipped=4")
        if len(args) >= 2 and args[1] == "version":
            return deploy.Completed(0, "Built At: now\nGo Version: go1.24\nAuthor: ops\n"
                                       "Commit ID: abc\nVersion: %s\n" % EXPECTED_VERSION)
        if args[0] == sys.executable and args[1].endswith(".py"):
            output = self.tool_outputs.get(Path(args[1]).name)
            if output is not None:
                return deploy.Completed(0, output)
        return deploy.Completed(0)


def build_legacy_database(path):
    Path(path).unlink(missing_ok=True)
    connection = sqlite3.connect(path)
    connection.execute("CREATE TABLE users (id INTEGER PRIMARY KEY, frozen REAL)")
    connection.execute("INSERT INTO users VALUES (1, 0.0)")
    connection.execute("CREATE TABLE api_keys (id INTEGER PRIMARY KEY, api_key TEXT)")
    connection.execute("INSERT INTO api_keys VALUES (1, ?)", (LEGACY_API_KEY,))
    connection.execute("CREATE TABLE channel_keys (id INTEGER PRIMARY KEY, key TEXT)")
    connection.execute("INSERT INTO channel_keys VALUES (1, ?)", (LEGACY_CHANNEL_KEY,))
    connection.execute("CREATE TABLE billing_records (id INTEGER PRIMARY KEY)")
    connection.execute("CREATE TABLE balance_ledgers (id INTEGER PRIMARY KEY)")
    connection.execute("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
    connection.execute("INSERT INTO settings VALUES ('auth_secret', 'session-signing-secret')")
    connection.commit()
    connection.close()


def build_migrated_database(path, residue=False):
    Path(path).unlink(missing_ok=True)
    connection = sqlite3.connect(path)
    connection.execute("CREATE TABLE users (id INTEGER PRIMARY KEY, frozen REAL)")
    connection.execute("INSERT INTO users VALUES (1, 0.0)")
    connection.execute("CREATE TABLE api_keys (id INTEGER PRIMARY KEY, key_hash TEXT)")
    connection.execute("INSERT INTO api_keys VALUES (1, ?)",
                       (hashlib.sha256(LEGACY_API_KEY.encode()).hexdigest(),))
    connection.execute("CREATE TABLE channel_keys (id INTEGER PRIMARY KEY, key TEXT)")
    connection.execute("INSERT INTO channel_keys VALUES (1, 'sysec$1$AAAA')")
    connection.execute("CREATE TABLE billing_records (id INTEGER PRIMARY KEY)")
    connection.execute("CREATE TABLE balance_ledgers (id INTEGER PRIMARY KEY)")
    connection.execute("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
    connection.execute("INSERT INTO settings VALUES ('auth_secret', 'sysec$1$BBBB')")
    if residue:
        connection.execute("INSERT INTO settings VALUES ('leaked', ?)", (LEGACY_API_KEY,))
    connection.commit()
    connection.close()


class TempLayoutCase(unittest.TestCase):
    """Shared temporary layout: root, nginx site, drop-in dir, key backup root."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        base = Path(self.tmp.name)
        self.layout = deploy.Layout(
            root=base / "opt" / "switchyard",
            site=base / "etc" / "nginx" / "sites-available" / "switchyard-tt",
            dropin_dir=base / "etc" / "systemd" / "system" / "switchyard.service.d",
            key_backup_root=base / "root-home",
            health_url="http://127.0.0.1:8088/healthz",
        )
        self.layout.root.mkdir(parents=True)
        (self.layout.root / "bin").mkdir()
        (self.layout.root / "data").mkdir()
        self.layout.site.parent.mkdir(parents=True)
        self.layout.site.write_text(SITE_TEXT)
        self.candidate = base / "candidate-switchyard"
        self.candidate.write_bytes(b"candidate-binary-v2")
        self.candidate.chmod(0o750)
        (self.layout.root / "bin" / "switchyard").write_bytes(b"previous-binary-v1")
        (self.layout.root / "data" / "config.json").write_text(
            '{"database": {"type": "sqlite", "path": "data/data.db"}}')
        self.legacy_db = base / "legacy.db"
        build_legacy_database(self.legacy_db)

    def make_recovery(self):
        recovery = self.layout.releases / "20260101-000000-security-batch2"
        recovery.mkdir(parents=True)
        shutil.copyfile(self.legacy_db, recovery / "pre-upgrade.db")
        shutil.copyfile(self.layout.bin, recovery / "switchyard.previous")
        shutil.copyfile(self.layout.config, recovery / "config.json")
        return recovery

    def backup_json(self, recovery):
        return json.dumps({
            "recovery_directory": str(recovery),
            "database_sha256": deploy.sha256_file(recovery / "pre-upgrade.db"),
            "file_sha256": {
                "pre-upgrade.db": deploy.sha256_file(recovery / "pre-upgrade.db"),
                "switchyard.previous": deploy.sha256_file(recovery / "switchyard.previous"),
                "config.json": deploy.sha256_file(recovery / "config.json"),
            },
            "result": "backup-created",
            "database_restored": False,
        })

    def deployer(self, runner, **overrides):
        kwargs = dict(
            candidate=self.candidate,
            sha256=deploy.sha256_file(self.candidate),
            version=EXPECTED_VERSION,
            tools_dir=SCRIPTS,
            layout=self.layout,
            run=runner,
            health=lambda: {"status": "ok", "db": "ok", "version": EXPECTED_VERSION,
                            "active_requests": 0},
            urandom=lambda size: b"\x07" * size,
            sleep=lambda seconds: None,
        )
        kwargs.update(overrides)
        return deploy.Deployer(**kwargs)

    def command_index(self, runner, needle):
        for index, command in enumerate(runner.commands):
            if needle in command:
                return index
        self.fail("command never executed: " + needle)


class DeployHelperTests(TempLayoutCase):
    def test_restored_ingress_rejects_502(self):
        driver = self.deployer(FakeRunner(candidate=self.candidate, backup_json="{}", probe_codes=["502"]))
        with self.assertRaises(deploy.DeployError):
            driver.confirm_entry_open()


    def test_master_key_created_from_urandom_never_overwritten(self):
        calls = []

        def urandom(size):
            calls.append(size)
            return b"\x11" * size

        worker = self.deployer(None, run=lambda *a, **k: None, urandom=urandom)
        info = worker.ensure_master_key()
        self.assertEqual(info["state"], "created")
        self.assertEqual(calls, [32])
        key_path = self.layout.master_key
        self.assertEqual(key_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.layout.keys_dir.stat().st_mode & 0o777, 0o700)
        first_bytes = key_path.read_bytes()
        self.assertEqual(deploy.decode_master_key(first_bytes), b"\x11" * 32)

        def explode(size):
            raise AssertionError("os.urandom must not run for an existing key")

        second = self.deployer(None, run=lambda *a, **k: None, urandom=explode)
        info = second.ensure_master_key()
        self.assertEqual(info["state"], "existing")
        self.assertEqual(key_path.read_bytes(), first_bytes)

    def test_master_key_rejects_invalid_existing_material(self):
        self.layout.keys_dir.mkdir(parents=True, exist_ok=True)
        self.layout.master_key.write_text("not-a-key\n")
        worker = self.deployer(None, run=lambda *a, **k: None)
        with self.assertRaises(deploy.DeployError):
            worker.ensure_master_key()

    def test_decode_master_key_contract(self):
        good_b64 = base64.b64encode(bytes(range(32))).decode()
        self.assertEqual(deploy.decode_master_key(good_b64), bytes(range(32)))
        self.assertEqual(deploy.decode_master_key(good_b64.encode()), bytes(range(32)))
        self.assertEqual(deploy.decode_master_key("01" * 32), b"\x01" * 32)
        with self.assertRaises(deploy.DeployError):
            deploy.decode_master_key("")
        with self.assertRaises(deploy.DeployError):
            deploy.decode_master_key("00" * 32)  # all zero is refused
        with self.assertRaises(deploy.DeployError):
            deploy.decode_master_key("A" * 43 + "=")  # base64 of all zero is refused
        with self.assertRaises(deploy.DeployError):
            deploy.decode_master_key("short")

    def test_verify_candidate_checksum_mismatch_fails_before_any_command(self):
        runner = FakeRunner(candidate=self.candidate, backup_json="{}")
        worker = self.deployer(runner, sha256="0" * 64)
        with self.assertRaisesRegex(deploy.DeployError, "checksum mismatch"):
            worker.verify_candidate()
        self.assertEqual(runner.calls, [])

    def test_verify_candidate_version_mismatch(self):
        runner = FakeRunner(candidate=self.candidate, backup_json="{}")
        worker = self.deployer(runner, version="v0.0.1-other")
        with self.assertRaisesRegex(deploy.DeployError, "version mismatch"):
            worker.verify_candidate()

    def test_pause_ingress_requires_exactly_one_marker(self):
        runner = FakeRunner(candidate=self.candidate, backup_json="{}")
        worker = self.deployer(runner)
        release = self.layout.releases / "x"
        release.mkdir(parents=True)
        self.layout.site.write_text("server {}  # no marker\n")
        with self.assertRaises(deploy.DeployError):
            worker.pause_ingress(release)
        self.assertEqual(self.layout.site.read_text(), "server {}  # no marker\n")
        self.layout.site.write_text(SITE_TEXT + deploy.LOCATION_MARKER)
        with self.assertRaises(deploy.DeployError):
            worker.pause_ingress(release)
        self.assertNotIn("return 503", self.layout.site.read_text())

    def test_scrub_metadata_redacts_secrets_but_keeps_hashes(self):
        cleaned = deploy.scrub_metadata({
            "candidate_sha256": "a" * 64,
            "secret": "hunter2",
            "api_key": "sk-live-123",
            "nested": {"token": "t", "note": "before sysec$1$AAAA after"},
            "counts": [1, "sysec$1$BBBB"],
        })
        self.assertEqual(cleaned["candidate_sha256"], "a" * 64)
        self.assertEqual(cleaned["secret"], "<redacted>")
        self.assertEqual(cleaned["api_key"], "<redacted>")
        self.assertEqual(cleaned["nested"]["token"], "<redacted>")
        self.assertNotIn("sysec$1$AAAA", cleaned["nested"]["note"])
        self.assertNotIn("sysec$1$BBBB", cleaned["counts"][1])

    def test_drain_requires_zero_active_requests(self):
        bodies = [{"active_requests": 2}, {"active_requests": 1}, None,
                  {"status": "ok", "active_requests": 0}]
        seen = []

        def health():
            seen.append(1)
            return bodies[min(len(seen), len(bodies)) - 1]

        worker = self.deployer(None, run=lambda *a, **k: None, health=health)
        worker.drain()
        self.assertEqual(len(seen), 4)

        worker = self.deployer(None, run=lambda *a, **k: None,
                               health=lambda: {"active_requests": 7})
        with self.assertRaisesRegex(deploy.DeployError, "drain"):
            worker.drain()

    def test_verify_migration_state_requires_hash_schema_and_envelopes(self):
        worker = self.deployer(None, run=lambda *a, **k: None)
        build_migrated_database(self.layout.database)
        state = worker.verify_migration_state()
        self.assertTrue(state["api_key_hash_schema"])
        self.assertEqual(state["channel_secrets_plaintext"], 0)

        build_legacy_database(self.layout.database)
        with self.assertRaises(deploy.DeployError):
            worker.verify_migration_state()

    def test_legacy_plaintext_residue_counts_without_exposing_values(self):
        recovery = self.make_recovery()
        worker = self.deployer(None, run=lambda *a, **k: None)
        build_migrated_database(self.layout.database)
        self.assertEqual(worker.legacy_plaintext_residue({"recovery_directory": str(recovery)}), 0)
        build_migrated_database(self.layout.database, residue=True)
        self.assertEqual(worker.legacy_plaintext_residue({"recovery_directory": str(recovery)}), 1)

    def test_file_contains_spans_read_boundaries(self):
        path = Path(self.tmp.name) / "big.bin"
        needle = b"needle-0123456789"
        with path.open("wb") as stream:
            stream.write(b"a" * (1024 * 1024 - 5))
            stream.write(needle)
            stream.write(b"b" * 4096)
        self.assertTrue(deploy.file_contains(path, needle))
        self.assertFalse(deploy.file_contains(path, b"missing-value"))

    def test_verify_backup_consistency_rejects_modified_copy(self):
        recovery = self.make_recovery()
        worker = self.deployer(None, run=lambda *a, **k: None)
        checks = worker.verify_backup_consistency(
            {"recovery_directory": str(recovery), "database_sha256": "db"})
        self.assertEqual(checks["config.json_sha256"], deploy.sha256_file(self.layout.config))
        (recovery / "config.json").write_text("{}  # tampered")
        with self.assertRaisesRegex(deploy.DeployError, "differs"):
            worker.verify_backup_consistency({"recovery_directory": str(recovery)})

    def test_tool_command_targets_tools_dir(self):
        worker = self.deployer(None, run=lambda *a, **k: None)
        command = worker.tool_command("backup", "--root", "/tmp/x")
        self.assertEqual(command[0], sys.executable)
        self.assertEqual(Path(command[1]), SCRIPTS / "security-batch2-backup.py")
        self.assertEqual(command[2:], ["--root", "/tmp/x"])


class DeployFlowTests(TempLayoutCase):
    def run_release(self, runner, **overrides):
        worker = self.deployer(runner, **overrides)
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            try:
                result = worker.run_release()
            except deploy.DeployError as error:
                return None, output.getvalue(), error
        return result, output.getvalue(), None

    def test_successful_release_sequence_and_artifacts(self):
        recovery = self.make_recovery()
        build_migrated_database(self.layout.database)
        runner = FakeRunner(candidate=self.candidate, backup_json=self.backup_json(recovery))
        before_db = deploy.sha256_file(self.layout.database)
        result, output, error = self.run_release(runner)
        self.assertIsNone(error)
        self.assertEqual(result["result"], "deployed")
        self.assertTrue(result["ingress_restored"])
        self.assertEqual(self.layout.site.read_text(), SITE_TEXT)

        stop = self.command_index(runner, "systemctl stop")
        backup = self.command_index(runner, "security-batch2-backup.py")
        migrate = self.command_index(runner, "secret migrate")
        compare = self.command_index(runner, "security-batch2-compare.py")
        compact = self.command_index(runner, "security-batch2-compact.py")
        start = self.command_index(runner, "systemctl start")
        restart = self.command_index(runner, "systemctl restart")
        self.assertLess(self.command_index(runner, "nginx -t"), self.command_index(runner, "curl"))
        self.assertLess(self.command_index(runner, "curl"), stop)
        self.assertLess(stop, backup)
        self.assertLess(backup, migrate)
        self.assertLess(migrate, compare)
        self.assertLess(compare, compact)
        self.assertLess(compact, start)
        self.assertLess(start, restart)
        self.assertEqual(
            sum(1 for command in runner.commands if "security-batch2-compare.py" in command), 2)
        self.assertEqual(
            sum(1 for command in runner.commands if "security-batch2-inspect.py" in command), 2)
        self.assertEqual(
            sum(1 for command in runner.commands if "systemctl reload nginx" in command), 2)

        migrate_calls = [(env, cwd) for args, env, cwd in runner.calls
                         if " ".join(args).endswith("secret migrate --config " + str(self.layout.config))]
        self.assertEqual(len(migrate_calls), 2)
        self.assertEqual(migrate_calls[0][0]["SWITCHYARD_KEK_FILE"], str(self.layout.master_key))
        # config.json holds a RELATIVE database.path; the service runs with
        # WorkingDirectory=/opt/switchyard, so candidate migration must too — otherwise
        # SQLite looks for data/data.db under the deployer cwd and fails with CANTOPEN(14).
        for env, cwd in migrate_calls:
            self.assertEqual(cwd, str(self.layout.root))
        status_calls = [cwd for args, env, cwd in runner.calls
                        if " ".join(args).endswith("secret status --config " + str(self.layout.config))]
        self.assertEqual(status_calls, [str(self.layout.root)])

        # master key: fresh os.urandom material, 0600 file / 0700 dir, independent root backup.
        key_material = self.layout.master_key.read_bytes()
        self.assertEqual(deploy.decode_master_key(key_material), b"\x07" * 32)
        self.assertEqual(self.layout.master_key.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.layout.keys_dir.stat().st_mode & 0o777, 0o700)
        backups = list(self.layout.key_backup_root.glob("switchyard-master-key-*/master.key"))
        self.assertEqual(len(backups), 1)
        self.assertEqual(backups[0].read_bytes(), key_material)
        self.assertEqual(backups[0].stat().st_mode & 0o777, 0o600)
        self.assertEqual(backups[0].parent.stat().st_mode & 0o777, 0o700)

        # systemd drop-in wires the external KEK file.
        dropin = self.layout.dropin.read_text()
        self.assertIn("[Service]", dropin)
        self.assertIn("Environment=SWITCHYARD_KEK_FILE=" + str(self.layout.master_key), dropin)

        # metadata: no secret material anywhere in the printed summary or deploy.json.
        written = json.loads(next(self.layout.releases.glob("*/deploy.json")).read_text())
        self.assertEqual(written["result"], "deployed")
        self.assertEqual(written["secret_migration"]["channel_keys_encrypted"], 2)
        self.assertTrue(written["secret_migration"]["schema_api_hash"])
        self.assertTrue(written["secret_migration"]["encryption"])
        self.assertEqual(written["legacy_plaintext_residue"], 0)
        self.assertFalse(written["database_restored"])
        self.assertNotIn("sysec$1$", json.dumps(written))
        self.assertNotIn(key_material.strip().decode(), output)
        self.assertNotIn(key_material.strip().decode(), json.dumps(written))
        self.assertEqual(deploy.sha256_file(self.layout.database), before_db)
        self.assertEqual(result["health"]["version"], EXPECTED_VERSION)
        self.assertEqual(result["health_after_restart"]["version"], EXPECTED_VERSION)

    def test_failed_migration_keeps_ingress_closed_and_never_overwrites_database(self):
        recovery = self.make_recovery()
        build_migrated_database(self.layout.database)
        db_before = self.layout.database.read_bytes()
        runner = FakeRunner(
            candidate=self.candidate,
            backup_json=self.backup_json(recovery),
            failures={"secret migrate": deploy.Completed(1, "", "secret migration error: boom")},
        )
        result, output, error = self.run_release(runner)
        self.assertIsNone(result)
        self.assertIsInstance(error, deploy.DeployError)
        self.assertIn("return 503", self.layout.site.read_text())
        self.assertNotEqual(self.layout.site.read_text(), SITE_TEXT)
        self.assertEqual(self.layout.database.read_bytes(), db_before)
        self.assertNotIn("systemctl start", " ".join(runner.commands))
        self.assertGreaterEqual(
            sum(1 for command in runner.commands if command.endswith("systemctl stop switchyard.service")), 1)

        written = json.loads(next(self.layout.releases.glob("*/deploy.json")).read_text())
        self.assertEqual(written["result"], "failed")
        self.assertTrue(written["manual_recovery_required"])
        self.assertFalse(written["database_overwritten"])
        self.assertEqual(written["service_state"], "stopped")
        self.assertIn("503", written["ingress_state"])
        self.assertNotIn("sysec$1$", json.dumps(written))
        self.assertIn("boom", json.dumps(written))

    def test_residue_gate_fails_closed(self):
        recovery = self.make_recovery()
        build_migrated_database(self.layout.database, residue=True)
        runner = FakeRunner(candidate=self.candidate, backup_json=self.backup_json(recovery))
        result, output, error = self.run_release(runner)
        self.assertIsNone(result)
        self.assertIn("legacy credential bytes", str(error))
        self.assertIn("return 503", self.layout.site.read_text())
        self.assertNotIn("systemctl start", " ".join(runner.commands))

    def test_compare_failure_aborts_before_start(self):
        recovery = self.make_recovery()
        build_migrated_database(self.layout.database)
        runner = FakeRunner(
            candidate=self.candidate,
            backup_json=self.backup_json(recovery),
            tool_outputs={"security-batch2-compare.py": json.dumps(
                {"users_rows_preserved": False, "legacy_api_key_fingerprints_match": True})},
        )
        result, output, error = self.run_release(runner)
        self.assertIsNone(result)
        self.assertIn("comparison checks failed", str(error))
        self.assertIn("users_rows_preserved", str(error))
        self.assertIn("return 503", self.layout.site.read_text())
        self.assertNotIn("systemctl start", " ".join(runner.commands))

    def test_missing_tools_fail_before_any_command(self):
        empty = Path(self.tmp.name) / "empty-tools"
        empty.mkdir()
        runner = FakeRunner(candidate=self.candidate, backup_json="{}")
        result, output, error = self.run_release(runner, tools_dir=empty)
        self.assertIsNone(result)
        self.assertIn("tools", str(error))
        self.assertEqual(runner.calls, [])
        self.assertEqual(self.layout.site.read_text(), SITE_TEXT)

    def test_main_requires_explicit_root(self):
        with mock.patch.object(deploy.os, "geteuid", return_value=1000):
            with self.assertRaises(SystemExit):
                deploy.main(["--candidate", "/missing", "--sha256", "0" * 64,
                             "--version", EXPECTED_VERSION, "--tools-dir", str(SCRIPTS)])
        self.assertEqual(self.layout.site.read_text(), SITE_TEXT)

    def test_tool_command_executes_real_helper_on_temp_database(self):
        """--tools-dir wiring against the real read-only helper; temporary database only."""
        build_migrated_database(self.layout.database)
        worker = self.deployer(None, run=deploy.real_runner)
        report = worker.run_inspect("unit")
        self.assertTrue(report["api_key_hash_schema"])
        self.assertFalse(report["api_key_plaintext_column"])


if __name__ == "__main__":
    unittest.main()
