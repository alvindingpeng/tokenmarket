# Update pipeline

The admin Settings → Info page checks the public GitHub Releases for `alvindingpeng/tokenmarket`.

## Availability checks

- `GET /api/v1/update` returns cached release metadata; administrators can force a fresh check with `?force=1`.
- `POST /api/v1/update/check` always performs a fresh metadata check.
- The background task warms the cache at the configured `update_check_interval` (default 60 minutes) while `update_check_enabled` is true.
- A release is usable only when it contains the platform archive named `octopus-<os>-<arch>.zip`.

## Applying an update

1. The backend downloads the matching ZIP and enforces the response size limit.
2. The archive is checked for traversal and symlink entries.
3. The archive SHA-256 digest is checked when GitHub provides one.
4. The candidate executable is run with `version` and must report the release tag.
5. The new executable is staged, the current executable is retained as `.old`, and rename operations install the new inode.
6. The HTTP response is flushed, then the process restarts with the same arguments through `syscall.Exec`.

The updater refuses same-version and downgrade updates by default. The `.old` file is intentionally retained for manual rollback.

## Release assets

Version tags trigger `.github/workflows/release-assets.yaml`. The workflow builds the embedded frontend, cross-compiles Linux, Windows, and macOS standard targets, creates the ZIP archives, and uploads them to the matching GitHub Release. Android is intentionally skipped in this workflow because the fork does not require an Android NDK for server releases.
