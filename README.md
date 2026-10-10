<div align="center">

<img src="web/public/logo.svg" alt="Octopus Logo" width="120" height="120">

### Octopus

**A self-hosted LLM API gateway: aggregate, route, meter, and operate — as a single binary**

`v0.15.0` · English | [简体中文](README_zh.md)

</div>

> **This repository** — [alvindingpeng/tokenmarket](https://github.com/alvindingpeng/tokenmarket) —
> is a production fork of the upstream [bestruirui/octopus](https://github.com/bestruirui/octopus)
> project (baseline: upstream `v0.13.9`). Everything below describes **what this fork actually runs
> today**; the delta against upstream is itemised in [CHANGELOG.md](CHANGELOG.md).

---

## Table of Contents

- [What it does](#what-it-does)
- [Quick start](#quick-start)
- [Compatibility](#compatibility)
- [Relay: routing, failover, error handling](#relay-routing-failover-error-handling)
- [Pricing and billing](#pricing-and-billing)
- [Channels, groups, and publishing](#channels-groups-and-publishing)
- [Operations: health, alerts, metrics, backups](#operations-health-alerts-metrics-backups)
- [Limits and access control](#limits-and-access-control)
- [Roles and the web console](#roles-and-the-web-console)
- [Site identity and maintenance mode](#site-identity-and-maintenance-mode)
- [Configuration](#configuration)
- [API surface](#api-surface)
- [Client integration](#client-integration)
- [Screenshots](#screenshots)
- [Documentation](#documentation)
- [Build from source](#build-from-source)
- [Testing and CI](#testing-and-ci)
- [Versioning and releases](#versioning-and-releases)
- [Roadmap](#roadmap)
- [Acknowledgments](#acknowledgments)

---

## What it does

Octopus puts a single OpenAI-shaped endpoint in front of many upstream providers. Your clients keep
calling `/v1/chat/completions`; Octopus picks the channel, converts the protocol if needed, reserves
balance, meters the usage, and fails over when an upstream misbehaves.

| Area | What you get |
|------|--------------|
| 🔀 **Aggregation** | Many channels behind one model name, each with its own credentials and limits |
| 🔄 **Protocol conversion** | OpenAI Chat ⇄ OpenAI Responses ⇄ Anthropic Messages, inbound and outbound |
| 🎨 **Image generation** | `/v1/images/generations` and `/v1/images/edits` forwarded, billed per image |
| 🧲 **Routing** | 7 selection modes: manual, failover order, cheapest, fastest, most reliable, weighted score, random |
| 🛡️ **Failover** | Per-member retries, cooldowns, and a bounded wait queue when nothing is available |
| 💰 **Billing** | Balance reservation before the call, settlement after, daily reconciliation, price snapshots |
| 📊 **Observability** | Live request traces, billing records, audit log, `/healthz`, Prometheus `/metrics`, ops dashboard |
| 🔐 **Multi-user** | Three roles (admin / reseller / user), per-user API keys, optional approval on signup |
| 🏷️ **White label** | Site name, description, contact, announcement banner, maintenance mode |
| 📦 **Deployment** | One static binary, embedded frontend, SQLite / MySQL / PostgreSQL |

**Deliberately absent:** no built-in payment gateway (balances are topped up by admins), and no
built-in billing currency conversion — prices are stored per model and charged against a numeric balance.

---

## Quick start

### Get a binary

**This fork publishes standard release archives.** The tag-triggered release workflow uploads
`octopus-<os>-<arch>.zip` assets (including the running platform) after the release notes are created.
For a local build or an offline deployment, build the binary yourself:

```bash
bash scripts/build-local.sh -o ./octopus    # one binary for the machine you are on
# or, for the whole platform matrix (linux amd64/arm64/armv7/386, windows amd64,
# darwin amd64/arm64, android): bash scripts/build.sh  -> build/archives/octopus-<os>-<arch>.zip
```

Then:

```bash
./octopus start
```

Data lives in `./data/` next to the binary: `data/config.json` is written on first start, `data/data.db`
is the default SQLite database.

### Docker

This fork publishes **no container image** — the upstream workflow pushes to registries this repository
does not own, so `docker run` examples pointing at upstream image names will not deliver this fork's code.
Build the image locally instead:

```bash
./scripts/build.sh            # cross-compiles into build/
docker build -f scripts/dockerfile/Dockerfile -t octopus .
docker run -d --name octopus -v /path/to/data:/app/data -p 8080:8080 octopus
```

The repo ships a `docker-compose.yml`, but note its `image:` line still points at the **upstream** image
name (`bestruirui/octopus`) — swap it for the tag you just built locally before `docker compose up -d`, or
you will be running upstream's build with none of this fork's code.

### First login

Open `http://localhost:8080` and sign in with the bootstrapped account:

- **Username**: `admin`
- **Password**: `admin`

> ⚠️ **Change this password immediately after the first login**, before the port is reachable from anywhere
> but `127.0.0.1`. Registration is closed by default, so `admin` stays the only account until you open it.

---

## Compatibility

### Inbound (what your clients can call)

```
POST /v1/chat/completions     # OpenAI Chat Completions
POST /v1/responses            # OpenAI Responses
POST /v1/messages             # Anthropic Messages
POST /v1/images/generations   # OpenAI Images
POST /v1/images/edits         # OpenAI Images (multipart, multiple input images)
GET  /v1/models               # model list visible to the calling key
```

Authenticate with an API key created in the console, sent as `Authorization: Bearer sk-octopus-…` or
`x-api-key: sk-octopus-…`. The `/v1` surface accepts **keys only** — a console session cookie is not an API
credential and is rejected there. Disabled, expired, or quota-exhausted (`max_cost`) keys are refused at
the door.

### Outbound (how a channel is addressed)

A channel is a **protocol** plus a base URL. The program appends the endpoint path itself, so the base
URL must **not** include `/v1` or a specific endpoint:

| Channel protocol | Auto-appended path | Base URL example |
|------------------|--------------------|------------------|
| OpenAI Chat | `/v1/chat/completions` | `https://api.openai.com` |
| OpenAI Responses | `/v1/responses` | `https://api.openai.com` |
| Anthropic Messages | `/v1/messages` | `https://api.anthropic.com` |
| OpenAI Images | `/v1/images/generations`, `/v1/images/edits` | `https://api.openai.com` |

> 💡 **Gemini and other providers.** Native Gemini protocol support was removed upstream at `v0.13.9`:
> `gemini`-typed channels migrate to the OpenAI Chat protocol. Point such channels at the provider's
> OpenAI-compatible endpoint instead.

### Dialects

Providers under the same protocol still differ in body details (for example, thinking content carried as
`reasoning_content` versus `reasoning`). Those differences are expressed by a per-channel **dialect**, not
by a URL hack. Only `generic` — standard protocol, no vendor specialisation — is defined today; the
mechanism exists so adding a vendor specialisation is a code change, never a schema change.

---

## Relay: routing, failover, error handling

A **group** is the model name your clients see. It contains ordered members, each backed by a channel.
The group's **mode** decides which member serves a request:

| Mode | Behaviour |
|------|-----------|
| `manual` | Only the members you selected, in your order. No dynamic choice. |
| `failover` | Follow the member order; move to the next one on failure. |
| `price` | Cheapest user-facing price first; escalate by price on failure. |
| `latency` | Fastest first, from a sliding average of time to first response. |
| `success` | Most reliable first, from a sliding-window success rate. |
| `score` | Weighted score of price, latency, and success rate. Default weights **40 / 30 / 30**, settable per group and per system. |
| `random` | Spread requests evenly across available members. |

For `price` and `score` you choose the **price metric** to compare: `blended` (average of input and
output), `input`, or `output` — a model that is cheap to read but expensive to generate should not win
on the wrong axis.

### Per-group relay knobs

Start member timeout, member retry count and interval, per-member streaming idle timeout, non-stream
response timeout, whole-request deadline (retries and streaming included), maximum wait when no member is
available, maximum queued waiters, and maximum attempts across members. Every value is clamped at the
backend (`1–3600s` for timeouts, `0` disables queueing or waiting) so a typo cannot produce an
unbounded queue or a request that never gives up.

### Failover hygiene

- A member that fails goes into a **cooldown** instead of being hammered; cooldowns and per-member
  metrics survive a restart (`routepersist`), so a cold process does not stampede the upstream that just
  shed load.
- When every member is busy or cooling down, requests enter a bounded **wait queue**; past
  `max_wait_seconds` they are rejected rather than left hanging.
- **Upstream errors are absorbed, not surfaced blindly**: failures either succeed on another member or
  produce one synthesised error the client can parse. Agent loops do not die on a 5xx from one provider.

### Live tracing

The log page streams in-flight requests over SSE (`/api/v1/log/overview/stream`) and exposes each attempt
per request, the raw request/response bodies, and a stop control for a running stream. Tracing starts
when the client sends the request — not when the upstream answers.

---

## Pricing and billing

### Where prices come from

1. **models.dev** — synced on a schedule (`model_info_update_interval`, default 24h) into a rebuildable
   baseline table. `/api/v1/model/rebuild-price` regenerates it from scratch.
2. **Price management page** — your own overrides. These win over the synced values.
   Every model touched by a channel but absent from models.dev gets a row created automatically, so you
   always have somewhere to set the price rather than a silently unbilled model.
3. **Per-channel cost** — what you pay the provider. Managed in the **publish console** (模型上架与定价):
   supply price per model and per credential, and the user-facing price is derived as
   `supply × (1 + markup_ratio)`, default markup `0.2`. Zero-price publish attempts are blocked.

Two dimensions are charged separately: **tokens** (input / output / cache read / cache write) and
**media** (per image; per-second and tiered media pricing are modelled for future protocols).

### How a request is billed

```
reserve → upstream call → settle → release
```

- **Reserve** freezes an estimate against the user's balance before the request goes upstream: no balance,
  no call. The estimate uses a default output-token cap (`balance_reserve_output_cap`, 4096) and a
  conservative image count (`balance_reserve_images`, 4, validated 1–64) when the caller omits `n`.
  `min_balance` sets the floor below which a call is refused.
- **Settle** replaces the estimate with actual usage from the upstream response. Usage numbers are
  **taken from upstream, never re-derived by a local tokenizer** — billing must match what the provider
  bills you, or every reconciliation report is fiction. Where an upstream returns nothing usable, the
  fallback estimate is recorded as such.
- **Release** runs on a 10-minute sweep: any reservation unsettled for 30 minutes is treated as a leak
  and unfrozen. A crashed request must not freeze money forever.

### Proof rather than hope

- Every request writes a **price snapshot** alongside the charge. Repricing a model must never rewrite
  history; an invoice issued before a price change has to remain explicable.
- A **daily reconciliation** task re-derives yesterday from the logs and records agreement or drift.
  It **only reports** — it never adjusts a balance automatically.
- **Billing records** (filter by model, channel, status; paginate; CSV export) and the **balance ledger**
  (reserve / settle / release / adjustment) are both user- and admin-facing.
- Everything an admin does to money, models, or users lands in the **audit log**, and the audit export
  itself is audited (`audit.export` records how many rows of how many, under which filters).

---

## Channels, groups, and publishing

### Channels

A channel is one upstream: a name, a protocol, one or more **named credentials**, optional proxy settings,
model grants, and per-credential cost.

- **Auto-add models** (`model_auto_add`, default **off**). When enabled, saving a channel probes the
  upstream `/models` list and merges it in. The rules are what make this safe to leave switched on:
  - **additive only** — models the upstream stopped returning are kept, never deleted;
  - **grants are OR-merged** — probing adds protocol bits, it never clears a grant you set by hand;
  - **capped at 200 new models** per save, with truncation written to the audit entry;
  - **audited** as `channel.model-added` with the added names.
  The probe is shared with the manual refresh, so a preview and a save always see the same thing, and a
  probe failure is best-effort: it logs, it never fails a save that already worked.
- **Publishing**: a channel can be published to the user side under a **share code** rather than its real
  name, so resellers and users never see which provider or credential served them.
- **Proxies** are configurable globally (`proxy_url`) and per channel, and the model probe honours them.
- Per-channel statistics: request counts, tokens, cost, success/failure, and a daily series.

### Model capabilities

Image-capable models are classified by name pattern (dall-e, flux, …) when auto-added, and the
classification is recomputed when grants change. This is a deliberate approximation — a capability probe
is on the roadmap.

### Rate limiting

Seven scopes, most specific wins: `system` → `user` → `api_key` → `group` → `channel` → `channel_key` →
`channel_model`. Each policy carries **RPM** (requests/minute), **TPM** (tokens/minute), and a concurrency
cap; `0` means unlimited. Real-time usage is readable next to the policy, so you can see how close a key
is to its ceiling instead of guessing from 429s.

---

## Operations: health, alerts, metrics, backups

### Probes

```
GET /healthz    # no auth, ever — liveness: status, db, version, active_requests, uptime
GET /metrics    # Prometheus text format; optional bearer auth
```

`/healthz` answers `503 degraded` when the database stops responding. It is deliberately never
authenticated: adding auth to a liveness probe just breaks orchestrators.

`/metrics` exposes `octopus_up`, `octopus_build_info`, `octopus_relay_requests`,
`octopus_relay_tokens_total`, `octopus_billing_cost_total`, `octopus_active_requests`, `octopus_channels`,
`octopus_users`, `octopus_user_balance_total`, `octopus_alerts_raised_24h`, plus Go runtime counters.
Set `metrics_auth` to `bearer` to require a token (`Authorization: Bearer …` or `?token=`); the token is
generated on demand and rotatable from the console.

### Channel health checks

Off by default, because active probing costs real money upstream. When enabled it probes channels on an
interval, marks a channel `degraded` when latency exceeds `health_latency_ms`, and only declares it `down`
after a consecutive failure streak (`alert_fail_streak`) — single flakes do not page anyone. A recovery
emits a `recovered` event.

### Alerts

- Kinds include channel failures, reconciliation drift, settlement failure, backup failure, archive
  failure, and **low balance** (`low_balance`: predicted at settlement against `min_balance + estimate`,
  with a backstop when a reservation is refused — two triggers, same reason, deduplicated).
- Deduplicated per channel/type/reason inside a window (`alert_dedup_minutes`, default 10).
- Delivered to an in-console feed and, if `alert_webhook_url` is set, to your webhook. A test button
  validates the URL without waiting for a real incident.
- Thresholds and weights are editable in the console, and `/api/v1/ops/alerts/rules` echoes the
  **effective** rules — defaults included — so what the UI shows is what is running.

### Backups

- Automatic consistent backups on an interval (`auto_backup_interval`, default 24h), keeping
  `auto_backup_keep` copies (default 7), each verified after writing.
- Optional **AES-256-GCM** encryption (`backup_encrypt`) and **WebDAV** offsite push
  (`backup_remote`) with a `.sha256` checksum beside every file.
- The passphrase and remote credentials live in `config.json` / environment only — never in the database,
  because the database is what gets backed up.
- Restore and verification are CLI:

```bash
./octopus backup verify  --file data/backup/db-20261009.db.sha256
./octopus backup decrypt --in backup.db.enc --out backup.db
./octopus version
```

### Log lifecycle

Call logs, attempts, and bodies are archived then pruned hourly past `log_retention_days` (default 7,
archiving controlled by `log_archive_enabled`). Request and response bodies are stored by default
(`log_store_body`) with configurable field masking (`log_mask_fields`, comma-separated JSON keys) — set it
before you keep bodies on a public box, since bodies are exactly where secrets live.

### Ops dashboard

`/api/v1/ops/overview` gives 24h requests, success rate, tokens, cost, in-flight count, channel
enable/total, and alerts raised; the console surfaces backup, reconciliation, log-lifecycle, health, and
alert history, each with a manual "run now".

---

## Limits and access control

| Concern | Mechanism |
|---------|-----------|
| Money | Balance reservation before the call, `min_balance` floor, per-key `max_cost` quota and `expire_at`, allowed-group whitelist per key |
| Volume | RPM / TPM / concurrency per scope, most-specific policy wins |
| Overload | Bounded wait queue, whole-request deadline, per-member attempt caps, cooldowns |
| Origins | `cors_allow_origins` (empty = allow no cross-origin; `*` = allow all) |
| Abuse of the console | Session cookie auth, role checks per route, `/api/v1/*` never public except `/api/v1/site/config` and the register/login endpoints |
| Secrets | API keys are `sk-octopus-…`; `auth_secret` and `metrics_token` are internal keys never exposed by the settings API; log bodies can be masked; backup credentials stay out of the database |
| Model surface | `model_filter` applies a global pattern when a channel lists models |

---

## Roles and the web console

| Role | Pages |
|------|-------|
| **user** | Home · Model configuration (groups) · Billing · API keys · Logs · Settings |
| **reseller** | + Channels · Publish console |
| **admin** | + Users · Audit · Rate limits · Ops centre · Price management · Settings (all tabs) |

The console is React 19 + TypeScript + Vite + Tailwind CSS v4, TanStack Query, Zustand, and `use-intl`
(en / zh_hans / zh_hant), installable as a PWA (`web/public/manifest.json` and a service worker that
scope-isolates its caches, so two deployments under different subpaths do not fight over storage).
Request-trace pages stream over SSE. Group members reorder by drag, and group edits support pin-to-top /
move-to-bottom.

**Subpath deployment**: the frontend builds with `base: './'` and the service worker derives its paths from
the registration scope, so the app runs behind `https://example.com/octopus/` under a reverse proxy without
rebuilding.

### Registration

Both signup flows are **off by default**: self-service user registration
(`register_user_enabled`) and reseller registration (`register_reseller_enabled`). When open,
`register_approval_required` (default **on**) puts new accounts into a pending state an admin must approve —
and the login page reads this from `/api/v1/user/register-config` so it tells registrants the truth about
whether they can sign in yet.

---

## Site identity and maintenance mode

Seven settings under **Settings → System Information** are served by one endpoint,
`GET /api/v1/site/config`, which needs no authentication — a signed-out visitor still sees who they are
looking at:

| Field | Where it shows up |
|-------|-------------------|
| `site_name` | Browser tab title, login page heading, API-key dashboard heading, share-image header. Empty falls back to `Octopus` rather than a blank title. |
| `site_description` | One line under the login heading |
| `site_contact` | Login page; auto-linked when it is a URL or an email, plain text otherwise |
| `announcement` + `site_announcement_enabled` | Dismissible banner for signed-in users; the switch is applied **server-side**, so a disabled announcement leaves no text in the payload |
| `maintenance_mode` + `maintenance_notice` | Non-stoppable red banner everywhere including the login page; non-admin logins are rejected with `503` carrying the same sentence |

Saving any setting invalidates the site-config and register-config caches, so the console reflects changes
in the same frame instead of after a reload. Details in
[docs/site-info-distribution.md](docs/site-info-distribution.md).

> ⚠️ **Maintenance mode is not a firewall.** It blocks non-admin **logins**; requests already carrying a
> valid session keep working, and `/healthz`, `/metrics`, and `/v1` relay traffic are untouched. Use it to
> lock humans out of the console, not to stop traffic.

---

## Configuration

### `data/config.json`

```json
{
  "server":   { "host": "0.0.0.0", "port": 8080 },
  "database": { "type": "sqlite",  "path": "data/data.db" },
  "log":      { "level": "info" },
  "backup":   { "passphrase": "", "remote_url": "", "remote_user": "", "remote_password": "" }
}
```

| Option | Description | Default |
|--------|-------------|---------|
| `server.host` | Listen address | `0.0.0.0` |
| `server.port` | Server port | `8080` |
| `database.type` | `sqlite` \| `mysql` \| `postgres` | `sqlite` |
| `database.path` | SQLite file path, or DSN | `data/data.db` |
| `log.level` | `debug` \| `info` \| `warn` \| `error` | `info` |
| `backup.passphrase` | Backup encryption passphrase; empty = unencrypted | `""` |
| `backup.remote_url` | WebDAV directory; empty = no offsite push | `""` |
| `backup.remote_user` / `backup.remote_password` | WebDAV credentials | `""` |

Every key is overridable through the environment as `OCTOPUS_` + the path joined with `_`
(`OCTOPUS_SERVER_PORT`, `OCTOPUS_DATABASE_TYPE`, `OCTOPUS_DATABASE_PATH`, `OCTOPUS_LOG_LEVEL`,
`OCTOPUS_BACKUP_PASSPHRASE`, …). `OCTOPUS_GITHUB_PAT` raises the GitHub rate limit for version checks.

### Database

| Type | `database.path` format |
|------|----------------------|
| SQLite | `data/data.db` |
| MySQL | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `host=localhost user=postgres password=xxx dbname=octopus port=5432 sslmode=disable` |

> 💡 Create the MySQL/PostgreSQL database yourself; the schema is migrated automatically.
> Schema migrations are ordered, additive files under `internal/db/migrate/` — they never rewrite history,
> which is what makes the "repricing must not alter old invoices" guarantee hold across upgrades.

### Runtime settings (not in the config file)

Thirty-plus operational settings — statistics flush interval, model info sync interval, markup ratio,
registration switches, reserve caps, score weights, log retention, backup cadence, health probe tuning,
alert thresholds, metrics auth, site identity — live in the database under the `setting` table and are
edited in the console. They are **exportable and importable** as JSON (`/api/v1/setting/export`,
`/api/v1/setting/import`), so a tuned instance can be reproduced on another.

> ⚠️ **Shutdown with `Ctrl+C` or `SIGTERM`, never `kill -9`.** Statistics accumulate in memory and flush to
> the database every `stats_save_interval` minutes (default 10); a forced kill drops whatever is buffered.

---

## API surface

Console API under `/api/v1`, session-cookie authenticated, role-checked per route:

```
/api/v1/user, /user/manage     login, register, register-config, profile, admin user CRUD, approval
/api/v1/apikey                 create, list, update, delete, stats, key login
/api/v1/channel                CRUD, enable, stats + daily series, grants, fetch-model, publish, models
/api/v1/group                  CRUD, events, per-group metrics
/api/v1/model                  list, create, update, delete, update-price, rebuild-price, last-update-time
/api/v1/stats                  daily, hourly, total, per-key, billing, billing records + export, ledger, revenue
/api/v1/log                    list, export, clear, SSE stream, attempts, request/response bodies, stop
/api/v1/audit                  list, actions, export (CSV, capped and self-auditing)
/api/v1/ratelimit              policy CRUD, inspect, usage
/api/v1/ops                    overview, reconcile, backup, log-lifecycle, health, alerts, metrics token
/api/v1/setting                list, set, export, import (admin only)
/api/v1/site                   config (public)
/api/v1/update                 status, forced check, now-version, perform update (admin only)
```

Version notes on the surface: `POST /api/v1/user/update` handles both admins editing other accounts and
a user changing their own balance-facing fields — one endpoint, role-checked inside; `/api/v1/channel/list`
and `/api/v1/group/list` are `GET` with no body. The internal setting keys `auth_secret` and
`metrics_token` never appear in `/api/v1/setting/list`.

---

## Client integration

A **group name** is the model name clients pass. Keys look like `sk-octopus-<48 chars>` — create them under
**API keys**, and never hand out an admin session cookie as a substitute.

### OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key="sk-octopus-REPLACE_ME",   # created in the console under API keys
)
completion = client.chat.completions.create(
    model="octopus-openai",           # the group name
    messages=[{"role": "user", "content": "Hello"}],
)
print(completion.choices[0].message.content)
```

The same key works against `/v1/responses` for clients that speak the Responses API.

### Claude Code

`~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_AUTH_TOKEN": "sk-octopus-REPLACE_ME",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "ANTHROPIC_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_SMALL_FAST_MODEL": "octopus-haiku-4-5",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "octopus-sonnet-4-5",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "octopus-haiku-4-5"
  }
}
```

### Codex

`~/.codex/config.toml`:

```toml
model = "gpt-5"
model_reasoning_effort = "xhigh"
model_provider = "octopus"
preferred_auth_method = "apikey"

[model_providers.octopus]
base_url = "http://127.0.0.1:8080/v1"
name = "octopus"
supports_websockets = false
requires_openai_auth = true
wire_api = "responses"
experimental_bearer_token = "sk-octopus-REPLACE_ME"
```

Leave `OPENAI_API_KEY` empty in `~/.codex/auth.json` so the bearer token above is used.

> 💡 The `model` values above are **group names you create**, not real provider models. A group called
> `octopus-sonnet-4-5` may sit in front of any channel that speaks Anthropic or OpenAI protocol.

---

## Screenshots

### Desktop

<div align="center">
<table>
<tr>
<td align="center"><b>Dashboard</b></td>
<td align="center"><b>Channel Management</b></td>
<td align="center"><b>Group Management</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="Dashboard" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="Channel" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="Group" width="400"></td>
</tr>
<tr>
<td align="center"><b>Price Management</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="Price Management" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="Logs" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="Settings" width="400"></td>
</tr>
</table>
</div>

### Mobile

<div align="center">
<table>
<tr>
<td align="center"><b>Home</b></td>
<td align="center"><b>Channel</b></td>
<td align="center"><b>Group</b></td>
<td align="center"><b>Price</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="Mobile Home" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="Mobile Channel" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="Mobile Group" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="Mobile Price" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="Mobile Logs" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="Mobile Settings" width="140"></td>
</tr>
</table>
</div>

> Screenshots of the newer pages (users, audit, rate limits, ops centre, billing, publish console) have not
> been captured yet; they follow the same layout as the pages above.

---

## Documentation

Design notes live in `docs/`, written at the time the decision was made rather than reconstructed later:

| Document | Covers |
|----------|--------|
| [billing-detail.md](docs/billing-detail.md) | Billing records, balance ledger, CSV export |
| [billing-audit.md](docs/billing-audit.md) | The "balance was not deducted" investigation, and what was really wrong |
| [channel-pricing.md](docs/channel-pricing.md) | Per-channel / per-credential repricing workbench |
| [image-generation.md](docs/image-generation.md) | Image support: protocols, grants, per-image billing |
| [reliability-first-batch.md](docs/reliability-first-batch.md) | Timeouts, retries, cooldowns, the wait queue |
| [ops-observability.md](docs/ops-observability.md) | Log search/export, `/healthz`, `/metrics`, ops overview |
| [ops-alert-loop.md](docs/ops-alert-loop.md) | Alert loop and health-driven alerts |
| [ops-second-batch.md](docs/ops-second-batch.md) | Second round of operational reliability |
| [probes-and-alert-rules.md](docs/probes-and-alert-rules.md) | Probe authentication, configurable alert rules |
| [backup-hardening.md](docs/backup-hardening.md) | AES-256-GCM encryption, WebDAV push, checksums |
| [audit-export-and-low-balance.md](docs/audit-export-and-low-balance.md) | Audit CSV export and low-balance alerts |
| [site-info-distribution.md](docs/site-info-distribution.md) | Site config endpoint, consumers, timing, layout traps |
| [frontend-tests.md](docs/frontend-tests.md) | What the vitest suite covers |
| [roadmap.md](docs/roadmap.md) | Backlog, including what was rejected and why |

---

## Build from source

**Requirements:** Go `1.26.4` (per `go.mod`), Node.js `^20.19 || >=22.12` (Vite 8 requires it), pnpm.

```bash
git clone https://github.com/alvindingpeng/tokenmarket.git
cd tokenmarket
cd web && pnpm install && pnpm run build && cd ..   # frontend must be built first
go run main.go start
```

> 💡 `static/static.go` embeds `static/out` into the binary. Build the frontend **before** the backend,
> or you ship yesterday's UI inside today's binary.

Two build scripts matter if you deploy this fork:

```bash
bash scripts/build-local.sh -o /path/to/binary   # local: web + backend, injects BOTH version strings
bash scripts/build.sh                            # release matrix: cross-compile every platform
```

`build-local.sh` passes `VITE_APP_VERSION` to the frontend and `-X internal/conf.Version` to the backend
from the same source. A plain `go build` yields `version=dev`, and the console then warns that the
frontend and backend versions differ — which is exactly how a stale-asset alarm was misdiagnosed here once.

**Development mode** (hot reload, API proxied to `127.0.0.1:8080`):

```bash
cd web && pnpm run dev     # http://localhost:5173
go run main.go start       # in another terminal
```

---

## Testing and CI

```bash
cd web && pnpm exec tsc --noEmit        # types (also part of pnpm run build)
cd web && pnpm exec vitest run          # 24 frontend unit tests, 4 files
go vet ./internal/...                   # backend static checks
go test ./internal/...                  # backend unit tests
gofmt -l internal/server/handlers/      # must print nothing
```

- The vitest suite targets **pure frontend logic**: price formatting, stats math, URL assembly. It was
  written from zero in this fork; see [docs/frontend-tests.md](docs/frontend-tests.md).
- The Go unit tests (in `internal/model`, `internal/op`, `internal/ratelimit`, `internal/relay`) cover the
  auto-add merge rules, alert dedup, backup crypto and remote push, log masking, limit clamping, and the
  wait queue. `gofmt` is gated only on `internal/server/handlers/`; `internal/` still carries upstream
  formatting drift.
- Cross-stack behaviour is covered by an out-of-tree end-to-end harness (156 assertions: billing, settlement,
  rate limits, image protocols, auto-add rules) that builds a temporary binary with a fresh SQLite
  database on an isolated port, so it never touches production data.
- `.github/workflows/build.yaml` builds frontend + backend on every push. `.github/workflows/release-assets.yaml`
  builds the frontend and standard cross-platform archives on version tags, then uploads them to the matching Release.
  The existing upstream-oriented `release.yaml` remains separate from this fork's release-asset workflow.

---

## Versioning and releases

Every update carries a version number, and release notes land in this repository.

| Source of truth | Location | Read by |
|-----------------|----------|---------|
| Version | the `// Version vX.Y.Z` marker in `main.go` | `scripts/version.sh`, CI release, `scripts/publish-release.sh` |
| Release notes | the `## vX.Y.Z` section of [CHANGELOG.md](CHANGELOG.md) | `scripts/publish-release.sh` → GitHub Release body |

Semver here: a new user-visible capability bumps the **minor** version; fixes, styling, docs, and copy bump
the **patch** version. At runtime the version appears in `octopus version`, `/healthz`,
`octopus_build_info`, and the console's Settings → Info page.

A `post-commit` hook keeps the fork in sync in real time: it pushes `HEAD` to `main` on GitHub, then — if
the marker changed — tags, pushes the tag, and creates the Release from the matching CHANGELOG section
(idempotent: it PATCHes rather than duplicating). Logs: `.git/auto-push.log`, `.git/publish-release.log`.

> ⚠️ **Bump `main.go` before you commit**, not after. Committing without bumping makes the next release
> attempt re-point an existing tag's release at a new commit while the tag stays behind.

See [docs/release-versioning.md](docs/release-versioning.md) for the full procedure, [docs/update-pipeline.md](docs/update-pipeline.md)
for the self-update and asset pipeline, and [CHANGELOG.md](CHANGELOG.md) for what changed in each version.

---

## Roadmap

Known gaps in this fork, with the reasoning kept where the code is:

| Area | Status |
|------|--------|
| Video generation (`/v1/videos`, per-second billing) | Protocol bit `1 << 5` reserved, route not implemented |
| Streaming image results (`partial_images`) and upstream usage reconciliation | Not started |
| Model capability detection by probe instead of name keywords | Not started |
| Scheduled channel model sync (cron rather than on-save) | Rejected for now: unbounded upstream cost and churn risk |
| Automatic pricing / automatic listing on auto-add | Deliberately excluded: both are money-moving actions |
| In-app self-update | Enabled for admins; checks this fork's Releases and atomically replaces the service binary from the matching `octopus-<os>-<arch>.zip` asset |
| Container images for this fork | Not published |

Full backlog: [docs/roadmap.md](docs/roadmap.md).

---

## Acknowledgments

- 🙏 [bestruirui/octopus](https://github.com/bestruirui/octopus) — the upstream project this fork builds on
- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) — the LLM API adaptation module derives from it
- 📊 [sst/models.dev](https://github.com/sst/models.dev) — model pricing data
- 🇨🇳 [AtomGit](https://atomgit.com/bestruirui/octopus) — China-based mirror of upstream
- 💬 [Linux.do](https://linux.do/)

## License

AGPL-3.0-only, as upstream. See [LICENSE](LICENSE).

