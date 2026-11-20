#!/usr/bin/env python3
"""Prune old deployment records in /opt/switchyard/data/releases; never touch evidence.

WHY THIS EXISTS
    data/releases grows with the number of RELEASES, not with business volume. Each
    directory holds a full database copy plus the old binary --- tens of megabytes
    apiece. Nothing in the repository ever deleted one, so this directory only ever
    grew. Unlike data/backups (keep=3, rotated by the Go backup task) and bin/*.prev-*
    (keep=10, rotated by swap_deploy.sh), releases had no retention at all.

WHY THIS IS NOT A PLAIN "ls | tail -n +N | rm"
    Three different scripts write here, and the directories are NOT of equal value.
    The marker file identifies the producer, and each producer documents what must be
    kept:

      *-financial          release.json   financial evidence. docs/financial-release-handoff.md
                                          says the recovery point must be kept exempt from
                                          routine rotation. NEVER pruned by this tool.
      *-security-batch2    backup.json    pre-migration recovery point (old DB + old binary +
                                          config). This is the only way back to the
                                          pre-migration schema, so it is kept forever too.
      *-security-batch2-deploy  deploy.json   deployment process record (nginx original,
                                          deploy.log). Purely operational. Rotated here.

    A failed deploy is a different matter: deploy-security-batch2.py leaves the entry
    closed at 503 and the service stopped when it cannot complete, and records
    manual_recovery_required=true in deploy.json. That directory IS the recovery
    evidence for an unfinished intervention, so it is exempt regardless of count.

FAIL-CLOSED RULES THAT MATTER MORE THAN THE COUNT
    1. Only directories whose name ends in the exact "-security-batch2-deploy" suffix
       are candidates. The two evidence categories are excluded by SUFFIX, not by
       inspecting contents we might misread.
    2. A candidate is pruned only when it carries a deploy.json we could parse AND
       whose result starts with "deployed" (i.e. the release actually completed). No
       deploy.json, unreadable JSON, or any other result => keep. An unknown directory
       is never deleted; deleting it is unrecoverable, keeping it costs only disk.
    3. Directories that are not ours (manual copies, future script names) are reported
       but never removed.
    4. Nothing is deleted while a deploy holds the lock. Two deploys running at once
       would otherwise be able to prune each other's in-flight directory.

The retained count is measured per category, so a burst of routine deploys can never
push an in-flight failure record out of the window by sheer count.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import sys

SUFFIX_DEPLOY = "-security-batch2-deploy"
SUFFIX_RECOVERY = "-security-batch2"
SUFFIX_FINANCIAL = "-financial"

LOCK_NAME = ".releases.lock"
DEFAULT_ROOT = Path("/opt/switchyard")
DEFAULT_KEEP = 5


def classify(name):
    """Return the category of a release directory name.

    These suffixes happen to be mutually exclusive (a"-security-batch2-deploy" name does
    not end with "-security-batch2"), so the order below is not load-bearing today. It is
    still written longest-first: if a future script adds a shorter overlapping suffix, the
    natural append-at-the-end habit would silently reclassify evidence as rotatable.
    """
    if name.endswith(SUFFIX_DEPLOY):
        return "deploy"
    if name.endswith(SUFFIX_RECOVERY):
        return "recovery"
    if name.endswith(SUFFIX_FINANCIAL):
        return "financial"
    return "unknown"


def completed_release(path):
    """True only when deploy.json says this deployment finished cleanly.

    Absent or unreadable metadata returns False on purpose: an incomplete record may be
    the only evidence for a half-applied change, and it is not ours to discard.

    The comparison is EXACT, not a prefix match. deploy-security-batch2.py writes
    "deployed-ingress-manual-recovery" when the service came up but restoring the
    nginx entry failed --- the watchdog would call that a successful deploy, yet the
    public entry is still closed and a human must intervene. A startswith("deployed")
    test would happily delete that recovery evidence.
    """
    marker = path / "deploy.json"
    try:
        data = json.loads(marker.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return False
    if data.get("result") != "deployed":
        return False
    if data.get("manual_recovery_required") is True:
        return False
    return True


def newest_first(paths):
    """Sort by mtime newest first; name breaks ties so the plan is deterministic."""
    return sorted(paths, key=lambda p: (-p.stat().st_mtime, p.name))


def scan(releases):
    """Group every directory under releases by category."""
    groups = {"deploy": [], "recovery": [], "financial": [], "unknown": []}
    if not releases.is_dir():
        return groups
    for entry in releases.iterdir():
        if not entry.is_dir() or entry.name.startswith("."):
            continue
        groups[classify(entry.name)].append(entry)
    return groups


def plan(groups, keep):
    """Decide what to delete without touching the filesystem."""
    completed, incomplete = [], []
    for path in newest_first(groups["deploy"]):
        (completed if completed_release(path) else incomplete).append(path)
    protected = newest_first(completed)[:keep]
    protected_names = {p.name for p in protected}
    removable = [p for p in newest_first(completed) if p.name not in protected_names]
    return removable, incomplete, protected


def directory_bytes(path):
    total = 0
    for sub in path.rglob("*"):
        try:
            if sub.is_file():
                total += sub.stat().st_size
        except OSError:
            pass
    return total


def main(argv=None):
    parser = argparse.ArgumentParser(description="prune old deploy records in data/releases")
    parser.add_argument("--root", type=Path, default=DEFAULT_ROOT,
                        help="switchyard root (default: %(default)s)")
    parser.add_argument("--keep", type=int, default=DEFAULT_KEEP,
                        help="completed deploy records to keep (default: %(default)s)")
    parser.add_argument("--dry-run", action="store_true",
                        help="report the plan without deleting anything")
    args = parser.parse_args(argv)

    if args.keep < 1:
        raise SystemExit("--keep must be at least 1")

    releases = args.root / "data" / "releases"
    if not releases.is_dir():
        print("SUMMARY kept=0 removed=0 reclaimed_bytes=0 (no releases directory)")
        return 0

    lock = None
    if not args.dry_run:
        lock = releases / LOCK_NAME
        try:
            fd = os.open(str(lock), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            os.close(fd)
        except FileExistsError:
            # A deploy is in flight, or a previous run died. Either way we must not guess:
            # pruning during an active deploy can remove the record the operator is about
            # to need. A stale lock is cleared by a human, not by this tool.
            raise SystemExit("another release is in progress (lock %s)" % lock)

    try:
        groups = scan(releases)
        removable, incomplete, protected = plan(groups, args.keep)

        for path in incomplete:
            print("KEEP incomplete release (needs manual review): %s" % path.name)
        for path in groups["unknown"]:
            print("KEEP unrecognised directory (not created by our scripts): %s" % path.name)
        for path in groups["recovery"]:
            print("KEEP recovery point (pre-migration evidence): %s" % path.name)
        for path in groups["financial"]:
            print("KEEP financial release record (exempt from routine rotation): %s" % path.name)

        reclaimed = 0
        for path in removable:
            size = directory_bytes(path)
            if args.dry_run:
                print("WOULD REMOVE %s (%d bytes)" % (path.name, size))
            else:
                shutil.rmtree(path)
                print("REMOVED %s (%d bytes)" % (path.name, size))
            reclaimed += size

        print("SUMMARY kept=%d removed=%d reclaimed_bytes=%d"
              % (len(protected) + len(incomplete), len(removable), reclaimed))
        return 0
    finally:
        if lock is not None:
            try:
                lock.unlink()
            except OSError:
                pass


if __name__ == "__main__":
    sys.exit(main())