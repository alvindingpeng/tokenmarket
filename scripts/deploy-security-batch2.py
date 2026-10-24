#!/usr/bin/env python3
"""Security batch-2 production release driver for switchyard (standalone, root, authorized host).

Scope of one run: the switchyard.service unit, /opt/switchyard/{bin/switchyard,data/data.db,
data/config.json}, the external master key and the single ingress prod.example.com (nginx only).
Runs locally on the production host as root; no SSH, no embedded credentials, no other hosts.

Sequence: verify candidate checksum + version -> close the prod.example.com nginx entry and confirm 503
-> drain active_requests to 0 -> stop switchyard.service -> consistent recovery point through
security-batch2-backup.py (old DB / bin / config) -> master key at /opt/switchyard/keys/master.key
(dir 0700, file 0600, fresh os.urandom material, never overwritten) -> independent root-private key
backup -> systemd drop-in wiring SWITCHYARD_KEK_FILE -> candidate `secret migrate` (schema migration
incl. one-way API key hashing plus envelope encryption) -> compare / inspect gates -> compact ->
start + health version -> restart + health again -> restore nginx only on success.

Hard safety rules enforced here:
  * the candidate must match --sha256 and report --version before anything is touched;
  * the live database is NEVER overwritten or rolled back by this script; on any failure the
    service is stopped and the prod.example.com entry stays closed (503) for manual recovery;
  * the master key is generated with os.urandom, never overwritten, and never printed;
  * deployment metadata carries paths, counts and booleans only -- never key material,
    credentials or envelope ciphertext.

Unit tests (test_deploy_security_batch2.py) exercise every helper against temporary directories
and mocked command execution; they never touch production.
"""

import argparse
import base64
import hashlib
import json
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import time
import urllib.request
from dataclasses import dataclass
from pathlib import Path

SERVICE = "switchyard.service"
SITE = Path("/etc/nginx/sites-available/switchyard-tt")
LOCATION_MARKER = "    location / {\n        proxy_pass http://127.0.0.1:8088;"
PAUSED_LOCATION = "    location / {\n        return 503;\n        proxy_pass http://127.0.0.1:8088;"
INGRESS_PROBE = (
    "curl", "-sk", "--noproxy", "*", "--max-time", "10",
    "--resolve", "prod.example.com:443:127.0.0.1", "-o", "/dev/null", "-w", "%{http_code}",
    "https://prod.example.com/",
)
TOOLS = {
    "backup": "security-batch2-backup.py",
    "compare": "security-batch2-compare.py",
    "compact": "security-batch2-compact.py",
    "inspect": "security-batch2-inspect.py",
}
SENSITIVE_METADATA_KEYS = {
    "material", "secret", "secrets", "password", "token", "credential", "credentials",
    "api_key", "channel_key", "kek",
}
VERSION_LINE = re.compile(r"^Version:\s*(\S+)\s*$", re.MULTILINE)
MIGRATION_STATS = re.compile(
    r"settings_encrypted=(\d+) channel_keys_encrypted=(\d+) already_encrypted=(\d+) empty_skipped=(\d+)"
)
KEY_ID_LINE = re.compile(r"key ID ([A-Za-z0-9_.-]+)")
STATUS_STATS = re.compile(r"would_encrypt=(\d+) already_encrypted=(\d+) empty_skipped=(\d+)")


class DeployError(RuntimeError):
    """A gate failed; the caller must leave the ingress closed and the service stopped."""


class Completed:
    """Minimal subprocess result so tests can mock command execution."""

    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def real_runner(args, env=None, cwd=None):
    proc = subprocess.run([str(a) for a in args], capture_output=True, text=True, env=env, cwd=cwd)
    return Completed(proc.returncode, proc.stdout, proc.stderr)


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def fetch_health(url):
    with urllib.request.urlopen(url, timeout=5) as response:
        return json.load(response)


def scrub_text(text):
    """Redact envelope ciphertext and inline key material from anything that may be logged."""
    text = re.sub(r"sysec\$1\$[^\s\"']+", "<redacted>", text or "")
    return re.sub(r"\b(?:[A-Za-z0-9+/]{40,}={0,2}|[0-9a-fA-F]{64})\b(?=.*key)", "<redacted>", text)


