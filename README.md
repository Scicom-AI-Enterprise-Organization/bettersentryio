# bettersentryio

**Single-binary error tracking + background-loop liveness monitoring.**
The 5% of Sentry a small team actually needs, without the 42-process fleet.

> Product = repo = binary: **bettersentryio** (naming decision D9 in PLAN.md).
> Status: **design phase — plan under review.** See [PLAN.md](PLAN.md) and
> [ARCHITECTURE.md](ARCHITECTURE.md). Nothing runs yet; the docs describe the target.

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

## What it will do

- **Error tracking** — point your existing `sentry-sdk` DSN at bettersentryio; events are grouped
  into issues (Sentry-inspired fingerprinting), with a small web UI and webhook alerts.
- **Loop liveness (the flagship)** — your loop sends a one-line heartbeat per iteration.
  No beat within the window → alert. Sentry-Crons-compatible check-ins also accepted, so
  `@sentry_sdk.crons.monitor` works unmodified.
- **Stall detection (beyond Sentry)** — heartbeats can carry a monotonic progress counter
  (batches processed, tokens generated). Beats arriving but the counter frozen → **stalled**
  alert. This is the vLLM case: alive, looping, doing nothing.
- **Alerting** — webhooks (generic JSON, Slack, Microsoft Teams, Telegram), with dedup,
  cooldown, and recovery notices.

## What it will never do

Tracing/APM, session replay, profiling, releases health, dashboards, orgs/teams/SSO,
multi-region anything. If we need those we'll buy them.

## Target shape

```
┌────────────────────────── one Go binary ──────────────────────────┐
│  ingest (Sentry envelope + native API) → store (PostgreSQL)       │
│  detector (1 ticker) → alerter (webhooks) → embedded web UI       │
└───────────────────────────────────────────────────────────────────┘
   1 stateless binary · 1 Postgres DB · <50 MB RAM idle · no queue
```

Postgres is the **only** external dependency — point it at a cluster you already run and the
marginal ops cost is roughly zero. See PLAN.md D2 for why not SQLite (short version: partitioned
retention, real PITR backups, and the option of >1 replica later).

## Intended usage (once built)

```bash
./bettersentryio serve --database-url postgres://…/bettersentryio   # that's the whole deployment
```

```python
# errors: keep using the official SDK, just change the DSN
import sentry_sdk
sentry_sdk.init(dsn="http://<key>@bettersentryio.internal:9090/1")
```

```python
# loop liveness: one line per iteration
while True:
    batch = queue.get()
    process(batch)
    beat("tts-batcher", progress=batches_done)   # tiny helper, or plain requests.post
```

## Repo layout

```
PLAN.md            — goals, non-goals, decisions, milestones  ← start here
ARCHITECTURE.md    — components, data model, wire protocol, detection algorithms, diagrams
docs/design/       — kubernetes.md: Helm vs operator vs CRDs decision + CRD sketches
docs/research/     — notes from reading the Sentry source (grouping, crons, ingest, bloat)
```
