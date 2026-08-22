# Developing bettersentryio

Status: **M0 + M1 + M3 built** (scaffold, Postgres store, loop liveness, stall
detection, alerting, and the web UI). M2 — Sentry-compatible error ingest — is next.
See [PLAN.md](PLAN.md).

## Two processes

Since PLAN **D11** the UI is the Scicom **Enterprise-Template** (Next.js 16, Auth.js v5,
Prisma, Tailwind v4, Radix), vendored into [`web/`](web/UPSTREAM.md). So there are two
app processes:

| | What it does | Depends on |
|---|---|---|
| **engine** (`cmd/bettersentryio`) | ingest, absence detection, alerting, JSON read API | Postgres `public` schema. One Go dependency. |
| **web** (`web/`) | operator UI, sign-in, RBAC | the engine's API + Postgres `auth` schema |

**The engine is what must never fail, and it does not depend on the UI.** If `web/` is
down, heartbeats are still recorded, monitors still go MISSING, and alerts still fire.

**One database, two schemas, strict ownership.** Prisma owns `auth.*`, the Go migrations
own `public.*`. This is not cosmetic: `prisma db push` treats its schema as exclusive and
will happily offer to drop every table it does not know about. Pointing it at `?schema=auth`
is what stops that. Never point Prisma at `public`.

### Running the UI

```bash
cd web
npm install
cp .env.example .env      # then set AUTH_SECRET, DATABASE_URL (?schema=auth), BSIO_API_*
npx prisma db push        # creates auth.* only
set -a; . ./.env; set +a  # db:seed runs under tsx, which does not read .env itself
npm run db:seed
PORT=3100 npm run dev     # 3000 is often taken
```

`.env` needs two bettersentryio-specific keys:

```
BSIO_API_URL=http://localhost:9090
BSIO_API_TOKEN=<the engine's operator token>   # server-side only, never sent to the browser
```

**Sign in with `admin@scicom.com.my` / `12345`** (Auth.js is email-keyed, so the username is
an email). Set `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` before seeding to change it. For
anything reachable by other people, configure **Entra ID** instead — the template already
wires it up, and SSO beats a shared password.

| Page | What it is for |
|---|---|
| `/monitors` | Monitors wall: state, last beat, progress, 1h activity, uptime over its observed window |
| `/monitors/{slug}` | One monitor: state with an explanation of *why*, 2h beat chart, incident history, config, mute |
| `/incidents` | Incident log. "Alerts delivered" is confirmed deliveries — `0 · retrying` on an open incident means it has not reached a channel yet |
| `/profile`, `/admin/*` | From the template: profile, users, roles, invitations |

### Design conventions worth keeping

Taken from how gpuplatform and SlurmUI actually use the template, which differs from the
bare template's defaults:

- **Dark is the default** and the accent flips: blue in light, Scicom orange in dark.
- **Semantic colour comes from `--status-active|idle|init|down`**, not Tailwind's
  `bg-green-100 text-green-800` idiom — those tint pairs do not survive a theme switch.
  Status chips are `bg-status-X/15 text-status-X` via `StatusPill`.
- **Do not use `--chart-*` for semantic meaning.** The chart ramp follows the dual-mode
  accent, so `--chart-2` is orange in dark mode; a "Healthy" figure painted with it reads
  as a warning. `StatCard`'s tone map uses the status tokens for exactly this reason.
- Mono (`JetBrains Mono`) with `tabular-nums` for every identifier, timestamp and figure.
- Logo is the company, the caption is the product: `SCICOM` + `BETTERSENTRYIO`.
- **Every volume chart is windowed, and the window lives in the URL.** `?range=`
  (default 30d) and `?interval=` (default auto) come from `@/lib/ranges`, and
  `OccurrenceChart` in `components/bsio/` is the one implementation — bars, a Y axis and
  a hover tooltip, stacked when there is more than one series. Because the window is a
  search param and not component state, the **list under a chart is filtered by the same
  window** (`getIssues(slug, { range })`), so the two cannot disagree, and a link carries
  the view.
