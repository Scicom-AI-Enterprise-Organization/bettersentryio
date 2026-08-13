# Minimal Sentry-compatible ingest surface (research notes)

> Sources: `getsentry/sentry` @ `b815e2e0` (marked `[repo]`, paths relative to repo root) and the
> **installed official `sentry-sdk` 2.50.0** at `~/.venv/lib/python3.10/site-packages/sentry_sdk`
> (marked `[sdk]`) — i.e. the exact client our services run.
>
> Key discovery: the sentry monolith does **not** implement the ingest HTTP endpoints — Relay
> (separate Rust repo) does. The authoritative in-repo artifact is the Relay-generated event
> schema `src/sentry/issues/event.schema.json` (3,062 lines, JSON Schema draft-07); the
> authoritative client behavior is the SDK itself. Both cited below.
>
> bettersentryio deviation from this note's checklist: `check_in` items are **MUST** for us (they feed
> the monitor engine — see [sentry-crons-semantics.md](sentry-crons-semantics.md)), not
> accept-and-drop.

## 1. Endpoints

DSN `scheme://public_key@host/prefix/project_id` → endpoint
`{scheme}://{host}/{prefix}/api/{project_id}/envelope/`.

- `POST /api/<project_id>/envelope/` — **the only endpoint modern Python SDKs use**
  ([sdk] `consts.py:14-24`, URL built `utils.py:396-406`). **Trailing slash required** (SDK
  hardcodes it). Honor a DSN sub-path prefix if present.
- `POST /api/<project_id>/store/` — legacy raw-event JSON, same auth
  ([repo] `src/sentry/testutils/pytest/relay.py:228-232`). SHOULD, for old SDKs.
- Everything else (`/minidump/`, `/security/`, `/csp-report/`, `/nel/`, `/unreal/`, OTLP…) —
  out of scope v1; return 200-empty if we ever want SDK silence
  ([repo] `src/sentry/api/serializers/models/project_key.py:93-106`).

## 2. Authentication

```
X-Sentry-Auth: Sentry sentry_key=<public_key>, sentry_version=7,
               sentry_client=<name>/<ver>[, sentry_secret=<secret>]
```

- Built at [sdk] `utils.py:408-421`, sent at `transport.py:320-326`. `sentry_version` is `7`
  (accept `6` too — [repo] `src/sentry/testutils/helpers/auth_header.py:4-16`). `sentry_secret`
  is deprecated → accept and ignore.
- Fallback: `?sentry_key=` query param (browser endpoints)
  ([repo] `src/sentry/models/projectkey.py:265-292`).
- CORS (for browser SDKs later): allow headers `X-Sentry-Auth, Content-Type, Content-Encoding,
  sentry-trace, baggage`; expose `X-Sentry-Error, Retry-After`
  ([repo] `src/sentry/api/base.py:165-172`).

## 3. Envelope framing

`Content-Type: application/x-sentry-envelope` ([sdk] `transport.py:455-462`). Newline-delimited:

```
{envelope_header_json}\n
{item_header_json}\n
<payload bytes>\n        ← repeat per item; no trailing newline required after last
```

Parser rules ([sdk] `envelope.py:129-140, 291-329`): if item header has `length`, read exactly N
bytes then consume one newline; if absent, read to end of line. Envelope header from Python SDK:
`event_id` (32-hex), `sent_at` (RFC3339), optional `trace` (dynamic sampling context object)
([sdk] `client.py:876-887`). Other SDKs add `dsn`, `sdk` — parse-and-ignore. All envelope-header
fields optional for a minimal server.

Item header: `type` (required), `length`, `content_type`, plus type-specific extras
(`filename`/`attachment_type`, `item_count`, `platform`) — tolerate unknown keys.

### Item type routing (bettersentryio policy)

