# bettersentryio

**Single-binary error tracking + background-loop liveness monitoring.**
The 5% of Sentry a small team actually needs, without the 42-process fleet.

> Product = repo = binary: **bettersentryio** (naming decision D9 in PLAN.md).
> Status: **running.** Error ingest, loop liveness, stall detection, alerting, the
> operator UI and a Sentry-compatible read API are all built and exercised daily.
> See [DEVELOPING.md](DEVELOPING.md) to run it, [PLAN.md](PLAN.md) for the decisions
> and [ARCHITECTURE.md](ARCHITECTURE.md) for the shape.

## Why this exists

Two real incidents on our AI infra:

1. **The TTS outage.** Our TTS API was broken for two days because of an internal PyTorch
   failure. `/health` returned 200 the whole time — the HTTP server was fine; the **background
   batching loop** behind it was dead. Nothing paged.
2. **The vLLM "super silence".** A `torch.compile` shape mismatch can leave vLLM in a state
   where the process is alive, the loop is "running", and no exception is ever raised. Rare,
   brutal to notice.

Health checks answer *"is the process up?"*. Nobody was answering *"is the work happening?"*.

Sentry solves the adjacent problems (error capture, cron check-ins) but self-hosting it means
**21 containers + 21 daemons, 6 storage engines, 52 Kafka topics, ~766k lines of Python**
(measured from source — see [docs/research/sentry-bloat-inventory.md](docs/research/sentry-bloat-inventory.md)).
The lightweight Sentry-compatibles (GlitchTip, Bugsink) don't do heartbeats; the heartbeat tools
(Healthchecks) don't do errors. bettersentryio fuses both cores into one binary.

## What it does

- **Error tracking** — point your existing `sentry-sdk` DSN at bettersentryio. The stock
  SDK's envelopes are accepted unchanged (D14): exceptions with stacktraces, locals,
  breadcrumbs, contexts, tags, releases, sessions, attachments and cron check-ins.
  Events group into issues by fingerprint, per environment.
- **Loop liveness (the flagship)** — your loop sends a one-line heartbeat per iteration.
  No beat within the window → alert. Sentry-Crons-compatible check-ins are also accepted, so
  `@sentry_sdk.crons.monitor` works unmodified.
- **Stall detection (beyond Sentry)** — heartbeats can carry a monotonic progress counter
  (batches processed, tokens generated). Beats arriving but the counter frozen → **stalled**
  alert. This is the vLLM case: alive, looping, doing nothing.
- **Issue workflow** — resolve, archive (forever, for a while, or until it recurs),
  priority, delete; per-issue and in bulk. A recurrence reopens what you called fixed.
- **Alerting** — a catalogue of channels (webhook, Slack, Microsoft Teams, Telegram)
  defined once, imported per project, with dedup, per-project patience windows that
  collapse a burst into one digest, and recovery notices.
- **Dashboards without a plugin** — the engine answers Sentry's *Web* API, so Grafana's
  official Sentry datasource queries it unmodified.
  See [docs/design/grafana-datasource.md](docs/design/grafana-datasource.md).

## What it will never do

Tracing/APM, session replay, profiling, per-token permission scopes, orgs/teams/SSO
inside the engine, multi-region anything. If we need those we'll buy them.

## Shape

```
┌──────────────────────── one Go binary ─────────────────────────┐
│  ingest (Sentry envelope + native API) → store (PostgreSQL)    │
│  detector (1 ticker) → alerter (webhooks) → JSON read API      │
└────────────────────────────────────────────────────────────────┘
        ▲                        │                      ▲
   sentry_sdk / beat()           ▼                 grafana-sentry-datasource
                          operator UI (Next.js)
```

Two processes, one database, two schemas: the **engine** owns `public.*` through its own
migrations, the **UI** owns `auth.*` through Prisma. Postgres is the only external
dependency — point it at a cluster you already run and the marginal ops cost is roughly
zero (PLAN D2a; D2 covers why not SQLite).

**The engine is what must never fail, and it does not depend on the UI.** If the UI is
down, heartbeats are still recorded, monitors still go MISSING, and alerts still fire.

## Run it

```bash
make dev-up      # postgres + engine (:9090) + UI (:3100), no Docker
make dev-status  # what is listening, and is it healthy
```

Sign in at **http://localhost:3100**. Full instructions, including what to do when port
5432 is already taken, are in [DEVELOPING.md](DEVELOPING.md).

Or the engine alone:

```bash
./bettersentryio serve --database-url postgres://…/bettersentryio
```

## Reporting to it

```python
# errors: keep using the official SDK, just change the DSN
import sentry_sdk
sentry_sdk.init(dsn="http://<ingest key>@bettersentryio.internal:9090/1")
```

```python
# loop liveness: one line per iteration, downstream of the work
while True:
    batch = queue.get()
    process(batch)
    beat("tts-batcher", progress=batches_done)   # or a plain requests.post
```

Beating *before* the work, or from a separate timer, reports healthy through exactly the
failure you are trying to catch. [`examples/fastapi-tts`](examples/fastapi-tts) breaks
itself on purpose so you can watch `/health` answer 200 while the work has stopped;
[`examples/simple-fastapi`](examples/simple-fastapi) is the smallest error-only case.

## Credentials

Three, and they are not interchangeable:

| Credential | Made in | Can |
|---|---|---|
| **ingest key** | Apps → the app → Setup | write events and heartbeats for that app |
| **API token** (`bsiot_…`) | Settings → API tokens | read everything, change nothing — this is what a dashboard holds |
| **operator token** | `BSIO_API_TOKEN` in the engine's environment | everything, including deleting apps |

## Layout

| Path | What |
|---|---|
| `cmd/bettersentryio` | the binary: flags, wiring, graceful shutdown |
| `internal/api` | HTTP surface — ingest, native read API, Sentry Web API, tokens, alert config |
| `internal/events` | error events: fingerprinting, normalization, issue queries, Discover |
| `internal/monitor` | monitors, beats, absence and stall detection |
| `internal/alert` | delivery to chat tools, deduplicated in Postgres |
| `internal/store` | pool, migrations, bootstrap, channels, tokens |
| `internal/web` | the retired server-rendered UI, kept as a no-Node fallback |
| `clients/python` | a one-file, stdlib-only heartbeat client, served by the engine |
| `web/` | the operator UI (Next.js 16, Auth.js v5, Prisma, Tailwind v4) |
| `deploy/eks` | manifests: Argo CD, CNPG Postgres, sealed secrets, Image Updater |
| `docs/design`, `docs/research` | why the compatibility surfaces look the way they do |

## Tests

```bash
make check       # gofmt + go vet + go test
```

Tests that need Postgres read `BSIO_TEST_DATABASE_URL` and skip without it.
`./scripts/demo.sh` reproduces both original incidents end to end.