- **Panel collapse is a cookie, not localStorage** (`@/lib/panel-state`). The layout is
  server-rendered, so the server has to know before it emits HTML; reading it in the
  browser paints the expanded sidebar and snaps it shut on hydration. `(app)/layout.tsx`
  reads it with `next/headers` and passes `initialCollapsed` down.

### The retired Go UI

The server-rendered UI still lives in `internal/web` and answers on the engine's port. It
is superseded and should be deleted once the Next UI has been through a week of real use —
it is kept for now only as a fallback that needs no Node.

## Prerequisites

- Go 1.23+
- PostgreSQL 15+ reachable locally

Nothing else. Postgres is the only external dependency (PLAN D2a).

## First run

```bash
make db          # creates bettersentryio_dev and bettersentryio_test
make run         # builds and serves on :9090
```

On first boot the server creates a `default` project, generates an ingest key, and
prints a ready-to-paste beat command. **The key is printed once** — subsequent
starts do not repeat it (it would end up in every log). To read it back:

```bash
psql bettersentryio_dev -tAc 'select public_key from ingest_keys limit 1'
```

### When 5432 is already taken

`./scripts/dev.sh` (`make dev-up`) talks to Postgres on 5432 by default. On a machine
where another project's container already holds that port, a hardcoded 5432 is worse
than an error: `pg_isready` succeeds, and every `psql`/`createdb` in the script then
runs against *their* database. Put the port in the gitignored `.env` and move the
cluster to match:

```bash
echo 'BSIO_PG_PORT=5434' >> .env
# and in the cluster: postgresql.conf -> port = 5434, then restart it
```

Both the engine and `web/.env`'s `DATABASE_URL` then follow that port.

## With Docker instead

Two options, both verified:

```bash
# Postgres only — run the app on the host with `make run` against it
docker compose -f docker-compose.dev.yml up -d
export BSIO_DATABASE_URL='postgres://bettersentryio:bettersentryio@localhost:5433/bettersentryio?sslmode=disable'

# Or the whole product in one command
docker compose -f docker-compose.dev.yml --profile app up --build
open http://localhost:9090/
```

Postgres is published on **5433** so it cannot collide with a Postgres already on
the host. Inside the compose network the app reaches it as `postgres:5432`.

The image is built from `scratch` — the binary plus CA certificates (needed for
HTTPS webhook delivery) and nothing else. No shell, no package manager, no base OS
to patch; it runs as uid 65532. About 17 MB.

```bash
docker build -t bettersentryio:dev .
docker run --rm bettersentryio:dev version
```

When alerting to a sink running on your host from inside a container, use
`http://host.docker.internal:9099/hook` — `localhost` there is the container.

## Adding an app

This is the path a service owner takes, and the one to keep working:

1. **Apps → + Add app**, name it (`TTS API` → slug `tts-api`).
2. The setup page shows **that app's own ingest key**, a snippet already filled in
   with it, and a live check that waits for the first heartbeat.
3. Paste the snippet into the service. The monitors it beats appear under the app
   within ~3 seconds — nothing else is registered by hand.

Two properties are load-bearing:

- **Each app has its own key.** A leaked or rotated key affects one service, not the
  estate. Keys live in `ingest_keys`, one row per app, minted on create.
- **Only the app is declared.** Monitors still create themselves on first beat, so an
  engineer never waits on config to see their loop appear.

Equivalent from the shell, if you are testing the engine without the UI:

```bash
curl -sX POST localhost:9090/api/0/apps \
  -H "X-BSIO-Key: $ANY_EXISTING_KEY" -H 'Content-Type: application/json' \
  -d '{"name":"TTS API"}'
# -> {"slug":"tts-api","name":"TTS API","ingest_key":"…"}
```

The engine serves the Python client too, so a service can fetch it from the engine it
reports to and the two never drift:

```bash
curl -O localhost:9090/clients/python/bettersentryio.py
```

## Sending a heartbeat

A monitor is created by its first beat — there is nothing to configure up front.