def scrub_metadata(value):
    """Recursively drop anything that could carry secret material from deployment metadata."""
    if isinstance(value, dict):
        return {
            key: ("<redacted>" if key.lower() in SENSITIVE_METADATA_KEYS else scrub_metadata(item))
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [scrub_metadata(item) for item in value]
    if isinstance(value, str):
        return scrub_text(value)
    return value


STRICT_TOKEN = re.compile(r"\b(?:[A-Za-z0-9+/]{43}=|[A-Za-z0-9+/]{44}|[0-9a-fA-F]{64})\b")


def strict_scrub(text):
    """Log-archive redaction: also drop bare key-material-shaped tokens (43+'=' / 44 base64 / 64 hex)."""
    return STRICT_TOKEN.sub("<redacted>", scrub_text(text))


class SecretFreeLog:
    """Scrubbed execution log archived with the release. Never carries key material,
    credentials or envelope ciphertext: every field passes strict_scrub before storage and the
    archive refuses to be written if anything secret-shaped survived."""

    def __init__(self):
        self.entries = []

    def record(self, step, argv=None, exit_code=None, detail=""):
        self.entries.append({
            "time": time.strftime("%Y-%m-%dT%H:%M:%S"),
            "step": str(step),
            "argv": [strict_scrub(str(a)) for a in (argv or [])],
            "exit_code": exit_code,
            "detail": strict_scrub(str(detail))[:500],
        })

    def render(self):
        lines = []
        for entry in self.entries:
            head = "%s %-30s" % (entry["time"], entry["step"])
            if entry["argv"]:
                head += " argv=" + json.dumps(entry["argv"])
            if entry["exit_code"] is not None:
                head += " exit=%s" % entry["exit_code"]
            lines.append(head)
            if entry["detail"]:
                lines.append("    " + entry["detail"])
        return "\n".join(lines) + "\n"

    def archive(self, release_dir):
        text = strict_scrub(self.render())
        if "sysec$" in text:
            raise DeployError("log archive would contain secret material")
        path = release_dir / "deploy.log"
        path.write_text(text)
        path.chmod(0o600)
        return path


@dataclass(frozen=True)
class Layout:
    """Every path this driver may touch. Tests substitute a temporary layout."""

    root: Path
    site: Path
    dropin_dir: Path
    key_backup_root: Path
    health_url: str

    @classmethod
    def production(cls):
        return cls(
            root=Path("/opt/switchyard"),
            site=SITE,
            dropin_dir=Path("/etc/systemd/system/switchyard.service.d"),
            key_backup_root=Path("/root"),
            health_url="http://127.0.0.1:8088/healthz",
        )

    @property
    def bin(self):
        return self.root / "bin" / "switchyard"

    @property
    def config(self):
        return self.root / "data" / "config.json"

    @property
    def database(self):
        return self.root / "data" / "data.db"

    @property
    def keys_dir(self):
        return self.root / "keys"

    @property
    def master_key(self):
        return self.keys_dir / "master.key"

    @property
    def dropin(self):
        return self.dropin_dir / "security-batch2.conf"

    @property
    def releases(self):
        return self.root / "data" / "releases"


def decode_master_key(raw):
    """Decode KEK text (base64 or hex, 32 random bytes, never all zero). Fail closed."""
    if isinstance(raw, bytes):
        raw = raw.decode("ascii", "strict")
    text = raw.strip()
    if not text:
        raise DeployError("master key file is empty")
    material = None
    if re.fullmatch(r"[0-9a-fA-F]{64}", text):
        material = bytes.fromhex(text)
    else:
        try:
            candidate = base64.b64decode(text + "=" * (-len(text) % 4), validate=False)
        except Exception:
            candidate = None
        if candidate is not None and len(candidate) == 32:
            material = candidate
    if material is None or len(material) != 32:
        raise DeployError("master key file does not carry 32 bytes of key material")
    if material == b"\x00" * 32:
        raise DeployError("master key material must not be all zero")
    return material


class Deployer:
    def __init__(self, *, candidate, sha256, version, tools_dir, layout, run=None,
                 health=None, urandom=None, sleep=time.sleep):
        self.candidate = Path(candidate)
        self.sha256 = sha256
        self.version = version
        self.tools_dir = Path(tools_dir)
        self.layout = layout
        self.run = run or real_runner
        self.health = health or (lambda: fetch_health(layout.health_url))
        self.urandom = urandom or os.urandom
        self.sleep = sleep
        self.log = SecretFreeLog()

    # ---------------------------------------------------------------- commands

    def _exec(self, args, env=None, what="command", cwd=None):
        result = self.run([str(a) for a in args], env=env, cwd=cwd)
        detail = (result.stderr or result.stdout or "").strip()[-500:]
        self.log.record(what, argv=[str(a) for a in args], exit_code=result.returncode,
                        detail=detail if result.returncode != 0 else "")
        if result.returncode != 0:
            raise DeployError("%s failed (exit %s): %s" % (what, result.returncode, scrub_text(detail)))
        return (result.stdout or "") + "\n" + (result.stderr or "")

    def tool_command(self, name, *args):
        script = self.tools_dir / TOOLS[name]
        return [sys.executable, str(script), *[str(a) for a in args]]

    def verify_tools(self):
        missing = [TOOLS[name] for name in TOOLS if not (self.tools_dir / TOOLS[name]).is_file()]
        if missing:
            raise DeployError("required deployment tools missing from --tools-dir: " + ", ".join(sorted(missing)))

    # ------------------------------------------------------------- candidate

    def binary_version(self, path):
        output = self._exec([path, "version"], what="candidate version probe")
        match = VERSION_LINE.search(output)
        if not match:
            raise DeployError("candidate version output did not carry a Version line")
        return match.group(1)

    def verify_candidate(self):
        if not self.candidate.is_file():
            raise DeployError("candidate binary not found: %s" % self.candidate)
        actual = sha256_file(self.candidate)
        if actual != self.sha256:
            raise DeployError("candidate checksum mismatch")
        reported = self.binary_version(self.candidate)
        if reported != self.version:
            raise DeployError("candidate version mismatch: reported %s, expected %s" % (reported, self.version))
        return reported

    def install_candidate(self):
        """Stage, checksum and atomically replace the service binary. Never touches the database."""
        stage = self.layout.bin.with_name("switchyard.security-batch2-stage")
        shutil.copyfile(self.candidate, stage)
        try:
            current = self.layout.bin.stat()
            os.chmod(stage, current.st_mode & 0o777)
            try:
                os.chown(stage, current.st_uid, current.st_gid)
            except OSError:
                pass  # already owned by the deploying root user
        except FileNotFoundError:
            os.chmod(stage, 0o750)
        if sha256_file(stage) != self.sha256:
            stage.unlink(missing_ok=True)
            raise DeployError("staged binary checksum mismatch")
        os.replace(stage, self.layout.bin)
        if sha256_file(self.layout.bin) != self.sha256:
            raise DeployError("installed binary checksum mismatch")
        return self.sha256

    # ----------------------------------------------------------------- nginx

    def pause_ingress(self, release_dir):
        """Close ONLY the prod.example.com nginx entry (return 503); keep the local backend reachable."""
        original = self.layout.site.read_text()
        if original.count(LOCATION_MARKER) != 1:
            raise DeployError("unexpected target nginx configuration")
        backup = release_dir / "nginx.original"
        backup.write_text(original)
        backup.chmod(0o600)
        paused = original.replace(LOCATION_MARKER, PAUSED_LOCATION)
        self.layout.site.write_text(paused)
        try:
            self._exec(["nginx", "-t"], what="nginx config test")
            self._exec(["systemctl", "reload", "nginx"], what="nginx reload")
        except Exception:
            self.layout.site.write_text(original)
            raise
        return original

    def probe_ingress(self):
        return self._exec(list(INGRESS_PROBE), what="ingress probe").strip()

    def confirm_entry_closed(self):
        code = ""
        for _ in range(20):
            code = self.probe_ingress()
            if code == "503":
                return
            self.sleep(0.25)
        raise DeployError("maintenance ingress (503) was not confirmed: " + code)

    def confirm_entry_open(self):
        code = ""
        for _ in range(20):
            code = self.probe_ingress()
            if code == "200":
                return
            self.sleep(0.25)
        raise DeployError("ingress still closed (503) after restore")

    def restore_ingress(self, original):
        self.layout.site.write_text(original)
        self._exec(["nginx", "-t"], what="nginx config test")
        self._exec(["systemctl", "reload", "nginx"], what="nginx reload")
        self.confirm_entry_open()

    # --------------------------------------------------------------- service

    def _systemctl(self, *args, what):
        return self._exec(["systemctl", *args], what=what)

    def service_main_pid(self):
        return self._exec(["systemctl", "show", SERVICE, "-p", "MainPID", "--value"],
                          what="service state probe").strip()

    def service_active(self):
        result = self.run(["systemctl", "is-active", SERVICE])
        return result.returncode == 0 and result.stdout.strip() == "active"

    def stop_service(self):
        self._systemctl("stop", SERVICE, what="service stop")
        if self.service_main_pid() != "0":
            raise DeployError("service still owns a running process")

    def start_service(self):
        self._systemctl("start", SERVICE, what="service start")

    def restart_service(self):
        self._systemctl("restart", SERVICE, what="service restart")

    def quiet_stop(self):
        try:
            self.run(["systemctl", "stop", SERVICE])
        except Exception:
            pass

    # ---------------------------------------------------------------- health

    def drain(self):
        for _ in range(120):
            try:
                body = self.health()
            except Exception:
                body = None
            if body is not None and body.get("active_requests") == 0:
                return
            self.sleep(1)
        raise DeployError("active requests did not drain to zero; aborting before install")

    def await_health(self):
        for _ in range(30):
            try:
                body = self.health()
            except Exception:
                body = None
            if (body is not None and body.get("status") == "ok" and body.get("db") == "ok"
                    and body.get("version") == self.version and self.service_active()):
                return {"status": body.get("status"), "db": body.get("db"),
                        "version": body.get("version"), "active_requests": body.get("active_requests")}
            self.sleep(1)
        raise DeployError("service health did not report version %s within 30 attempts" % self.version)

    # --------------------------------------------------------------- backup

    def run_backup_tool(self):
        output = self._exec(self.tool_command("backup", "--root", self.layout.root),
                            what="recovery point (security-batch2-backup.py)")
        try:
            data = json.loads(output)
        except ValueError:
            raise DeployError("recovery point tool did not report JSON")
        if data.get("result") != "backup-created":
            raise DeployError("recovery point was not created")
        recovery = Path(data.get("recovery_directory", ""))
        if not (recovery / "pre-upgrade.db").is_file():
            raise DeployError("recovery point is missing pre-upgrade.db")
        self.log.record("recovery point", detail="recovery_directory=" + str(recovery))
        return data

    def verify_backup_consistency(self, recovery):
        """The recovery point must be the old DB/bin/config, byte-identical for bin and config."""
        directory = Path(recovery["recovery_directory"])
        checks = {}
        for name, source in (("switchyard.previous", self.layout.bin), ("config.json", self.layout.config)):
            copy = directory / name
            if not copy.is_file():
                raise DeployError("recovery point is missing " + name)
            checks[name + "_sha256"] = sha256_file(copy)
            if checks[name + "_sha256"] != sha256_file(source):
                raise DeployError("recovery point copy of " + name + " differs from the live file")
        checks["pre-upgrade.db_sha256"] = recovery.get("database_sha256")
        self.log.record("recovery point consistency", detail="bin/config copies match live files")
        return checks

    # ------------------------------------------------------------- master key

    def ensure_master_key(self):
        """Create /opt/switchyard/keys/master.key from os.urandom (dir 0700, file 0600).

        An existing key is validated and kept untouched: this driver never overwrites key material.
        """
        keys_dir = self.layout.keys_dir
        keys_dir.mkdir(parents=True, exist_ok=True)
        os.chmod(keys_dir, 0o700)
        key_path = self.layout.master_key
        if key_path.exists():
            decode_master_key(key_path.read_bytes().decode("ascii", "strict"))
            os.chmod(key_path, 0o600)
            state = "existing"
        else:
            material = self.urandom(32)
            if len(material) != 32:
                raise DeployError("os.urandom did not return 32 bytes")
            payload = base64.b64encode(material) + b"\n"
            try:
                fd = os.open(str(key_path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            except FileExistsError:
                raise DeployError("master key appeared concurrently; refusing to overwrite")
            with os.fdopen(fd, "wb") as stream:
                stream.write(payload)
            decode_master_key(payload.decode("ascii"))
            state = "created"
        if key_path.stat().st_mode & 0o777 != 0o600:
            raise DeployError("master key file permissions are not 0600")
        if keys_dir.stat().st_mode & 0o777 != 0o700:
            raise DeployError("master key directory permissions are not 0700")
        return {"path": str(key_path), "state": state, "dir_mode": "0700", "file_mode": "0600",
                "owner": "root", "material_recorded": False}

    def backup_master_key(self):
        """Copy the KEK to a root-private location, independent of any data backup. No printing."""
        stamp = time.strftime("%Y%m%d-%H%M%S")
        target_dir = self.layout.key_backup_root / ("switchyard-master-key-" + stamp)
        target_dir.mkdir(parents=True, exist_ok=False)
        os.chmod(target_dir, 0o700)
        payload = self.layout.master_key.read_bytes()
        decode_master_key(payload.decode("ascii", "strict"))
        target = target_dir / "master.key"
        fd = os.open(str(target), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(payload)
        if target.stat().st_mode & 0o777 != 0o600:
            raise DeployError("key backup file permissions are not 0600")
        return {"path": str(target), "dir_mode": "0700", "file_mode": "0600",
                "independent_of_data_backup": True, "material_recorded": False}

    def write_dropin(self):
        """systemd drop-in: point the unit at the external KEK file (never in config.json)."""
        self.layout.dropin_dir.mkdir(parents=True, exist_ok=True)
        content = (
            "# Managed by deploy-security-batch2.py; do not edit by hand.\n"
            "[Service]\n"
            "Environment=SWITCHYARD_KEK_FILE=%s\n" % self.layout.master_key
        )
        self.layout.dropin.write_text(content)
        self.layout.dropin.chmod(0o600)
        self._systemctl("daemon-reload", what="systemd daemon-reload")
        return self.layout.dropin

    # -------------------------------------------------- candidate secret migrate

    def secret_migrate(self):
        """Run the candidate's `secret migrate`: schema migration (incl. one-way API key hashing)
        plus envelope encryption of settings/channel_keys. Records counts only."""
        env = dict(os.environ, SWITCHYARD_KEK_FILE=str(self.layout.master_key))
        # config.json uses a relative database.path (data/data.db); systemd runs the service
        # with WorkingDirectory=/opt/switchyard, so every candidate invocation must too.
        output = self._exec(self.tool_or_binary_command("secret", "migrate", "--config", self.layout.config),
                            env=env, what="candidate secret migrate", cwd=str(self.layout.root))
        match = MIGRATION_STATS.search(output)
        if not match:
            raise DeployError("candidate secret migrate did not report migration statistics")
        key_id = KEY_ID_LINE.search(output)
        stats = {
            "settings_encrypted": int(match.group(1)),
            "channel_keys_encrypted": int(match.group(2)),
            "already_encrypted": int(match.group(3)),
            "empty_skipped": int(match.group(4)),
            "key_id": key_id.group(1) if key_id else "",
            "schema_api_hash": True,
            "encryption": True,
        }
        self.log.record("secret migrate", detail=json.dumps(stats, sort_keys=True))
        return stats

    def tool_or_binary_command(self, *args):
        return [str(self.candidate), *[str(a) for a in args]]

    # ------------------------------------------------------ migration gates

    def verify_migration_state(self):
        """Read-only schema/encryption gate on the live database. Counts and booleans only."""
        connection = sqlite3.connect(self.layout.database.resolve().as_uri() + "?mode=ro", uri=True)
        try:
            columns = {row[1] for row in connection.execute("PRAGMA table_info(api_keys)")}
            result = {
                "api_key_plaintext_column_removed": "api_key" not in columns,
                "api_key_hash_schema": "key_hash" in columns,
                "invalid_api_key_hashes": 0,
            }
            if "key_hash" in columns:
                result["invalid_api_key_hashes"] = connection.execute(
                    "SELECT COUNT(*) FROM api_keys WHERE key_hash IS NULL OR length(key_hash) != 64 "
                    "OR key_hash GLOB '*[^0-9a-f]*'"
                ).fetchone()[0]
            result["channel_secrets_plaintext"] = connection.execute(
                "SELECT COUNT(*) FROM channel_keys WHERE key IS NOT NULL AND key != '' "
                "AND key NOT LIKE 'sysec$1$%'"
            ).fetchone()[0]
            result["channel_secrets_enveloped"] = connection.execute(
                "SELECT COUNT(*) FROM channel_keys WHERE key LIKE 'sysec$1$%'"
            ).fetchone()[0]
        finally:
            connection.close()
        if (not result["api_key_plaintext_column_removed"] or not result["api_key_hash_schema"]
                or result["invalid_api_key_hashes"] or result["channel_secrets_plaintext"]):
            raise DeployError("migration state gate failed: " + json.dumps(result, sort_keys=True))
        self.log.record("migration state", detail=json.dumps(result, sort_keys=True))
        return result

    def run_compare(self, recovery):
        before = Path(recovery["recovery_directory"]) / "pre-upgrade.db"
        result = self.run(self.tool_command("compare", before, self.layout.database))
        if result.returncode != 0:
            raise DeployError("comparison failed: " + scrub_text((result.stdout or result.stderr or "")[-500:]))
        try:
            checks = json.loads(result.stdout)
        except ValueError:
            raise DeployError("comparison did not report JSON")
        failed = sorted(name for name, ok in checks.items() if not ok)
        if failed:
            raise DeployError("comparison checks failed: " + ", ".join(failed))
        self.log.record("compare", detail=json.dumps(checks, sort_keys=True))
        return checks

    def run_inspect(self, label):
        result = self.run(self.tool_command("inspect", self.layout.database, "--require-hashed-api-keys"))
        if result.returncode != 0:
            raise DeployError("inspection (" + label + ") failed: " + scrub_text((result.stdout or result.stderr or "")[-500:]))
        try:
            report = json.loads(result.stdout)
        except ValueError:
            raise DeployError("inspection did not report JSON")
        if (report.get("integrity") != "ok" or report.get("foreign_key_violations")
                or report.get("negative_frozen") or report.get("api_key_plaintext_column")
                or not report.get("api_key_hash_schema") or report.get("invalid_api_key_hashes")):
            raise DeployError("inspection (" + label + ") gate failed: " + json.dumps(report, sort_keys=True))
        self.log.record("inspect " + label, detail=json.dumps(report, sort_keys=True))
        return report

    def run_compact(self):
        result = self.run(self.tool_command("compact", self.layout.database, "--service-stopped"))
        if result.returncode != 0:
            raise DeployError("compaction failed: " + scrub_text((result.stdout or result.stderr or "")[-500:]))
        try:
            report = json.loads(result.stdout)
        except ValueError:
            raise DeployError("compaction did not report JSON")
        if not report.get("compacted"):
            raise DeployError("compaction did not complete")
        self.log.record("compact", detail=json.dumps(report, sort_keys=True))
        return report

    def legacy_plaintext_residue(self, recovery):
        """Count legacy credential strings from the recovery point that still occur in the live
        database/WAL bytes. Returns a count only -- never any credential value."""
        before = Path(recovery["recovery_directory"]) / "pre-upgrade.db"
        needles = set()
        connection = sqlite3.connect(before.resolve().as_uri() + "?mode=ro", uri=True)
        try:
            for table, column in (("api_keys", "api_key"), ("channel_keys", "key")):
                exists = connection.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)
                ).fetchone()
                if not exists:
                    continue
                columns = {row[1] for row in connection.execute("PRAGMA table_info(" + table + ")")}
                if column not in columns:
                    continue
                for (value,) in connection.execute("SELECT " + column + " FROM " + table):
                    if isinstance(value, str) and len(value) >= 8 and not value.startswith("sysec$1$"):
                        needles.add(value.encode())
        finally:
            connection.close()
        residue = 0
        for needle in needles:
            for suffix in ("", "-wal", "-shm"):
                path = Path(str(self.layout.database) + suffix)
                if path.is_file() and file_contains(path, needle):
                    residue += 1
                    break
        self.log.record("legacy plaintext residue", detail="remaining credential strings=%d" % residue)
        return residue

    # ---------------------------------------------- post-migration repeat verification

    def verify_migration_repeat(self):
        """Repeat verification right after the migration run.

        `secret migrate` is documented idempotent (already-enveloped rows are only re-opened,
        zero writes) and re-opens EVERY envelope with the current master key, so a repeat run
        reporting zero newly encrypted rows proves both idempotency and decryptability. The
        `secret status` dry run must then agree that nothing is left to encrypt (no writes)."""
        env = dict(os.environ, SWITCHYARD_KEK_FILE=str(self.layout.master_key))
        output = self._exec(self.tool_or_binary_command("secret", "migrate", "--config", self.layout.config),
                            env=env, what="repeat secret migrate", cwd=str(self.layout.root))
        match = MIGRATION_STATS.search(output)
        if not match:
            raise DeployError("repeat secret migrate did not report migration statistics")
        stats = {
            "settings_encrypted": int(match.group(1)),
            "channel_keys_encrypted": int(match.group(2)),
            "already_encrypted": int(match.group(3)),
            "empty_skipped": int(match.group(4)),
        }
        if stats["settings_encrypted"] or stats["channel_keys_encrypted"]:
            raise DeployError("repeat secret migrate was not idempotent: " + json.dumps(stats, sort_keys=True))
        status = self._exec(self.tool_or_binary_command("secret", "status", "--config", self.layout.config),
                            env=env, what="secret status dry run", cwd=str(self.layout.root))
        smatch = STATUS_STATS.search(status)
        if not smatch:
            raise DeployError("secret status did not report dry-run statistics")
        stats["would_encrypt_after"] = int(smatch.group(1))
        if stats["would_encrypt_after"]:
            raise DeployError("secret status still reports %d row(s) needing encryption"
                              % stats["would_encrypt_after"])
        stats["decrypt_verified"] = True
        self.log.record("migration repeat verification", detail=json.dumps(stats, sort_keys=True))
        return stats

    def repeat_verification(self, recovery):
        """Read-only repeat verification after the restart: schema/API-hash/encryption state plus
        the compare and inspect gates on the running service's database."""
        report = {
            "migration_state": self.verify_migration_state(),
            "inspect": self.run_inspect("post-restart"),
            "compare": self.run_compare(recovery),
        }
        self.log.record("post-restart repeat verification", detail="all read-only gates passed")
        return report

    # ------------------------------------------------------------- metadata

    def write_metadata(self, release_dir, metadata):
        cleaned = scrub_metadata(metadata)
        payload = json.dumps(cleaned, ensure_ascii=False, indent=2, sort_keys=True)
        (release_dir / "deploy.json").write_text(payload + "\n")
        (release_dir / "deploy.json").chmod(0o600)
        return cleaned

    # ----------------------------------------------------------------- run

    def run_release(self):
        os.umask(0o077)
        self.verify_tools()
        candidate_version = self.verify_candidate()
        release_dir = self.layout.releases / (time.strftime("%Y%m%d-%H%M%S") + "-security-batch2-deploy")
        release_dir.mkdir(parents=True, mode=0o700, exist_ok=False)
        metadata = {
            "script": "deploy-security-batch2.py",
            "service": SERVICE,
            "ingress": "prod.example.com (nginx only)",
            "ingress_site": str(self.layout.site),
            "database": str(self.layout.database),
            "binary": str(self.layout.bin),
            "config": str(self.layout.config),
            "candidate_sha256": self.sha256,
            "candidate_version": candidate_version,
            "expected_version": self.version,
            "database_restored": False,
        }
        original = None
        paused = False
        deployed = False
        error = None
        try:
            original = self.pause_ingress(release_dir)
            paused = True
            self.confirm_entry_closed()
            self.drain()
            self.stop_service()
            metadata["previous_version"] = self.binary_version(self.layout.bin)
            recovery = self.run_backup_tool()
            metadata["recovery_directory"] = recovery.get("recovery_directory")
            metadata["recovery"] = self.verify_backup_consistency(recovery)
            metadata["master_key_file"] = self.ensure_master_key()
            metadata["master_key_backup"] = self.backup_master_key()
            metadata["dropin"] = str(self.write_dropin())
            metadata["installed_binary_sha256"] = self.install_candidate()
            metadata["secret_migration"] = self.secret_migrate()
            metadata["migration_repeat"] = self.verify_migration_repeat()
            metadata["migration_state"] = self.verify_migration_state()
            metadata["compare"] = self.run_compare(recovery)
            metadata["inspect"] = self.run_inspect("pre-compact")
            metadata["compact"] = self.run_compact()
            metadata["legacy_plaintext_residue"] = self.legacy_plaintext_residue(recovery)
            if metadata["legacy_plaintext_residue"]:
                raise DeployError("legacy credential bytes remain in the active database files")
            metadata["compare_final"] = self.run_compare(recovery)
            metadata["inspect_final"] = self.run_inspect("post-compact")
            self.start_service()
            metadata["health"] = self.await_health()
            self.restart_service()
            metadata["health_after_restart"] = self.await_health()
            deployed = True
        except Exception as caught:
            error = caught
            metadata["error"] = str(caught)
        if deployed:
            try:
                self.restore_ingress(original)
                metadata["ingress_restored"] = True
                metadata["result"] = "deployed"
            except Exception as caught:
                metadata["ingress_restored"] = False
                metadata["result"] = "deployed-ingress-manual-recovery"
                metadata["error"] = "ingress restore failed after deployment: " + str(caught)
                error = DeployError(metadata["error"])
        else:
            # Fail closed: keep the prod.example.com entry at 503 and the service stopped. When the pause
            # itself never took effect the entry is still open, so the service is left untouched
            # instead of being turned into a self-inflicted outage.
            metadata["ingress_restored"] = False
            metadata["result"] = "failed"
            if paused:
                self.quiet_stop()
                metadata["service_state"] = "stopped"
                metadata["ingress_state"] = "left closed (503) for manual recovery"
            else:
                metadata["service_state"] = "unchanged"
                metadata["ingress_state"] = "unchanged"
            metadata["manual_recovery_required"] = True
            metadata["database_overwritten"] = False
            metadata["recovery_howto"] = (
                "recover manually from the recovery point together with the master key backup; "
                "this script never overwrites the live database"
            )
        cleaned = self.write_metadata(release_dir, metadata)
        print(json.dumps({"release_directory": str(release_dir), **cleaned}, ensure_ascii=False, indent=2, sort_keys=True))
        if error is not None:
            raise error
        return cleaned


def file_contains(path, needle):
    overlap = b""
    chunk_size = 1024 * 1024
    with open(path, "rb") as stream:
        while True:
            data = stream.read(chunk_size)
            if not data:
                return False
            buffer = overlap + data
            if needle in buffer:
                return True
            overlap = buffer[-(len(needle) - 1):] if len(needle) > 1 else b""


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--candidate", type=Path, required=True, help="approved candidate binary")
    parser.add_argument("--sha256", required=True, help="approved sha256 of the candidate")
    parser.add_argument("--version", required=True, help="version the candidate and /healthz must report")
    parser.add_argument("--tools-dir", type=Path, default=Path(__file__).resolve().parent,
                        help="directory holding security-batch2-{backup,compare,compact,inspect}.py")
    args = parser.parse_args(argv)
    if os.geteuid() != 0:
        raise SystemExit("requires explicitly authorized root execution")
    deployer = Deployer(
        candidate=args.candidate,
        sha256=args.sha256,
        version=args.version,
        tools_dir=args.tools_dir,
        layout=Layout.production(),
    )
    deployer.run_release()


if __name__ == "__main__":
    main()
