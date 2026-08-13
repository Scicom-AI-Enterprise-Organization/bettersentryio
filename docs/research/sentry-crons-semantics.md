# Sentry Crons check-in semantics (research notes)

> Source: read from `getsentry/sentry` @ `b815e2e0` (2026-08-07, shallow clone).
> All file paths below are relative to the sentry repo root.
> Why we care: bettersentryio's killer feature is catching silently-dead background loops
> (health endpoint green, batching loop stuck). Sentry Crons check-ins are the proven
> wire semantics for this, and official `sentry-sdk` already speaks them — if we mirror
> the subset below, `sentry_sdk.crons.monitor` / `capture_checkin` work against bettersentryio unmodified.

## 1. The check-in wire payload

Envelope item type `check_in`, JSON payload (`src/sentry/monitors/types.py:24-31`):

```jsonc
{
  "check_in_id": "<32-hex uuid>",   // all-zero UUID = "close the latest in_progress check-in"
  "monitor_slug": "tts-batcher",
  "status": "in_progress" | "ok" | "error",   // only these 3 accepted from clients
  "environment": "production",      // optional; server defaults to "production"
  "duration": 12.5,                 // optional, SECONDS on the wire; stored as ms (×1000)
  "monitor_config": { ... },        // optional → upsert monitor on first check-in
  "contexts": { "trace": { "trace_id": "<hex>" } }   // optional
}
```

Typical SDK usage is two-phase: send `in_progress` at job start (opens the check-in, arms the
timeout), then `ok`/`error` with the same `check_in_id` at the end. One logical run = 2 messages
(noted in `src/sentry/options/defaults.py:2606-2608`).

### Monitor config (upsert form SDKs send)

`src/sentry/monitors/validators.py:160-309` accepts:

```jsonc
{
  "schedule": { "type": "interval", "value": 5, "unit": "minute" },
  // or { "type": "crontab", "value": "*/10 * * * *" },
  "timezone": "UTC",                // must be a known tz
  "checkin_margin": 5,              // minutes of grace after expected time; default 1, min 1
  "max_runtime": 30,                // minutes an in_progress may run; default 30, max 40320 (28d)
  "failure_issue_threshold": 1,     // N consecutive failures before incident; 1..720
  "recovery_threshold": 1           // N consecutive OKs to resolve; 1..720
}
```

- Interval units: `year|month|week|day|hour|minute` (`validators.py:74`), value int > 0.
- Crontab: 5 fields max, `@daily`-style macros expanded, `@reboot` unsupported, must round-trip
  through `cronsim` (`validators.py:283-301`).
- Stored form is normalized (`schedule_type` int enum 1=crontab 2=interval; interval as `[5,"minute"]`)
  — schema at `src/sentry/monitors/models.py:55-71`.
- **Upsert: yes.** First check-in carrying `monitor_config` auto-creates the monitor
  (`_ensure_monitor_with_config`, `src/sentry/monitors/consumers/monitor_consumer.py:94-191`);
  config changes on later check-ins are merged. Check-in without config for an unknown monitor
  → dropped (`MONITOR_NOT_FOUND`).

## 2. Data model essentials

Three tables (`src/sentry/monitors/models.py`):

- **Monitor** — slug (unique per project), name, config JSON, status (active/disabled).
- **MonitorEnvironment** — per (monitor, environment). Carries the detection state:
  - `next_checkin` — next expected check-in time
  - `next_checkin_latest` — `next_checkin + checkin_margin` → **miss fires when clock passes this**
  - `last_checkin` — last *real* check-in (synthetic MISSED rows never advance it)
  - `status` — MonitorStatus: `ACTIVE(0) DISABLED(1) ... OK(4) ERROR(5)`
  - Index `(status, next_checkin_latest)` is the miss-scan index (`models.py:699`).
- **MonitorCheckIn** — guid (= client `check_in_id`), status, duration ms,
  `expected_time`, `timeout_at` (= start truncated to minute + max_runtime),
  plus a denormalized `monitor_config` snapshot.

`CheckInStatus` enum (`models.py:130-182`):

```
UNKNOWN=0  OK=1  ERROR=2  IN_PROGRESS=3  MISSED=4(synthetic)  TIMEOUT=5(synthetic)
```

Incidents: **MonitorIncident** — one open incident max per monitor-env (DB constraint,
`models.py:800-805`), `grouphash = f"crons:{monitor_environment_id}"`.

## 3. Miss / timeout detection algorithm

What drives detection in Sentry: a **clock derived from Kafka message timestamps** (never
wall-clock) — slowest partition wins, ticks once per minute, backfills skipped minutes
(`src/sentry/monitors/clock_dispatch.py:72-153`). A celery `clock_pulse` task keeps it ticking
under low traffic. Each tick runs `check_missing` then `check_timeout`
(`src/sentry/monitors/consumers/clock_tick_consumer.py:27-56`).

**One-minute granularity everywhere** — every schedule computation clamps
`second=0, microsecond=0` (`src/sentry/monitors/schedule.py:18-53`).

### MISSED (`src/sentry/monitors/clock_tasks/check_missed.py`)