```bash
KEY=$(psql bettersentryio_dev -tAc 'select public_key from ingest_keys limit 1')

# Liveness only: "this loop should beat every 30s, allow 60s of slack"
curl -fsS "localhost:9090/api/0/beat/tts-batcher?key=$KEY&every=30&grace=60"

# Liveness + progress: pass a counter that only ever goes up. A loop that keeps
# beating while the counter sits still is STALLED, which liveness alone misses.
curl -fsS "localhost:9090/api/0/beat/tts-batcher?key=$KEY&every=30&progress=$BATCHES_DONE"
```

| Parameter | Meaning |
|---|---|
| `key` | ingest key (or send the `X-BSIO-Key` header) |
| `every` | seconds between expected beats |
| `grace` | extra seconds before MISSING fires (default: `every`, min 30s) |
| `stall_window` | seconds of frozen progress before STALLED fires (default `3 × every`, min 120s; `-1` disables) |
| `progress` | monotonic counter — batches, tokens, rows, anything that only grows |
| `env` | environment name (default `production`) |

From Python, use the client in `clients/python/` — one stdlib-only file. It never
raises and never blocks the caller, which matters because this call sits inside the
loop you are trying to protect: a monitoring call that can throw or hang turns an
observability problem into an outage.

```python
from bettersentryio import Beat

bsio = Beat(base_url="http://localhost:9090", key=KEY)

while True:
    batch = queue.get()
    process(batch)
    batches_done += 1
    bsio.beat("tts-batcher", progress=batches_done, every=30)
```

## Watching it

- `http://localhost:3100/apps` — the project list; picking one opens its views in a third column
- `http://localhost:3100/apps/<slug>/issues/outages` — MISSING loops in that project
- `http://localhost:3100/apps/<slug>/issues/breached` — STALLED loops (beating, no progress)
- `http://localhost:3100/apps/<slug>/issues/warnings` — LATE loops, still inside grace
- `http://localhost:3100/apps/<slug>/setup` — its snippets, filled in with its key
- `http://localhost:3100/monitors` — every monitor across every project
- `http://localhost:3100/learn` — **How it works**: which SDK argument produces which Issues view,
  how to pick the numbers, and the placement mistakes that report healthy through a real failure.
  Each issue view deep-links into it (`/learn#breached`), so keep the anchor ids in step with
  `IssueViewId`.
- `http://localhost:9090/` — the retired Go-rendered wall, kept as a Node-free fallback
- `http://localhost:9090/api/0/monitors` — the same data as JSON
- `http://localhost:9090/api/0/apps` — apps with their keys and monitor counts
- `http://localhost:9090/-/health` — **our own loop ages**. Returns 503 when the
  detector is stale or its sweeps are failing, so this service cannot repeat the
  green-health-check failure that motivated the project.

### Windowed reads in the engine

Two endpoints back the charts, and both take Sentry's window parameters
(`statsPeriod=30d`, or `start`/`end`, plus an optional `interval`):

| Endpoint | Answers |
|---|---|
| `GET /api/0/issues/{id}/series` | one issue's occurrences per bucket |
| `GET /api/0/apps/{slug}/series` | an app's events per bucket, split by level |

Read requests authenticate with an API token (`Authorization: Bearer bsiot_…`), the
operator token, or an ingest key. Tokens are managed at `/admin/tokens` and stored as a
SHA-256 — `GET/POST /api/0/tokens` and `DELETE /api/0/tokens/{id}`, all of which need
the operator token or a session, because a read credential must not be able to mint
another one.

`GET /api/0/issues?project=…` accepts `statsPeriod` too, and **`events.Store.Issues`
grew a `since *time.Time` parameter** to serve it — a signature change worth knowing
about before you add the next caller. Buckets are zero-filled and epoch-aligned; see
the interval note in [docs/design/grafana-datasource.md](docs/design/grafana-datasource.md)
for why that alignment is load-bearing.

## Dashboards: Grafana, with no plugin of ours

The engine answers Sentry's Web API, so Grafana Labs' own
`grafana-sentry-datasource` queries it unmodified — the read-side version of the
bargain D14 struck for the SDKs.

```bash
set -a; . .env; set +a
cd examples/grafana && docker compose up -d
open http://localhost:3020          # admin / admin, datasource + dashboard provisioned
```

