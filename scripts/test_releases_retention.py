"""Isolated checks for the releases retention tool; never touch production."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "retention", Path(__file__).with_name("releases-retention.py"))
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)


class RetentionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.releases = self.root / "data" / "releases"
        self.releases.mkdir(parents=True)

    def make_deploy(self, name, result="deployed", manual=False, payload=b"record"):
        """Create a completed (or deliberately incomplete) deploy record."""
        path = self.releases / name
        path.mkdir()
        (path / "payload.bin").write_bytes(payload)
        if result is not None:
            data = {"result": result}
            if manual:
                data["manual_recovery_required"] = True
            (path / "deploy.json").write_text(json.dumps(data), encoding="utf-8")
        # Distinct mtimes: the plan sorts by mtime, and equal mtimes would make the
        # "keep the newest N" assertions depend on filesystem clock resolution.
        stamp = len(list(self.releases.iterdir())) * 10
        os.utime(path, (stamp, stamp))
        return path

    def make_marked(self, name, marker):
        path = self.releases / name
        path.mkdir()
        (path / marker).write_text("{}", encoding="utf-8")
        return path

    def test_only_deploy_records_are_candidates(self):
        # Evidence categories must never appear in the removable set, at any age.
        self.make_marked("20260101-000000-security-batch2", "backup.json")
        self.make_marked("20260101-000000-financial", "release.json")
        for index in range(8):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        groups = retention.scan(self.releases)
        removable, _, _ = retention.plan(groups, keep=1)

        names = {p.name for p in removable}
        self.assertEqual([p.name for p in groups["recovery"]],
                         ["20260101-000000-security-batch2"])
        self.assertEqual([p.name for p in groups["financial"]],
                         ["20260101-000000-financial"])
        self.assertNotIn("20260101-000000-security-batch2", names)
        self.assertNotIn("20260101-000000-financial", names)

    def test_financial_and_recovery_survive_full_run(self):
        # End-to-end: the prune really runs, and the evidence is still on disk.
        self.make_marked("20250101-000000-security-batch2", "backup.json")
        self.make_marked("20250101-000000-financial", "release.json")
        for index in range(7):
            self.make_deploy("2026030%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "2"])

        self.assertTrue((self.releases / "20250101-000000-security-batch2").is_dir())
        self.assertTrue((self.releases / "20250101-000000-financial").is_dir())
        remaining = [p.name for p in self.releases.iterdir()
                     if p.is_dir() and p.name.endswith("-deploy")]
        self.assertEqual(len(remaining), 2)

    def test_incomplete_release_is_never_removed(self):
        # A failed deploy leaves the ingress closed and the service stopped; that record
        # is the recovery evidence and must outlive every routine rotation.
        self.make_deploy("20260101-000000-security-batch2-deploy", result="failed", manual=True)
        for index in range(6):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1"])

        self.assertTrue((self.releases / "20260101-000000-security-batch2-deploy").is_dir())

    def test_ingress_manual_recovery_variant_is_kept(self):
        # The service came up but the public entry is still closed (503). The deploy
        # script records "deployed-ingress-manual-recovery"; a prefix match would treat
        # it as success and delete the only record of the unfinished intervention.
        self.make_deploy("20260101-000000-security-batch2-deploy",
                         result="deployed-ingress-manual-recovery")
        for index in range(5):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1"])

        self.assertTrue((self.releases / "20260101-000000-security-batch2-deploy").is_dir())

    def test_manual_flag_alone_blocks_removal(self):
        # result says "deployed" but the record is flagged for manual recovery.
        self.make_deploy("20260101-000000-security-batch2-deploy",
                         result="deployed", manual=True)
        for index in range(5):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1"])

        self.assertTrue((self.releases / "20260101-000000-security-batch2-deploy").is_dir())

    def test_missing_or_unreadable_metadata_is_kept(self):
        # We never delete what we cannot prove to be a completed deploy.
        broken = self.releases / "20260101-000000-security-batch2-deploy"
        broken.mkdir()
        (broken / "deploy.json").write_text("{not json", encoding="utf-8")

        absent = self.releases / "20260102-000000-security-batch2-deploy"
        absent.mkdir()

        for index in range(5):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1"])

        self.assertTrue(broken.is_dir())
        self.assertTrue(absent.is_dir())

    def test_unknown_directories_are_reported_not_removed(self):
        # A hand-made copy or a future script's layout must not be guessed at.
        mine = self.releases / "manual-copy"
        mine.mkdir()
        (mine / "data.db").write_bytes(b"important")
        for index in range(4):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1"])

        self.assertTrue(mine.is_dir())

    def test_dry_run_removes_nothing(self):
        for index in range(5):
            self.make_deploy("2026020%d-000000-security-batch2-deploy" % index)

        retention.main(["--root", str(self.root), "--keep", "1", "--dry-run"])

        self.assertEqual(len([p for p in self.releases.iterdir() if p.is_dir()]), 5)

    def test_lock_blocks_concurrent_pruning(self):
        # Two deploys at once must not prune each other's in-flight directory.
        (self.releases / retention.LOCK_NAME).write_text("", encoding="utf-8")
        self.make_deploy("20260101-000000-security-batch2-deploy")

        with self.assertRaises(SystemExit):
            retention.main(["--root", str(self.root), "--keep", "1"])

    def test_keep_floor_is_enforced(self):
        self.make_deploy("20260101-000000-security-batch2-deploy")
        with self.assertRaises(SystemExit):
            retention.main(["--root", str(self.root), "--keep", "0"])

    def test_classify_prefers_deploy_suffix(self):
        # "-security-batch2" is a prefix of "-security-batch2-deploy"; getting this
        # backwards would either delete the pre-migration recovery point or make every
        # deploy record immortal.
        self.assertEqual(retention.classify("x-security-batch2-deploy"), "deploy")
        self.assertEqual(retention.classify("x-security-batch2"), "recovery")
        self.assertEqual(retention.classify("x-financial"), "financial")
        self.assertEqual(retention.classify("x"), "unknown")


if __name__ == "__main__":
    unittest.main()