1. Scan: `SELECT ... WHERE next_checkin_latest <= tick_ts` (excluding disabled/muted-for-deletion).
2. For each hit, create a **synthetic check-in** `status=MISSED`, back-dated to the *expected*
   time (`date_added == expected_time == next_checkin`), so the timeline shows when it should
   have run (`check_missed.py:114-124`).
3. Recompute the next expectation from `get_prev_schedule(expected, now)` — i.e. re-anchor to the
   most recent schedule slot before *now*, not `expected + interval`. This prevents schedule drift
   after gaps/backlogs (`check_missed.py:154-158`). Keep `last_checkin` unchanged.
4. `mark_failed(...)` → incident logic.

A monitor can never be MISSED before its first real check-in (`next_checkin` is null until then,
asserted at `check_missed.py:106`).

Worked example (`tests/sentry/monitors/clock_tasks/test_check_missed.py:174-270`): 10-min interval
+ 5-min margin → miss fires only after `next_checkin + 5min` passes; MISSED row stamped at the
expected time; `next_checkin` advances a full interval from the missed slot.

### TIMEOUT (`src/sentry/monitors/clock_tasks/check_timeout.py`)

1. Scan: `SELECT ... WHERE status = IN_PROGRESS AND timeout_at <= tick_ts` (index `(status, timeout_at)`).
2. Set `status=TIMEOUT`. **Skip mark_failed if a newer OK/ERROR check-in already exists**
   (out-of-order guard). Timeout does NOT advance the schedule.
3. Every `in_progress` heartbeat on the same check-in **re-arms `timeout_at`**
   (`monitor_consumer.py:440-444`) — long-running jobs stay alive by re-sending in_progress.

### Incident thresholds (`src/sentry/monitors/logic/incidents.py:38-195`)

- Failure: with `failure_issue_threshold=N`, trip when the last N check-ins are all non-OK
  (ERROR/MISSED/TIMEOUT/UNKNOWN all count). Default N=1.
- On trip: env status → ERROR, open MonitorIncident, emit issue occurrence
  (title `"Cron failure: {name}"`, fingerprint = incident grouphash). Muted envs record
  everything but emit no occurrences.
- Recovery: `recovery_threshold=N` consecutive OKs → resolve incident, env status → OK.
- Out-of-order guard: `mark_ok` only advances state if the check-in isn't older than `last_checkin`.

### UNKNOWN (data-loss guard)

During a detected Sentry-side systems incident, in-progress check-ins are marked `UNKNOWN`
instead of timing out, so users don't get paged for Sentry's own outage
(`src/sentry/monitors/clock_tasks/mark_unknown.py`). Nice-to-have, not core.

## 4. Ingest behaviors worth mirroring

From `src/sentry/monitors/consumers/monitor_consumer.py`:

- Process serially per `(project, slug, environment)` — never reorder one monitor's check-ins.
- `check_in_id` of all zeros = "apply to latest in_progress check-in".
- A check-in already OK/ERROR cannot be re-opened; a TIMEOUT can receive a late duration but
  stays TIMEOUT (`monitor_consumer.py:428-437`).
- Implicit duration when closing without one: `abs(close_time - open_time)`.
- Per-monitor rate limit: default **6 check-ins/min** (option `crons.per_monitor_rate_limit`).
- Limits: 1500 monitors/org, 1000 envs/monitor (`src/sentry/conf/server.py:3142-3143`).
- Environment defaults to `"production"` when absent (`models.py:628-629`).

## 5. Python SDK surface (what must keep working)

From sentry's own onboarding guides
(`static/app/views/insights/crons/components/manualCheckInGuides.tsx`, `upsertPlatformGuides.tsx`):

```python
from sentry_sdk.crons import monitor

@monitor(monitor_slug='tts-batcher', monitor_config=monitor_config)  # upsert form
def run_batch():
    ...

# or manual two-phase:
check_in_id = capture_checkin(monitor_slug='tts-batcher',
                              status=MonitorStatus.IN_PROGRESS, monitor_config=...)
capture_checkin(monitor_slug='tts-batcher', check_in_id=check_in_id,
                status=MonitorStatus.OK)
```

Copy-paste test payload builders: `src/sentry/monitors/testutils.py:14-41`,
`tests/sentry/monitors/consumers/test_monitor_consumer.py:92-111`.

## 6. Implications for bettersentryio

- **No Kafka needed.** The entire detection engine reduces to: a monotonic once-per-minute tick +
  two indexed scans (`next_checkin_latest <= now`, `in_progress AND timeout_at <= now`) +
  per-monitor serialization. Single-node wall-clock is fine and stays wire-compatible.
- Keep Sentry's invariants or schedules drift: synthetic misses never advance `last_checkin`;
  always re-anchor the next expectation via prev-schedule-before-now.
- `in_progress` + `max_runtime` re-arming is *exactly* the dead-loop detector: a loop iteration
  opens/heartbeats a check-in; if the loop wedges, `timeout_at` fires regardless of what
  `/health` says.
- bettersentryio extension beyond Sentry (our differentiator): heartbeats may carry a monotonic
  progress counter (e.g. `items_processed`) so we can flag a loop that beats but processes
  nothing — the vLLM torch.compile "super silence" case. Sentry has no equivalent.