Give the datasource a token from **Settings → API tokens** (`/admin/tokens`): `bsiot_…`,
read-only, revocable on its own, and it records when it was last used. The operator token
also works and can delete apps, which is exactly why a dashboard should not hold it.
Details, the supported query types and the traps (`localhost` inside the container is
Grafana; Sentry reports counts as strings) are in
[docs/design/grafana-datasource.md](docs/design/grafana-datasource.md).

## Alerts

One flag registers a channel:

```bash
make build
bin/bettersentryio serve --database-url "$BSIO_DATABASE_URL" \
  --alert-webhook 'https://hooks.slack.com/services/...' --alert-type slack
```

`--alert-type` is `webhook` (generic JSON), `slack`, or `teams`. Telegram needs
`bot_token` and `chat_id`, so insert it directly:

```sql
insert into channels (name, type, config) values
  ('oncall-tg', 'telegram', '{"bot_token":"123:abc","chat_id":"-1001234567890"}');
```

For local development, `scripts/webhook-sink.py` prints whatever arrives.

## The demo

```bash
make demo
```

Reproduces both motivating incidents in about a minute: a loop that dies while
its health check stays green, and a loop that keeps beating while doing no work.

## Tests

```bash
make test
```

Tests run against a real Postgres because most of the state machine is SQL —
partial-index sweeps and `on conflict` upserts cannot be meaningfully mocked. They
truncate their tables on every run, so point `BSIO_TEST_DATABASE_URL` somewhere
disposable. The engine's clock is injectable (`Engine.SetClock`) so "beats arriving
on time while progress is frozen" can be simulated without waiting in real time.

## Layout

```
cmd/bettersentryio/     flags, wiring, graceful shutdown
internal/store/         pool, embedded migrations, bootstrap
internal/monitor/       beat handling (arrival transitions) + detector (absence transitions)
internal/alert/         dedup ledger + channel formatting
internal/api/           beat endpoint, health, apps, read API for the UI
clients/                the SDKs, embedded so the engine can serve them
clients/python/         stdlib-only heartbeat client
web/                    the Next.js UI (vendored Enterprise-Template — see web/UPSTREAM.md)
scripts/                demo.sh, webhook-sink.py, monitors.py
```

Two rules worth knowing before changing the state machine:

1. **Arrival transitions live in the engine, absence transitions in the detector.**
   Recovery is immediate on a beat; MISSING and STALLED can only be discovered by
   a sweep.
2. **A beat proves the loop is alive, not that it is working.** It clears MISSING.
   Only a moving progress counter clears STALLED — otherwise a stalled monitor
   flaps between alert and false all-clear on every heartbeat. There is a
   regression test for exactly this.
3. **Navigation is three columns then content** (PLAN D13), declared in `web/src/lib/nav.ts`:
   `rail.tsx` (sections) → `nav-panel.tsx` (the section's contents; for Projects, the project
   list) → `project-panel.tsx` (the selected project's views, only rendered when a slug is in
   the path). Columns 2 and 3 collapse independently, each to a 48px icon rail via the shared
   `IconRail`/`IconLink` in `nav-panel.tsx` — collapsing narrows a column, it does not hide where
   things are. Adding a project view means adding it to
   `projectNav()` and, for an issue view, to `ISSUE_VIEWS` in `web/src/lib/issues.ts`, which maps
   it onto the monitor states and incident kinds it covers. There is deliberately no project
   dropdown and no global issue list — both were built and rejected (see D13's alternatives).
4. **Durations come from the engine, never the browser.** `open_incident_secs` is measured in
   Go because the engine's clock is the one detection uses; computing "for 27s" from `Date.now()`
   in the UI would drift against it and breaks hydration. Relative times that are purely
   cosmetic go through `web/src/components/bsio/time.tsx`, which marks the drift as expected.
5. **Name → slug happens in exactly one place**, `store.Slugify`. The Add-app dialog
   previews the slug client-side with a mirror of it in `web/src/lib/bsio.ts`; if you
   change one, change both, or the preview starts lying about the URL you will get.
