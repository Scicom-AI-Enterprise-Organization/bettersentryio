# Sentry-compatible ingest: the refactor plan

> Status: **approved direction** (Husein, 2026-08-18) — implementation-ready.
> Protocol facts: [research/sentry-ingest-protocol.md](../research/sentry-ingest-protocol.md)
> (checked against installed sentry-sdk 2.50 and re-verified against 2.68).
> Crons mapping: [research/sentry-crons-semantics.md](../research/sentry-crons-semantics.md).
> Evidence: the 2026-08-18 stt-api parity audit (see §1).

## 0. The decision (D14)

**The official `sentry_sdk` is the error client. bettersentryio speaks Sentry's wire
protocol on ingest.** The custom JSON endpoint + vendored Python client built for M2
error tracking was a deviation from D3 ("speak Sentry's wire protocol"), and it loses
on capture depth. This doc returns M2 to what PLAN.md always said, with the work
itemized.

The contract, verbatim from the Sentry FastAPI docs, must work with zero extra code:

```python
from fastapi import FastAPI
import sentry_sdk

sentry_sdk.init(dsn="https://<ingest_key>@bsio-ingest.aies.scicom.dev/<project_id>")
app = FastAPI()

@app.get("/sentry-debug")
async def trigger_error():
    division_by_zero = 1 / 0
```

One `sentry_sdk.init(...)` with a bettersentryio DSN. Settle.

## 1. Why (measured, not vibes)

The 2026-08-18 audit ran identical failures through sentry_sdk→sentry.io and the
custom client→bettersentryio, then diffed the stored events. Same `KeyError`:
**45.3 KB captured by sentry_sdk vs 8.3 KB by our client.** The delta is per-frame
local variables, ±5 source context lines, 14 searchable tags, request headers +
client IP + user, breadcrumbs, and runtime/OS contexts — all of it produced
client-side by sentry_sdk for free. Reimplementing that in our own client is a
permanent chase against a mature SDK; accepting the SDK's envelopes gets all of it
in one server-side feature.

The one place our client beat sentry_sdk — a dead asyncio task with a held
reference, missed by Sentry's default config — is covered in sentry_sdk by
explicitly enabling `AsyncioIntegration` (it wraps the coroutine, so it fires on
task completion, not GC). That goes into our recommended init block.

## 2. DSN scheme

`https://<public_key>@bsio-ingest.aies.scicom.dev/<project_id>`

