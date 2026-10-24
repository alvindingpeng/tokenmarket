# Routing safety review follow-up

Status: independent read-only review received after the preceding implementation/test report. Findings below are not fixed or regression-tested by this review. No deployment performed. The earlier completion statement was premature: loop-level safety does not imply transport-level single dispatch.

## Resolution status (updated)

All six priority findings are fixed with regressions; full `go test -count=1 ./...` and `go vet ./...` pass, `git diff --check` clean. No deployment performed.

1. Redirects disabled on all rhttp clients (`rejectRedirects` returning `http.ErrUseLastResponse`) in `internal/rhttp/http.go` — no POST replay, no credential forwarding, DELETE no longer rewritten to GET.
2. `request.setTargetKeyID(channelKey.ID)` wired in `internal/relay/handler.go`; duplicate channel key names rejected in `internal/op/channel.go`.
3. `model.VideoJob.ChannelID` persisted in `internal/relay/state.go` (`recordVideoJob`); deleted-grant fallback and grant-present path both fail closed on channel mismatch in `internal/relay/videotask.go`. GORM AutoMigrate adds the column; rows predating it (ChannelID=0) fail closed.
4. Upstream task IDs validated against `^[A-Za-z0-9_-]+$` via `safeVideoTaskID` before URL construction.
5. Stream finalization keeps success when delivery completed (`err = ctx.Err()` skipped when `last && err == nil`); regression `TestStreamDeliveredThenCanceledKeepsSuccess` fails against the old code and passes against the fix. gin `Flush()` returns no error, so flush-failure handling remains a known limitation.
6. `ForwardVideoTask` returns a sanitized 502 message without upstream URL; local video jobs are deleted only after a 2xx DELETE response.

Remaining known limitations: gin `Flush()` errors are unobservable at the writer layer (dead clients keep draining upstream until idle timeout); stream success bodies are buffered without a size cap; successful stream aggregation with `result.usage == nil` still settles as zero usage per the documented policy; legacy VideoJob rows without ChannelID cannot recover after grant deletion (fail-closed by design).

## Priority findings (original review)

1. **High — automatic upstream redirects.** `internal/rhttp/http.go` constructs clients without `CheckRedirect`. Default 307/308 handling can replay media POSTs; custom secret headers can reach redirect destinations. DELETE can become GET on 301/302/303, and `ForwardVideoTask` currently treats any response below 400 as deletion confirmation. Disable redirects on relay clients (consider impact on other shared-client consumers), require 2xx for deletion, and test redirect destinations receive zero requests and no secrets.
2. **Exact creation-key attribution is not wired.** `RequestState.setTargetKeyID` exists but production `Forward` does not call it after `startRound`; creation-key persistence instead depends on mutable channel/key names. Wire the selected key ID after round initialization and cover rename/duplicate-name cases.
3. **Deleted-grant recovery lacks immutable channel identity.** `VideoJob` stores key/grant IDs but no creation `ChannelID`. Recovery derives the channel from the current key row; manually moved/reused IDs can route GET/DELETE to the wrong upstream. Persist and validate channel identity with migration/backward-compatibility policy; fail closed when identity cannot be established.
4. **Task IDs are concatenated into upstream URLs.** Validate a safe task-ID alphabet or safely encode each ID as one path segment before building GET/DELETE URLs. Test traversal, query, fragment and encoded separators.
5. **Delivered stream vs cancellation race.** Streaming finalization overwrites a successful result with `ctx.Err()` even after terminal delivery. Preserve success after verified complete delivery; cover disconnect racing finalization. Flush failures also need review.
6. **Task errors expose upstream URLs.** `ForwardVideoTask` forwards raw `err.Error()` as 502, hiding upstream status/body and potentially exposing configuration. Return sanitized transport failures and preserve appropriate upstream error statuses. Success body buffering remains unbounded.

## Verified scope of existing tests

Existing relay regressions cover loop-level media single dispatch on 5xx/429, text failover, paced admission rejection without exhausting dispatch budget, disabled waiting returning 429, cancellation-success body/billing preservation, stream round finalization once, and commitment requiring positive written bytes. Video tests cover unsafe durations (including gzip), creation-key resolution with grant present, disabled creation keys, foreign/missing keys with grant present, and ordinary deleted-grant recovery. They do not cover the findings above.

The preceding full `go test -count=1 ./...` passed. This does not prove the untested transport and identity invariants. `git diff --check` was run successfully before final cleanup; no `go vet` execution was evidenced in that report despite its claim. The actual Go binary used was `/srv/deepseek-harness/workspaces/.toolchain/go/bin/go`.

## Additional unfinished verification

Verification debt closed in v0.44.1: backup-import rejection regressions cover unsupported dump versions, non-finite media prices, invalid finite settings, and invalid channel configuration; each asserts no unrelated row was imported. Cache/runtime isolation regressions cover owner-scoped same-name groups, group rename and empty-database refresh clearing stale name indexes, user cache refresh across independent databases, group-scoped route state and manual-mode bypass, full route-state reset, and egress ID isolation/reset. `go test ./... -count=1`, `go vet ./...`, `gofmt` on changed Go files, and `git diff --check` pass. These are code-level regressions; real MySQL/PostgreSQL service acceptance and live-funds recharge/withdrawal E2E remain separate operational gaps.

## Security hardening batch 1 (v0.45.0)

The two long-accepted streaming limits from this review are now enforced in code: non-stream upstream responses are capped at 128 MiB by a transport-layer limited reader (SSE bypasses it; per-event caps already exist in the decoder), and the relay aggregation buffer for billing caps accumulated stream bytes at 128 MiB, failing the round instead of buffering unboundedly. `usage == nil` on a **succeeded** request now releases the reservation (unchanged: no invented billing) and raises a `usage_missing` alert so free-usage upstreams enter the reconcile view; failed/canceled requests without usage stay silent as before.

New SSRF defense: `internal/netguard` rejects loopback/private/link-local/multicast targets at channel-save time (BaseURL, channel proxy, egress proxy URLs) and re-checks resolved addresses inside `DialContext` on every connection, closing the DNS-rebinding gap; `allow_private_upstream` (default false) is the explicit admin exception for self-hosted upstreams. Default-admin exposure is closed: the plaintext password line is gone from bootstrap logs, `admin_default_pwd_changed` gates every Auth-protected admin request until the password is changed (change-password/logout/status stay reachable), and login/status responses carry `must_change_password` for the frontend. Login throttling (`login_max_attempts`/`login_lockout_seconds`) predates this batch and is unchanged. Full `go test ./... -count=1` and `go vet ./...` pass; httptest-based suites enable the private-upstream exception explicitly.
