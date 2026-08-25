# Grafana

Two ways in, and the recommendation reversed once we had used both:

1. **`scicom-bettersentryio-datasource` — ours, and the default.** Typed frames
   (counts are numbers, timestamps are time fields), heartbeat monitors and incidents
   as first-class query types, the analytics leaderboard, and lookup by
   correlation/trace id with deep links back into the UI. Lives in
   [`scicom-bettersentryio-datasource/`](../../scicom-bettersentryio-datasource/).
2. **[grafana-sentry-datasource][ds] — the compatibility path.** The engine answers
   Sentry's *Web* API, so Grafana Labs' plugin works unmodified. Kept provisioned for
   anything built on it, and as proof the compat surface holds.

The original position here was "there is no bettersentryio plugin and there will not
be one". Reversed deliberately (2026-08-24) after real use: the Sentry datasource
returns issue counts as strings, needs an `organize` transformation to make its
35-field frames readable, speaks in org slugs and Discover, and has no notion of the
flagship data — monitors, beats, incidents. Compatibility is a fine floor and a bad
ceiling.

[ds]: https://grafana.com/grafana/plugins/grafana-sentry-datasource/

## The native datasource

Frontend-only on purpose: queries run in the browser and reach the engine through
Grafana's **data proxy** (the `engine` route in `plugin.json`), which attaches the
API token server-side — the browser never holds the credential and there is no Go
binary to build per platform or sign. The one capability that costs is
Grafana-managed alert rules (they need a backend plugin), and it costs nothing here:
bettersentryio is itself the alerter.

Query types: `events` (per-level, zero-filled series), `issues`, `monitors` (from
`/api/0/overview` — the wall endpoint is a leaner shape), `incidents`, `topIssues`,
`lookup` (`?tag=correlation_id:…` / `?trace=…` against per-event identity,
GIN-indexed, with an "Open in bettersentryio" data link on every hit), and
`eventDetail` — the full anatomy of the newest matching event as section/key/value
rows: exception with stacktrace, highlights, every context (trace included),
additional data, and installed packages, i.e. what the issue page shows, inside
Grafana. Dashboard variables interpolate in the identity fields, so a textbox
variable drives both the lookup table and the detail panel.

`lookup` and `eventDetail` ignore the panel's time range, and the engine leaves an
unwindowed identity search unwindowed (2026-08-25; both ends measured). The id is
exact, and the person pasting it rarely knows when the event happened — that is why
they are looking it up. Windowed, the trace-correlation click answered "no data" for
any trace older than the pane's range, which reads as "not in Sentry" when the truth
is "not in the last hour". An explicit `start`/`end` on the API is still honoured.
Both ends also trim pasted ids: a trailing space from a log line or a Grafana cell
made an exact-match search silently empty.

Build and run:

```bash
cd scicom-bettersentryio-datasource
npm install && npm run build        # → dist/, which the compose file mounts
```

