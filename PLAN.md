# bettersentryio — Build Plan

> Companion to [ARCHITECTURE.md](ARCHITECTURE.md). Research grounding in [docs/research/](docs/research/).
> Written 2026-08-10, before any code. Review this, veto decisions, then we build.

## 1. Problem statement

We need to know, within a minute or two, when:

1. a service **throws** — exceptions in our Python AI services (TTS API, vLLM serving, batch
   pipelines) should be captured, deduplicated, and alerted on;
2. a service **silently stops working** — the TTS incident: `/health` 200 for two days while the
   internal PyTorch batching loop was dead. No exception ever escaped to be captured;
3. a service **runs but does nothing** — the vLLM `torch.compile` shape-mismatch "super
   silence": loop alive, output effectively zero, no error raised.

Case 1 is classic error tracking. Cases 2 and 3 are *liveness* and *progress* — invisible to
health checks (outside-in probes hit the HTTP layer, not the work loop) and invisible to error
tracking (nothing throws). The fix is inside-out signals: the loop itself must emit proof of life
(heartbeat) and proof of work (progress counter), and something must alert when they stop.

## 2. Goals

| # | Goal | Acceptance |
|---|---|---|
| G1 | Detect a dead background loop | Kill a test loop → webhook alert within its grace window (≤2 min for a 30 s loop) |
| G2 | Detect a stalled-but-beating loop | Freeze the progress counter → **stalled** alert within N windows |
| G3 | Drop-in error capture | Unmodified `sentry-sdk` (Python first) pointed at a bettersentryio DSN: `capture_exception` produces a grouped issue |
| G4 | Drop-in cron check-ins | Unmodified `sentry_sdk.crons.monitor` decorator works (upsert + in_progress + ok) |
| G5 | Trivial ops | 1 binary + 1 Postgres database (reuse an existing cluster if we have one) and nothing else — no queue, no cache, no second engine; deploy = replace the image; restore = `pg_dump`/PITR; app idle RSS < 50 MB; binary < 20 MB |
| G6 | Alerts people actually see | Slack / Microsoft Teams / Telegram / generic webhook, with dedup + cooldown + recovery notices |

Capacity target (deliberately modest, honestly stated): ≥ 200 events/s sustained on a 2-vCPU VM
(plus Postgres), p99 ingest < 20 ms, and — with partitioned retention doing the pruning — comfortable
into the hundreds of GB rather than SQLite's ~50 GB. That is ~100× our current need.

## 3. Non-goals (permanent, not "later")

Tracing/APM, session replay, profiling, release health, metrics product, dashboards,
source maps / symbolication, orgs/teams/RBAC/SSO (one admin login + per-project keys only),
Kafka-scale ingestion. Multi-replica HA is **out of scope for v1** but no longer architecturally
foreclosed — D2 (Postgres) makes it a later config change rather than a rewrite. **Feature requests
that add a second storage engine, a queue, or any third moving part are rejected by default
(D2a).** That discipline is the product.

## 4. Why not an existing tool

Numbers measured from source where possible — see [docs/research/sentry-bloat-inventory.md](docs/research/sentry-bloat-inventory.md).

| Tool | Errors | Loop liveness | Stall detection | Footprint | Verdict |
|---|---|---|---|---|---|
| Sentry self-hosted | ✅ best-in-class | ✅ Crons | ❌ | 42 processes, 6 storage engines, 12 repos, ~766k LOC Python | Operationally absurd for one team |
| GlitchTip | ✅ (Sentry-SDK-compatible) | ❌ (uptime pings only, outside-in) | ❌ | Django + Postgres + Redis + workers | Closest incumbent; misses the actual incident class |
| Bugsink | ✅ (Sentry-SDK-compatible) | ❌ | ❌ | 1 container, SQLite | Great at errors-only; deliberately nothing else; source-available license |
| Healthchecks.io | ❌ | ✅ (cron pings) | ❌ | Django + Postgres | The other half of the problem; no error capture, minute-granularity crons |
| Grafana Faro + LGTM | browser-only SDK | ❌ | ❌ | Alloy+Loki+Tempo+Grafana | Frontend RUM; irrelevant to backend Python loops |
| Uptime-Kuma etc. | ❌ | outside-in probes only | ❌ | 1 container | Probes the HTTP layer — exactly what fooled us for 2 days |

The gap: **errors + inside-out liveness + progress, in one lightweight deployable.** Nothing
occupies it. (If we ever decide we want errors-only, Bugsink already exists — bettersentryio earns its
keep on G1/G2 or not at all.)

## 5. Decisions