| `type` | Policy |
|---|---|
| `event` | **process** → error pipeline |
| `check_in` | **process** → monitor engine ([sdk] `envelope.py:86-90`) |
| `client_report` | accept-and-drop, **never rate-limit** (it's the SDK's loss telemetry, [sdk] `transport.py:381-400`) |
| `transaction`, `session(s)`, `attachment`, `profile(_chunk)`, `log`, `trace_metric`, `statsd`, `replay_*`, `user_report`/`feedback`, `span`, … | accept-and-drop with 200 (count an outcome) |
| **anything unknown** | accept-and-drop — **never 400** ([sdk] `envelope.py:249-273`) |

Full category taxonomy mirror: [repo] `static/app/types/dataCategory.tsx:41-69`.

## 4. Error event payload

Canonical schema: **[repo] `src/sentry/issues/event.schema.json`** — port this, stay permissive
(`additionalProperties: true` everywhere).

Minimum viable event from an unmodified Python SDK: `event_id`, `timestamp` (epoch float **or**
RFC3339 string, schema `:2776`), `platform`, `level` (`debug|info|warning|error|fatal`, `:1682`),
`exception.values[].{type,value,stacktrace.frames[]}`, `sdk.{name,version}`.

- `Exception` (`:1182`): at least one of `type`/`value` required or the exception is discarded.
- `Frame` (`:1262`): `filename`, `abs_path`, `function`, `module`, `lineno`, `colno`, `in_app`,
  `context_line`, `pre/post_context`, `vars`, native fields. Frames are callee-last ordered
  (crash at the end).
- `logentry` (`:1742`): `message`/`formatted`/`params`, strings capped 8192.
- `tags` (`:2660`): object map **or** `[k,v]` pair array — accept both.
- `fingerprint` (`:1251`): `string[]`.

Storage strategy: store the raw JSON blob, index only what the UI/grouping needs.

## 5. Response contract & rate limiting

- Success = any 2xx; **body never parsed** by the SDK ([sdk] `transport.py:337-357`). Return
  `200` + `{"id": "<32-hex event_id>"}` (what Relay returns; [repo]
  `src/sentry/testutils/relay.py:38-51`).
- Rate limiting, consumed at [sdk] `transport.py:285-306`:
  1. `X-Sentry-Rate-Limits: <retry_secs>:<cat>;<cat>:<scope>:<reason>` — honored on **any**
     status code incl. 200; empty category list = all. Category tokens per [sdk]
     `_types.py:305-321` (`error`, `monitor`, `session`, …).
  2. `429` + `Retry-After` — fallback; missing header → SDK backs off 60 s globally.
- On 429 the SDK does not record a client-report loss (assumes server counted it).

## 6. Encodings & limits

- Python SDK sends `Content-Encoding: gzip` by default, `br` optionally, identity when
  compression disabled ([sdk] `transport.py:205-225, 471-490`). Implement: identity + gzip
  (MUST), br + deflate (SHOULD), zstd (nice).
- Body caps aren't in the monolith (Relay's stock config: ~100 KiB/event compressed, 20 MiB
  attachments — not verifiable from this repo). Sane caps: **20 MB/envelope, 1 MB/event**,
  reject with 413.
- **Timestamp acceptance window**: clamp/reject > 60 s future or > 30 d past
  ([repo] `src/sentry/constants.py:699-708`).
- Downstream field caps worth copying: tag value 256, culprit 200, release 200, environment 64
  ([repo] `src/sentry/constants.py:42,72-77`).

## 7. MUST-implement checklist (drop-in Python SDK)

- [ ] `POST {prefix}/api/<project_id>/envelope/` (trailing slash, sub-path honored)
- [ ] Parse `X-Sentry-Auth` (versions 6+7, secret optional) + `?sentry_key=` fallback
- [ ] gzip + identity decoding
- [ ] Envelope parser (length-exact reads, tolerant headers)
- [ ] Route `event` + `check_in`; accept-and-drop all else; never 400 a well-formed envelope
- [ ] Permissive event normalization per `event.schema.json`; generate/normalize `event_id`
- [ ] `200` + `{"id": …}`; timestamp clamping
- [ ] 429 + `Retry-After` + `X-Sentry-Rate-Limits` on per-key token-bucket overflow
