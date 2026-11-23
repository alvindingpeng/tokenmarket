"""Test deployment failure paths without starting services or hitting the network."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "deploy" / "swap_deploy.sh"


class SwapDeployTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        self.bin_dir = root / "bin"
        self.bin_dir.mkdir()
        self.stubs = root / "stubs"
        self.stubs.mkdir()
        self.calls = root / "calls"
        self.env = {
            **os.environ,
            "PATH": f"{self.stubs}{os.pathsep}{os.environ['PATH']}",
            "SWITCHYARD_BIN_DIR": str(self.bin_dir),
            "SWITCHYARD_DB_PATH": str(root / "absent.db"),
            "SWITCHYARD_HEALTH_RETRIES": "1",
            "SYSTEMCTL_CALLS": str(self.calls),
        }
        self.executable(self.bin_dir / "switchyard", "echo 'Version: v1.16.0'\n")
        self.executable(self.bin_dir / "switchyard.new", "echo 'Version: v1.16.1'\n")
        self.executable(self.stubs / "systemctl", """
echo "$1" >> "$SYSTEMCTL_CALLS"
if [ "$1" = is-active ]; then echo active; fi
""")
        self.executable(self.stubs / "curl", """
while [ "$1" != -o ]; do shift; done
shift
version=$("$SWITCHYARD_BIN_DIR/switchyard" version | cut -d' ' -f2)
if [ "$version" = "$FAIL_HEALTH_VERSION" ]; then printf 503; exit 0; fi
printf '{"status":"ok","db":"ok","version":"%s"}' "$version" > "$1"
printf 200
""")

    def executable(self, path, body):
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(0o755)

    def deploy(self, version="v1.16.1"):
        return subprocess.run(
            ["bash", str(SCRIPT), version], env=self.env,
            capture_output=True, text=True, timeout=10,
        )

    def active_version(self):
        return subprocess.check_output(
            [str(self.bin_dir / "switchyard"), "version"], text=True
        ).strip()

    def test_bad_candidate_keeps_service_running(self):
        result = self.deploy("v1.17.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.calls.exists())
        self.assertFalse((self.bin_dir / ".swap-deploy.lock").exists())

    def test_backup_copy_failure_keeps_service_running(self):
        self.executable(self.stubs / "cp", "exit 1\n")
        result = self.deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.calls.exists())
        self.assertEqual(self.active_version(), "Version: v1.16.0")
        self.assertFalse(list(self.bin_dir.glob(".switchyard-backup.*")))

    def test_replace_failure_recovers_old_service(self):
        self.executable(self.stubs / "mv", """
case "$2" in */switchyard.new) exit 1;; esac
exec /bin/mv "$@"
""")
        result = self.deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("DEPLOY_ROLLED_BACK reason=replace_failed", result.stdout)
        self.assertEqual(self.calls.read_text().splitlines(), ["stop", "stop", "start"])
        self.assertEqual(self.active_version(), "Version: v1.16.0")

    def test_health_failure_recovers_and_checks_old_version(self):
        self.env["FAIL_HEALTH_VERSION"] = "v1.16.1"
        result = self.deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("DEPLOY_ROLLED_BACK reason=health_check_failed", result.stdout)
        self.assertEqual(self.active_version(), "Version: v1.16.0")

    def test_success_keeps_verified_rollback_copy(self):
        result = self.deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("DEPLOY_OK version=v1.16.1", result.stdout)
        self.assertEqual(self.active_version(), "Version: v1.16.1")
        self.assertTrue((self.bin_dir / "switchyard.prev-v1.16.0").exists())


if __name__ == "__main__":
    unittest.main()