| # | Decision | Rationale | Rejected alternative |
|---|---|---|---|
| D1 | **Go** | Single static binary (`CGO_ENABLED=0`), goroutines/`time.Ticker` map 1:1 onto ingest/detector/alerter loops, `html/template` + `go:embed` eliminates the Node toolchain, fastest path to done, easiest for a Python-first team to maintain. | **Rust** — smaller RSS, no GC; rejected for v0 velocity, not on merit. Architecture is language-agnostic; **veto here if you want Rust**, cost ≈ +50% build time. |
| D2 | **PostgreSQL 15+ via `pgx/v5`** (pure Go — `CGO_ENABLED=0` still holds) | Ariff's call, 2026-08-10. One real database we already know how to operate, and it buys things a file cannot: native **range partitioning** turns retention into `DROP TABLE partition` instead of a `DELETE` scan; `pg_dump` + WAL archiving/PITR (or the managed cluster's snapshots) is a real DR story instead of "hope the file copy was consistent"; `INSERT … ON CONFLICT` makes group-hash upserts atomic under concurrency; `tsvector` + GIN gives better message search than SQLite's FTS5; **partial indexes** cover exactly the detector's two scan predicates; and `pg_try_advisory_lock` lets us run more than one replica later (rolling updates, k8s HA) — permanently impossible with a single-writer file. | **SQLite (WAL)** — the previous default, and genuinely lighter: zero ops, backup = copy one file. Rejected because it caps us at one writer process forever, has no replication, and forces `replicas: 1 + strategy: Recreate` in k8s. **Not kept as a second dialect**: supporting two SQL dialects doubles the store code and the test matrix for a team of our size. |
| D2a | Accept **exactly one** external dependency — Postgres — and nothing else. No queue, no cache tier, no second engine, no search cluster. | D2 is the single place we spend the "one process, no dependencies" budget. Everything else stays in-binary, so the deployment is *two* things rather than 42. | A second dependency for any reason is a PLAN §3 non-goal, not a trade-off to weigh. |
| D3 | **Speak Sentry's wire protocol for ingest** (envelope endpoint: `event` + `check_in` items), plus a **native one-line beat API** | Compat = zero client migration (G3/G4); native API = `curl`-able heartbeats with progress counters, no SDK needed in shell scripts. | Custom-protocol-only — would orphan every existing SDK integration. |
| D4 | Monitor kinds: **`cron`** (Sentry-Crons-compatible, minute granularity) and **`loop`** (bettersentryio-native: `expected_every` seconds + grace + optional progress-stall detection, 15 s detector tick) | Crons semantics are proven and SDK-supported (see [research notes](docs/research/sentry-crons-semantics.md)); loops need sub-minute response and progress semantics Sentry lacks. | Loops-as-fast-crons — minute floor is too slow for a 5 s batching loop, and no stall concept. |
| ~~D5~~ | ~~UI: **server-rendered `html/template` + htmx**, embedded via `go:embed`~~ | ~~No SPA, no node_modules, no build step.~~ | **SUPERSEDED by D11.** |
| D11 | UI: **the Scicom [Enterprise-Template](https://github.com/Scicom-AI-Enterprise-Organization/Enterprise-Template)** (Next.js 16 + Auth.js v5 + Prisma + Tailwind v4 + Radix), vendored into `web/` | Ariff's call, 2026-08-10 — internal tools should look and log in like the rest of the estate. The template is the shared ancestor of gpuplatform and SlurmUI (its own HEAD commit reads *"standardize with gpuplatform, label platform and slurmui"*), so this buys the house design language, the sidebar shell, **and real SSO** (Entra ID / Google / Keycloak / SAML) plus RBAC — none of which a hand-rolled admin password gives us. | **Reimplementing the look in CSS** — visually close, no new dependencies, but no component reuse, no SSO, no RBAC, and it drifts from upstream the day the template changes. Rejected once the template turned out to be an app scaffold rather than a theme. |
| D11a | Accept the honest cost of D11: **two app processes** (Go engine + Next UI), `node_modules` (~767 MB), and Node in the build. The engine stays dependency-free and independently deployable. | Still three moving parts against Sentry's 42, and the part that must never fail — ingest, detection, alerting — remains a single static Go binary with one dependency. The UI can be down while monitoring continues. | Embedding a static export in the Go binary — impossible here, since the template relies on server-side auth, server actions and Prisma. |
| D12 | **Apps are first-class and self-serve.** A service (`tts-api`, `vllm-serving`) is created from the UI with **+ Add app**, gets **its own ingest key**, and its monitors group under it. Onboarding is three steps on one page: the key, a filled-in snippet, and a live "has it reported yet" check. | Ariff's call, 2026-08-10 — the UI had no answer to "how do I add my FastAPI service?". It also fixes a real weakness: one shared key for the whole estate meant a leak or a rotation hit everything. Per-app keys scope the blast radius, and grouping makes "which service is this loop in?" answerable. Registration stays beat-first — the app is the only thing you declare; monitors still create themselves. | **A config file or CRD per app** — right for GitOps later (D10), wrong as the only path: it puts a merge request between an engineer and their first heartbeat. **Keeping one global key** — simpler, but unrotatable in practice. |
| D12a | The engine **serves its own SDK** at `GET /clients/python/bettersentryio.py` (embedded via `go:embed`, unauthenticated). | The setup page tells you to curl the client off the engine you are about to report to, so the served client and the engine are always the same version — no second artifact to publish. Unauthenticated because it is public source with no secrets, and needing a key to fetch the thing that uses the key is circular. | Publishing to an internal PyPI — more infrastructure, more drift, for one stdlib-only file. |
| D13 | **Three nav columns, then content.** A rail of sections (Projects / Monitors / Settings); the section's contents — for Projects, the project list, which **stays put**; and when a project is selected, a third column holding its views (Errors & Outages / Breached Metrics / Warnings / Setup). Columns 2 and 3 each collapse to an **icon rail** — still navigable, just narrower. Issues are not a rail entry: an issue is always an issue *in* something. | Ariff's call, 2026-08-11. Keeping the project list visible means switching project is one click from inside any view, with no back step — which is the thing a dropdown was trying to solve, done as navigation instead of as a widget. Collapsing exists because three nav columns plus content is a lot of width on a laptop; a collapsed column keeps its icons (project marks with a status ring, view icons) so it stays usable rather than becoming a bare toggle. | **A dropdown filter in the page header** with Projects under Settings — the main axis became a widget inside a page *and* a settings entry. **A drilling panel** that replaced the project list with the project's views — one column narrower, but every project switch cost a trip back. **A tab strip on the project page** — duplicates column 3 in a second shape. |
| D13a | The three Issues views map onto what the engine actually detects: **Errors & Outages** = `MISSING` (the loop stopped), **Breached Metrics** = `STALLED` (progress froze), **Warnings** = `LATE` (overdue, inside grace). | Each demands a different response — page someone, page someone about a subtler thing, or just watch — so they are separate lists rather than one severity column. The middle one is why this project exists. | One "Issues" list with a kind column — cheaper, but buries the stall case that no other tool surfaces. |
| D6 | Alerts: **outbound webhooks first** (generic JSON, Slack, MS Teams, Telegram), SMTP later | Webhooks cover every chat tool we use; SMTP adds config surface for little gain in v1. | Full notification platform — no. |
| D7 | Auth: per-project **ingest keys** (DSN-embedded, constant-time compare) + single **admin password** for the UI (session cookie). TLS via reverse proxy. | Matches single-tenant internal reality. | Users/teams/SSO — non-goal. |
| D8 | Single node, single tenant. Scale ceiling stated in README. | Our fleet is ~10 services. | Distributed anything. |
| D9 | **Name: `bettersentryio`** — product = repo = binary (Ariff's call, 2026-08-10). Retires the earlier working name "SentraIO", which collided with sentra.io, an existing security company. K8s API group under our own domain: `bettersentryio.scicom.com.my`. | Zero ambiguity between repo, binary, and docs. | — |
| D10 | **Kubernetes: Helm chart yes, operator no; CRDs later via the Traefik pattern** — the same binary optionally watches `Monitor`/`AlertChannel`/`Project` CRs with `--kubernetes` (embedded controller-runtime, status writeback → `kubectl get monitors` shows liveness). Full reasoning + CRD sketches: [docs/design/kubernetes.md](docs/design/kubernetes.md). | An operator encodes complex day-2 ops; bettersentryio has none (1 pod, 1 file). CRDs still buy GitOps config next to workloads — without a second deployment. k8s stays an optional target; bare-metal GPU boxes are first-class. | Separate operator deployment / OLM — automation with nothing to automate. |

## 6. Milestones

Ordered **pain-first**: heartbeats before error ingest, because G1/G2 are why this project exists.

### M0 — Skeleton (≈ half a day)
Repo scaffold (`cmd/bettersentryio`, internal packages), config via flags+env, structured logging,
Postgres store via `pgx/v5` with embedded migrations (`go:embed` + a `schema_migrations` table),
`docker-compose.dev.yml` for a local Postgres, `go test` CI (integration tests against a real
Postgres container), `--version`. Startup retries the DB with backoff instead of crash-looping.
**Done when:** binary serves `/-/health` and creates its schema on first run.

### M1 — Loop liveness + alerting (the reason we're here; ≈ 2–3 days)
Native beat API (`POST /api/0/beat/<monitor>` with optional `progress` counter and auto-create),
monitor registry, detector goroutine (15 s tick; states OK → LATE → **MISSING** → RECOVERED,
plus **STALLED** on frozen progress), alerter with per-monitor dedup/cooldown/recovery notices,
generic + Slack + Teams + Telegram webhook formats, minimal `monitors.json` admin API.
**Done when (G1):** a `while true; do curl …; sleep 5; done` loop killed mid-run produces a Teams/Slack
alert within grace, and a recovery notice on resume. **(G2):** beats with a frozen counter trip STALLED.
Detector fully unit-tested against a fake clock.

### M2 — Sentry-compatible ingest: errors + check-ins (≈ 3–4 days)
Envelope endpoint (`POST /api/<project_id>/envelope/`, `X-Sentry-Auth`/`sentry_key` auth,
gzip/zstd — exact contract per [ingest research](docs/research/sentry-ingest-protocol.md)),
accept `event` + `check_in` items, **accept-and-drop everything else** (sessions, transactions,
client reports) so SDKs never error; store events; grouping per
[grouping research](docs/research/sentry-grouping.md) (fingerprint → md5 of type+frames /
parameterized message; issues with first_seen/last_seen/count; regression = event after resolve
→ reopen); `check_in` items route into the M1 monitor engine (Crons semantics: two-phase
check-ins, `monitor_config` upsert, margin/max_runtime/timeout re-arm); issue-alert webhooks
(new issue / regression, rate-capped).
**Done when (G3):** unmodified `sentry-sdk` + `1/0` → one grouped issue; run twice → count=2, no
new issue. **(G4):** `@sentry_sdk.crons.monitor(monitor_slug=…)` round-trips against bettersentryio.
Golden-envelope fixtures captured from real SDKs live in `testdata/`.

### M3 — Web UI ✅ **built** (ahead of M2)
Delivered: session login (`admin`/`12345` default, warned about in the log and on every
page), monitors wall with status pills / uptime / activity sparklines, monitor detail
with a 2h beat chart and incident history, incident log showing confirmed deliveries,
settings with ingest keys and redacted channel targets. Dark single theme, embedded
via `go:embed`, no build step. Error-tracking pages (issue list, stacktrace) wait on M2.

<details><summary>original scope</summary>
Issues list (open/resolved, sort by last_seen/count) → issue detail (message, tags, stacktrace
with in-app highlighting, occurrence sparkline, resolve/mute) → monitors wall (green/red tiles,
last beat, uptime %) → monitor detail (beat/check-in timeline, incident history) → project +
channel settings pages. Embedded, no build step.
**Done when:** the TTS incident post-mortem could have been driven entirely from these pages.
</details>

### M4 — Hardening + ops (≈ 2 days)
Retention janitor (defaults: events 90 d, check-ins/beats 14 d raw + daily rollups kept 365 d) —
implemented as **partition create/drop**, not row deletes; per-key token-bucket rate limits with
proper 429 + `Retry-After`; payload size caps; graceful shutdown; `/-/metrics` (Prometheus text);
~~Dockerfile (scratch, non-root)~~ **done early in M1** — 17 MB `scratch` image, uid 65532,
verified against the compose Postgres; + systemd unit + **Helm chart** (stateless pod, no PVC, rolling
updates safe — see [docs/design/kubernetes.md](docs/design/kubernetes.md)) + backup/restore doc
(`pg_dump` + PITR); DB-outage soak test (kill Postgres mid-ingest → shed with 429, reconnect, no
partial batches thanks to transactional writes); load test proving §2 targets.
**Done when:** `docker run -v data:/data bettersentryio` is the entire production deployment guide.

### M5 — Stretch (only if earned)
SMTP alerts; per-monitor maintenance windows; Sentry `session` items → crash-free rate;
CSV/JSON export; read-only dashboard token; **multi-replica HA** (N stateless pods + advisory-locked
detector — unlocked by D2, config change only);
**`--kubernetes` CRD watch mode** (D10: `Monitor`/`AlertChannel` CRs, status writeback) and the
k8s event watcher (OOMKilled/CrashLoopBackOff → issues) — both per [docs/design/kubernetes.md](docs/design/kubernetes.md).

**Total to usable (M0–M4): roughly two working weeks.** LOC budget ≤ ~8k Go excluding tests
(ingest 1.5k, store 1.5k, detect 0.8k, alert 0.8k, ui 2k, glue 0.5k) — enforced in review; the
budget is the anti-bloat mechanism.

## 7. Testing strategy

- **Golden envelopes**: capture real `sentry-sdk` output (Python 2.x SDK line first, then JS/Go)
  into `testdata/envelopes/`; ingest tests replay them byte-for-byte. Compat is tested against
  artifacts, not documentation.
- **Detector simulation**: fake clock; property-style tests for schedule math (misses never
  double-fire; synthetic misses never advance `last_beat`; re-anchoring prevents drift — the
  invariants Sentry encodes, per research notes).
- **Alert dedup**: storm scenario (1000 identical failures → 1 alert + cooldown behavior).
- **Crash safety**: kill -9 during ingest; reopen; no corruption, ≤ 1 batch lost.
- **End-to-end**: docker-compose test rig with a real Python process using sentry-sdk, killed and
  resumed, asserting webhook payloads received by a stub server.

## 7a. Measured limits

Stress-tested against `examples/fastapi-tts` on 2026-08-10 with `scripts/stress.py`. Full
numbers in that example's README; the boundaries it established:

| Failure | Detected | Note |
|---|---|---|
| Loop killed by an unhandled exception | **Yes, ~20s** | And the exception was **never logged** — asyncio only surfaces it on GC, and the task stays referenced. Log scraping misses this entirely. |
| Loop alive, progress frozen | **Yes, ~30s** | The motivating case. |
| Request-path errors (1737 × HTTP 500) | **No** | Heartbeats watch the loop, not handlers. This is what M2 (error ingest) is for; the "Errors & Outages" view only covers *outages* until then. |
| Severe degradation (10× throughput loss, p95 → 10s) | **Partly** | The loop kept limping so progress kept moving; only a brief `late`. Detecting this needs a **progress-rate threshold** ("fewer than N per minute"), which is not built. |

Two consequences for the roadmap: M2 is not optional if the Errors view is to mean what its name
says, and a rate-based detector is worth adding beside the absence-based ones, since "slow enough
to be useless" is a real production state that neither MISSING nor STALLED covers.

## 8. Risks

| Risk | Mitigation |
|---|---|
| SDK envelope drift across sentry-sdk versions | Test against pinned SDK versions via golden fixtures; accept-and-drop unknown item types so new SDKs never hard-fail; envelope parser tolerant by design |
| **Postgres is now a hard dependency** — if it's down, ingest stops | Bounded channel absorbs short blips; beyond that shed at the source with `429 + Retry-After` (SDKs retry/buffer); reconnect with backoff; `/-/health` reports `degraded` rather than crash-looping; alerting for monitors already in an open incident still fires from in-memory state |
| Connection-pool exhaustion / too many replicas × pool size | `pgxpool` with a small fixed cap (≈10) sized against the DB's `max_connections`; the writer is one goroutine, so ingest needs ~1 connection under load; pool saturation exposed on `/-/metrics` |
| Write throughput under bursts | Single writer goroutine + batched transactions (`pgx CopyFrom`); ingest path never blocks on a per-event round trip; back-pressure via bounded channel + 429 |
| Alert storms (mass failure → spam) | Per-monitor and per-issue dedup keys, cooldown, global rate cap, digest rollup ("14 monitors missing") when > N alerts/min |
| Detector wall-clock skew / host sleep | Monotonic tick; on large clock jumps (resume from suspend) re-anchor instead of firing a backlog of false misses |
| Beat traffic self-DoS (a 5 ms loop beating per iteration) | Server-side min-interval coalescing (a beat updates state; storage rollup per window); document "beat ≤ 1/s" |
| Scope creep back into Sentry | §3 non-goals + LOC budget are review-blocking rules, not vibes |

## 9. Open questions (answer before/at M0)

1. **Language veto (D1):** Go is the default. Say "Rust" now if you feel strongly — after M1 it's expensive to flip.
2. **Alert channel priority (D6):** which do we wire first — Teams? Slack? Telegram?
3. **Deployment target + which Postgres (D2):** which VM/host or cluster runs the binary, and does it point at an **existing shared Postgres** (cheapest — inherits your backup/monitoring) or a **dedicated instance** (isolated blast radius)? Version ≥ 15 assumed for partitioning ergonomics.
4. **Retention defaults (§M4):** 90 d events OK, or shorter for the GPU boxes' disk budget?
5. **Project granularity:** one bettersentryio project per service (tts-api, vllm-serving, pipelines…) is the assumption.
6. **Kubernetes (D10):** do the TTS/vLLM workloads actually run on k8s today, or on bare-metal/VMs? Decides whether the Helm chart is enough (M4) and whether the CRD watch mode ever gets built (M5).
