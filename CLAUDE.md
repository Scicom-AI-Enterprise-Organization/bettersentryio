# Working in this repo

Orientation for a fresh session. [README.md](README.md) is what the product is,
[DEVELOPING.md](DEVELOPING.md) is how to run it, [PLAN.md](PLAN.md) holds the numbered
decisions (referenced as D2, D9, D11, D14 throughout the code). This file is the part
that bites you if nobody says it.

## Run and check

```bash
make dev-up       # postgres + engine (:9090) + UI (:3100), no Docker
make dev-status   # what is listening, and is it healthy
make dev-down     # stops the engine and the UI; Postgres keeps running
make check        # gofmt -w + go vet + go test
```

- **Postgres port.** `scripts/dev.sh` defaults to 5432 but honours `BSIO_PG_PORT` from
  the gitignored `.env`. Set it when another project's container already holds 5432 —
  a hardcoded port there does not error, it silently runs every `psql`/`createdb`
  against *their* database.
- **The UI hot-reloads** (`next dev`). `BSIO_WEB_MODE=start ./scripts/dev.sh up` serves
  a production build instead, and then needs `npm run build` in `web/` first.
- **Postgres-backed tests** read `BSIO_TEST_DATABASE_URL` and `t.Skip` without it, so a
  green `go test` does not always mean the SQL ran. Set it before trusting a state-machine
  change.
- Browser-driving a page under `next dev`: the route compiles and hydrates on first hit,
  so filling a form immediately after `domcontentloaded` submits into a dead handler and
  lands back on `/login` with a 200. Wait for the field to be visible, then a beat more.

## Two processes, one database, two schemas

| | Owns | Depends on |
|---|---|---|
| **engine** (`cmd/bettersentryio`) | `public.*`, via its own embedded migrations | Postgres. One Go dependency. |
| **web** (`web/`) | `auth.*`, via Prisma | the engine's API + Postgres |

**The engine is what must never fail, and it does not depend on the UI.** Nothing in
`internal/` may require `web/` to be up.

**Never point Prisma at `public`.** `prisma db push` treats its schema as exclusive and
will offer to drop every table it does not know about. The `?schema=auth` in
`web/.env`'s `DATABASE_URL` is what stops that.

Migration numbering is sequential and shared — `ls internal/store/migrations` before
claiming a number, because a peer may be holding the next one.

## Credentials

Three, not interchangeable. Getting this wrong is how a dashboard ends up able to delete
apps.

| Credential | Shape | Can |
|---|---|---|
| ingest key | 32 hex, stored in plaintext (it is embedded in clients by design) | write events and heartbeats for one app |
| **API token** | `bsiot_…`, stored as SHA-256 | read everything, change nothing |
| operator token | `BSIO_API_TOKEN` | everything, including deleting apps |

All three arrive as `Authorization: Bearer …`; `presentedKey` routes by prefix.
`mayAdminister` compares against the operator token in constant time, so a bearer
API token can never administer. Tokens are managed at `/admin/tokens`; minting is the
only time the plaintext exists, so the UI shows it once and says so.

## The trap that cost the most

`time.Truncate` counts from **year 1**. Postgres `date_bin` counts from **its origin**.
They agree only for intervals that divide a day — so a 7h12m automatic interval put
every Postgres bucket off the Go axis and every chart silently read **zero**. Use
`events.alignDown` (epoch arithmetic) for any bucket axis, and keep
`TestAlignDownMatchesDateBin` passing.

Related: automatic intervals snap **up** to a width that divides a day, so buckets start
on wall-clock boundaries instead of drifting across the axis.

## Read paths go through one place

`internal/events/discover.go` owns the aggregation: `SearchIssues`, `DiscoverEvents`,
`EventSeries`, plus the Discover field and aggregate registries. The Sentry Web API
(`internal/api/sentryweb.go`), the analytics endpoint and the UI's charts all answer from
it. **No hand-written aggregate SQL in the API layer** — a second path is a second set of
numbers.

An unsupported *filter* is an error, never a silent no-op: a panel that quietly ignores
`level:error` does not look broken, it looks like there are no errors. An unrecognised
`key:value` is a tag lookup (the tag key space is open, `gpu:0` must work); an
unsupported *operator* fails loudly.

