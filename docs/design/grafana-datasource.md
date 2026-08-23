# Grafana, through the official Sentry datasource

**There is no bettersentryio Grafana plugin, and there will not be one.** The engine
answers Sentry's *Web* API, so [grafana-sentry-datasource][ds] — Grafana Labs' own,
signed, maintained plugin — queries us unmodified. This is D14's bargain applied to
the read side: the SDKs needed no code change, and neither do dashboards.

[ds]: https://grafana.com/grafana/plugins/grafana-sentry-datasource/

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