`examples/grafana/docker-compose.yml` mounts `dist/`, allows the unsigned plugin id,
and provisions the datasource (`uid: bsio-native`, default) with
`BSIO_GRAFANA_TOKEN`/`ACCESS_TOKEN` from the repo-root `.env`. Grafana must be
**≥ 12.3** (the scaffold's dependency floor). The `bettersentryio — native` dashboard
is provisioned alongside the sentry-compat one.

Two provisioning traps, both measured:
- **Never rename a provisioned datasource in place.** The provisioner matches rows by
  name; a name pointing at a new uid/type is a "data source not found" crash-loop on
  a volume that already holds the old row. New identity → new name.
- **A frontend datasource cannot be verified through `/api/ds/query`** — that path is
  backend-only. Queries run in the browser, so only a browser proves them.

## The sentry-compat path

```
sentry_sdk ──envelope──▶ ┌─────────────────┐ ◀──/api/0/organizations/…── grafana-sentry-datasource
                         │  bettersentryio │
   beat() ──heartbeat──▶ └─────────────────┘ ──▶ operator UI (Next.js)
```

## Run it

```bash
# Mint a token in the UI first: Settings → API tokens → Create token.
echo 'BSIO_GRAFANA_TOKEN=bsiot_…' >> .env

set -a; . .env; set +a
cd examples/grafana && docker compose up -d
open http://localhost:3020                   # admin / admin
```

The datasource and one dashboard are provisioned, so there is nothing to configure by
hand. **Save & test** answers `plugin health check successful. N projects found.`

## Two traps worth naming

**The DSN key and the read credential are different things.** In Sentry they are
strictly different — the DSN's public key only authorises *writing* events, and reading
requires an auth token, which is why `GET /api/0/…` on a Sentry install rejects a DSN
key outright.

Mint the read credential in **Settings → API tokens** (`/admin/tokens`). A token is
`bsiot_…`, read-only, named, individually revocable, and reports when it was last used;
the engine stores only its SHA-256, so it is shown exactly once. Three credentials are
accepted as `Authorization: Bearer …`, and only the first belongs in a dashboard:

| Credential | Where it comes from | What it can do |
|---|---|---|
| **API token** (`bsiot_…`) | Settings → API tokens | reads only — give Grafana this |
| operator token | `BSIO_API_TOKEN` in `.env` | everything, including deleting apps; not revocable without a redeploy |
| an app's ingest key | Apps → the app → Settings | reads, but it exists for *writing* events and is embedded in client code |

There is no scopes UI and no per-project token: a permissions subsystem is not
something we are building (PLAN, "what it will never do"). A token reads everything or
it is revoked.

**`localhost` in the datasource URL is Grafana itself.** Inside the container,
`localhost:9090` is the container's own port 9090, not the engine on your host. The
provisioning uses `host.docker.internal`, and the compose file maps it to
`host-gateway` so it also resolves on Linux, where Docker does not provide that name.
On Kubernetes it is a Service DNS name instead.

A third, smaller one: the token lives in the **repo-root `.env`, which is gitignored**,
and the provisioning file interpolates `${BSIO_GRAFANA_TOKEN}` from the environment
(falling back to `BSIO_API_TOKEN` so a fresh clone works before anyone has minted one).
`examples/grafana/` deliberately contains no `.env` of its own — a tracked file with an
empty `…_TOKEN=` line is an invitation to commit a real one.

## What the datasource asks for, and what it gets

| Query type | Endpoint | Status |
|---|---|---|
| `issues` | `/api/0/organizations/{org}/issues/` | ✅ window-scoped counts, `lifetime` dates, permalinks |
| `events` | `/api/0/organizations/{org}/events/` | ✅ Discover-style field selection, `count()` / `count_unique()` |
| `eventsStats` | `/api/0/organizations/{org}/events-stats/` | ✅ zero-filled series, grouped + `topEvents` |
| `statsV2` | `/api/0/organizations/{org}/stats_v2/` | ✅ every event is `outcome=accepted`, `category=error` |
| `metrics` | `/api/0/organizations/{org}/metrics/data/` | ❌ 501 with a reason |
| `spans`, `spansStats` | Discover with a spans dataset | ❌ no tracing here |

Config and picker calls are answered too: `organizations/`, `{org}/projects/`,
`{org}/teams/`, `teams/{org}/{team}/projects/`, `{org}/tags/`.

The unsupported ones return `501` with a `detail` the plugin surfaces verbatim, rather
than a 404 — *"bettersentryio does not store tracing"* is information; a 404 looks
like a broken URL.

### The mapping

| Sentry | Here |
|---|---|
| organization | the install. One org, slug `bettersentryio`; any slug in a path is accepted, because a single-tenant install refusing a typo helps nobody |
| project | an **app** (a row in `projects`) |
| team | a synthetic `engineering`, because the pickers need something to pick |
| issue | an issue, grouped by fingerprint per environment |
| event | an event row; `id` is the SDK's `event_id` when it sent one |
| tag | `issues.tags` — client tags merged with the ones ingest derives (`level`, `environment`, `release`, `transaction`, `handled`, …) |
| `userCount` | always 0. We do not track users |

### Search syntax

Supported: `is:unresolved|resolved|ignored|archived`, `level:`, `environment:`,
`project:`, `error.type:`, `issue.id:`, `message:`/`title:`, `tags[k]:v`, any
`key:value` (resolved against `issues.tags`), quoted phrases, and bare words matched
against the issue title.

Rejected with a message: negation (`!level:error`), boolean operators (`OR`, `AND`,
parentheses) and `is:` values we cannot evaluate (`is:assigned`). An unrecognised
*key* becomes a tag filter — the tag key space is open, so `gpu:0` must work — but an
unsupported *operator* is an error. The rule: a filter that would silently answer a
different question fails loudly; one that can only return fewer rows is allowed to.

### Intervals

`events-stats` buckets are zero-filled and aligned to the epoch, matching Postgres
`date_bin`. An absent interval snaps **up** to the smallest width that divides a day
and keeps the window under ~100 buckets (1m, 2m, 5m, 10m, 15m, 30m, 1h, 2h, 3h, 6h,
12h, 1d, 7d), so buckets start on wall-clock boundaries.

> Measured bug, worth not repeating: the Go axis was first built with
> `time.Truncate`, which counts from **year 1**, while `date_bin` counts from its
> **origin**. They agree only for intervals that divide a day — so a 7h12m automatic
> interval put every Postgres bucket off the axis and every chart read zero.
> `alignDown` in `internal/events/discover.go` does epoch arithmetic instead, and
> `TestAlignDownMatchesDateBin` holds the line.

### Dashboard notes

`examples/grafana/dashboards/errors.json` is provisioned and worth reading before you
build your own, because three things bite:

- **Sentry reports issue counts as strings** (`"count": "91"`) and the plugin passes
  that through. Without a `convertFieldType` transformation the column sorts
  lexicographically — `"9"` above `"91"` — and gauge cells refuse to draw.
- **The issues frame has 35 fields.** A table showing all of them is unreadable; the
  dashboard hides all but the triage columns with an `organize` transformation.
- **Grafana derives `interval` from panel width**, which over 30 days asks for
  minute-wide buckets and draws hairlines. The bar panel sets a `6h` minimum.

## Grafana's own deep links

The plugin builds "Open in Sentry" links from the datasource URL, so they arrive at the
engine as `/organizations/{org}/issues/{id}/` and
`/organizations/{org}/discover/{project}:{event}/`. Both redirect to the operator UI's
issue page (`{base-url}/apps/{slug}/errors/{id}`), the second by resolving the event id
back to its issue — so a link out of a Grafana panel lands somewhere useful instead of
on a 404.

## Where the code is

| File | What |
|---|---|
| `internal/api/sentryweb.go` | routes, Sentry response shapes, search and window parsing |
| `internal/events/discover.go` | the queries: issue search, Discover fields, bucketed series |
| `examples/grafana/` | compose, provisioning, starter dashboard |