**A windowed list means "fired in the window", not "lifetime overlaps it".** Measured: an
Aug 10-15 window listed eleven issues, four of which had produced none of the 51 events
the chart above it was drawing — they had merely started inside the window and were still
going. `Issues()` now requires an `exists` on an event inside the bounds, after a cheap
prefilter on the indexed `last_seen`.

This one is worth internalising because **it fails silently in the direction that
matters**: an over-reporting list does not look broken, it looks like a busy project. It
surfaced only when the same quantity was computed a second way and the two disagreed —
`/api/0/analytics` said 4 issues for that window while `/api/0/issues` said 11. When a
figure has two possible derivations, compute both once and compare; that is the only
check that catches a plausible wrong number.

### The one-partial-bucket trap

`EventSeries` floors its first bucket with `alignDown(from, interval)` and filters
`received_at >= buckets[0]`, so a series total can exceed an exact `[from, to)` aggregate
by one leading partial bucket. That is why `OccurrenceChart`'s `total` prop is optional
and the analytics page omits it. **Never print two totals for the same window on one
page.**

## Project analytics (`internal/api/analytics.go`)

`GET /api/0/analytics?project=<slug>&statsPeriod=30d` returns aggregates only: window
totals, distinct issues, the previous window of the same length with its own level split,
breakdowns, an environment × level cross-tab, and a top-10 leaderboard. Read-only.

- **It deliberately owns no time series.** The chart on that page comes from
  `GET /api/0/apps/{slug}/series`. One endpoint owning the buckets is the only thing
  keeping the chart and the figures beside it from disagreeing. A new metric that needs
  buckets goes *there*.
- Breakdown dimensions are `error.type, environment, release, transaction, server_name`
  plus up to three *discovered* tags. A tag is skipped when a fixed dimension already
  answers it, when it has >50 distinct values (an identifier, not a dimension), when it
  yields fewer than two rows, or when ≥90% of its events leave it unset. Measured: `url`
  was set on 3 events out of 196 and its panel was one full-width "(not set)" bar.
- **Cost, stated honestly:** up to 13 grouped queries per request, run sequentially. Fine
  at this scale; if it needs to be faster, parallelise with a `WaitGroup` rather than
  cutting panels.
- Issue-level panels read `getIssues(..., { limit: 200 })` — the engine defaults to 100
  and caps at 500, and a figure summed from a silently truncated list is worse than no
  figure.
- The hour-of-day heatmap makes a second series call at a **fixed** `interval: "1h"`;
  `auto` snaps to 12h at 30 days and destroys the axis. Buckets are UTC and folded into
  weekday × hour in the server's zone, which is why the panel prints the zone name.
- Its shading uses **absolute count bins** (1, 2-3, 4-7, 8-15, 16+), not a share-of-max
  ramp. Measured: with one hour of 33 events and a long tail of ones, both linear and
  sqrt against the maximum put every quiet cell within a few percent of every other and
  the grid read as one flat wash.

## UI conventions

Beyond the visual rules in [DEVELOPING.md](DEVELOPING.md) (dark default, status tokens
rather than `--chart-*` for meaning, mono + `tabular-nums` for every figure):

- **No native `<select>`.** Use `@/components/bsio/select-box.tsx` — the themed Radix
  select wearing the `<option>` children API. Radix rejects `value=""`, so the "all"
  option is mapped to a sentinel, and `onChange` receives `{target:{value}}` rather than
  a real event.
- **No `window.confirm`.** Use `@/components/bsio/confirm-dialog.tsx`. The browser's own
  dialog ignores the theme, cannot explain what is about to happen, and blocks the main
  thread so the row behind it cannot show that it is busy.
- **Never `SelectValue` in a trigger.** It reads its text from the portalled items, so it
  ships empty from the server and the control flashes blank until hydration. Render the
  resolved label in the trigger.
- **`SelectContent` must be `position="popper"`.** The upstream template defaults to
  `item-aligned`, which resolved to `position: fixed; top: 1050px; left: 0` — the
  viewport's bottom-left corner — for *every* menu on every page. The shell is
  `h-screen overflow-hidden`, so there is nowhere to scroll to reach it: the user clicks
  and nothing appears. `ui/select.tsx` now defaults to popper, and the house components
  pass it explicitly because `web/` is vendored (`web/UPSTREAM.md`) and a re-sync would
  restore the upstream default.