- `public_key` = the existing `ingest_keys.public_key` (already 32-hex, exactly a
  Sentry key's shape). No new key type.
- `project_id` = numeric `projects.id`. The Setup page renders the full DSN per app.
- Same key remains valid for `X-BSIO-Key` beats — one key per app, two protocols.

## 3. Engine work (Go)

### E1 — envelope endpoint (the MUST checklist, now binding)

- `POST /api/{project_id}/envelope/` — trailing slash required (SDK hardcodes it);
  honor a DSN sub-path prefix. Legacy `/store/` deliberately skipped until a real
  old-SDK client shows up.
- Auth: parse `X-Sentry-Auth` (versions 6+7, `sentry_secret` accepted-and-ignored),
  `?sentry_key=` query fallback. Constant-time compare; the key's project must match
  the URL's project_id.
- `Content-Encoding`: identity + **gzip (MUST — SDK default is gzip -9)**, br SHOULD
  (SDK auto-switches to brotli when the module is installed), deflate SHOULD.
- Envelope parser: newline framing, length-exact reads when `length` present,
  tolerate unknown header keys. **Never 400 a well-formed envelope.**
- Item routing: `event` → error pipeline; `check_in` → monitor engine (E3);
  `client_report` → accept, never rate-limit; everything else accept-and-drop with a
  per-type outcome counter (no silent drops — the counter is the honesty).
- Response: `200 {"id": "<event_id>"}` — the SDK never parses the body.
- Rate limiting: per-key token bucket → `429` + `Retry-After` +
  `X-Sentry-Rate-Limits: <secs>:error:project`.
- Caps: 20 MB/envelope, 1 MB/event → 413; timestamps clamped (>60 s future /
  >30 d past); tag value 256, culprit 200, release 200, environment 64.

### E2 — event normalization

- Widen `events.Event` to the Sentry shape: `contexts`, `breadcrumbs`, `user`,
  `sdk`, `modules`, `fingerprint[]`, `threads`; `Request` gains `headers`, `env`,
  `cookies`(dropped by default), full URL; frames gain `abs_path`, `colno`,
  `pre_context`, `post_context`, `vars`. Storage is already a whole-event `jsonb`
  (0003_errors.sql) — **no schema migration expected**, only struct + validation.
- `tags`: accept both object map and `[k,v]` pair array. `level`:
  `debug|info|warning|error|fatal`. `timestamp`: epoch float or RFC3339.
- Grouping: honor an explicit `fingerprint[]` (translate `{{ default }}` to our
  computed hash); otherwise keep the current in-app module+function+filename
  fingerprint (line-number-free — same philosophy Sentry ships).
- `event_id`: normalize to 32-hex, generate when absent; dedupe per (project,
  event_id).

### E3 — check_in → monitor engine (can trail E1/E2)

Per the crons research note: `check_in` items upsert a monitor from the embedded
config; `in_progress`/`ok`/`error` map onto beats + explicit failure. This gives
`sentry_sdk.crons` users the monitor engine with the stock SDK. Native `Beat`
stays — progress-counter stall detection has no Sentry equivalent and remains the
flagship.

## 4. What happens to the custom surface

| Piece | Fate |
|---|---|
| `POST /api/0/errors` | **Kept, frozen.** Zero-dep clients (bash crash reporter, curl) still need it. No new features. |
| `POST /api/0/beat` + `Beat` | **Unchanged.** The differentiator. |
| `clients/python` error capture (hooks, middleware, log handler) | **Legacy.** Superseded by sentry_sdk + `AsyncioIntegration`. Docs and Setup page stop recommending it for services that can pip install. |

## 5. UI work (the audit's other finding)

The Next UI currently has **no error-tracking surface**: nothing fetches
`/api/0/issues`, and `connected` is `Monitors > 0`, so an error-only app renders
"has never reported" while its events sit in the DB. Required regardless of the
protocol, and worth sequencing right after E1/E2 because the payloads become rich:

- App DTO: add `last_event_at` + issue counts; `connected` = beats **or** events.
- Errors tab per app: issue list (title, culprit, level, times_seen, last_seen,
  environment).
- Issue detail: stacktrace with context lines + locals, breadcrumbs, tags chips,
  request block — everything the sentry_sdk payload now carries.
- Alert on first-seen issue: wire `is_new` (the ingest already computes it) into the
  existing alerter + delivery-retry ledger.

## 6. Service migration (stt-api / tts-api): zero code change

Both services already run the standard `SENTRY_DSN`-gated sentry_sdk block, so the
flip is an env change: point `SENTRY_DSN_STT` / `SENTRY_DSN_TTS` at the
bettersentryio DSN. Add `AsyncioIntegration` to the standard block first.

Side-by-side validation before the flip: a ~40-line tee transport (subclass
`HttpTransport`, mirror the serialized envelope to a second DSN, env knob
`SENTRY_DSN_MIRROR`) so both backends receive identical bytes during the window.

**Acceptance test** = the audit's 4-failure harness pointed at a bettersentryio
DSN: unhandled 500, `logger.error`, `logger.exception`, dead asyncio task. Expect
4/4 captured (with `AsyncioIntegration`) and the stored KeyError event carrying
locals + context lines + tags + breadcrumbs — diffed against the saved 45 KB
sentry.io reference event.

## 7. Testing

- Golden envelopes: capture real sentry-sdk 2.68 bytes (mock HTTP sink) into
  `testdata/envelopes/` — identity, gzip, br variants; parser tests replay
  byte-for-byte (PLAN §7 strategy). Keep the 2.50-era fixtures when captured.
- Integration: harness FastAPI app + engine in docker-compose.dev; assert issue
  rows, grouping stability across two identical events, rate-limit headers.
- Never-fail property: fuzz the envelope parser with truncated/garbage bodies —
  every response is 2xx/4xx, never a panic.

## 8. Effort

| Piece | Estimate |
|---|---|
| E1 endpoint + parser + auth + limits | 1–1.5 days |
| E2 normalization + grouping honor | 0.5–1 day |
| UI errors views + connected fix | ~1 day |
| E3 check_in mapping | ~1 day, deferrable |
| Migration flip + acceptance run | hours |

## 9. Open decisions

1. Retire vs freeze `clients/python` error capture — this doc says freeze (§4);
   delete later if nothing zero-dep still uses it.
2. Legacy `/store/` endpoint — skip until a concrete old SDK needs it.
3. Transactions/sessions/logs items — stay dropped-with-counter (non-goal §3 of
   PLAN.md). Revisit only if a counter shows real volume being thrown away.