- **A DOM-level check does not prove a control works.** That menu was in the DOM with the
  right options, `allInnerTexts()` on the trigger read back correctly, and keyboard
  selection worked — three sessions "verified" dropdowns that were invisible. Click the
  thing, then assert its menu's bounding box is inside the viewport.
- **Windows live in the URL.** `?range=` (default 30d), `?interval=` (default auto), and
  for `range=custom` a `?start=`/`?end=` pair, all via `@/lib/ranges`.
  `resolveWindow(searchParams)` on the server, `<WindowControls window={w} />` for the
  picker, `OccurrenceChart` for the drawing. A list under a chart takes the same window
  (`getIssues(slug, { window: w })`) so the two cannot disagree, and a link carries the
  view. Two pickers for one window is two sources of truth — pass `controls={false}` to
  the chart when the page mounts its own.
- **Custom windows carry their offset** (`2026-08-20T13:00:00+08:00`), never bare local
  time. `fromLocalInput`/`toLocalInput` convert, and both are **browser-only**: the pages
  and `bsio.ts` are server-side, so resolving "13:00" there would resolve it in the
  server's zone and move a +08 user's afternoon by eight hours.
- **The filter row's other controls do not navigate.** `q`/`status`/`level`/`env` narrow
  rows the browser already has, so they write the URL with `history.replaceState` — the
  view stays linkable without a round trip for data we are holding. Only the window
  navigates, because only the window changes what the engine must read.
- **Panel collapse is a cookie**, not localStorage (`@/lib/panel-state`). The layout is
  server-rendered, so the server must know before it emits HTML; reading it in the
  browser paints the expanded sidebar and snaps it shut on hydration.
- **Anything that reads the clock during render** needs the wrappers in
  `@/components/bsio/time.tsx` — server and client render a second apart, which is a
  hydration mismatch (React #418). `Date.now()` inline in a cell is the usual culprit.

### `web/src/lib/bsio.ts`

One client module for the engine. `get`/`write` stay **unexported**: they are the only
place the operator token is attached, and keeping them private is what stops a client
component importing something that reaches the engine with it. Add a function here rather
than exporting the helper. Everything is server-only and returns `Result<T>` — it never
throws, so a page can say "the engine is unreachable" instead of crashing.

### `web/src/lib/nav.ts`

`projectNav()` is the per-project order: Errors & Outages, Breached Metrics, Warnings,
Analytics, Releases, Alerts, Setup — lists first, configuration last.

**Every nav item needs an `ITEM_ICONS` entry** (project views keyed by last path segment,
admin items by full path) or the collapsed rail renders a blank, unlabelled row. That bug
shipped twice in one day.

## Several sessions work here at once

Two or three Claude sessions routinely share this checkout and the dev database.

- `ListAgents`, then message the peers **before** touching `web/src/lib/nav.ts`,
  `web/src/lib/bsio.ts`, or `internal/api/api.go`'s route table. Every feature needs
  those three, Edit/Write are read-modify-write, and an edit racing a peer silently
  reverts theirs. Send the owner the exact lines instead.
- **A package that will not compile mid-session is usually a peer's refactor in flight.**
  Do not "fix" it. Type-check your own against a clean baseline:
  `git show HEAD:internal/alert/alert.go > /tmp/a.go`, then `go build -overlay` with a
  JSON mapping for that path.
- The dev database is shared and the user is looking at it. Anything you create while
  testing is visible to them as real data — name it obviously and delete it when you are
  done. Two test tokens named `grafana` were reported as a duplicate-creation bug.

## Style

Comments explain *why*, and say what was measured. The codebase is full of load-bearing
reasons — why beats go downstream of the work, why Prisma is fenced into `auth`, why a
count comes back as a string — and they exist because each was a bug once. A comment that
restates the code earns nothing; one that records the failure it prevents earns its line.

`gofmt` under Go 1.26 wants to rewrite `''` to typographic quotes inside comments in four
pre-existing files. `make check` runs `gofmt -w`, so that churn will appear in an
unrelated diff if you let it.
