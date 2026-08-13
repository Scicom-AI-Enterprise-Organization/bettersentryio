# Dummy TTS service — reproducing the outage on purpose

A FastAPI service with a background batching loop and the bettersentryio SDK wired in.
It exists to make the failure that started this project reproducible in about a minute:
**the HTTP server stays healthy while the loop that does the actual work dies or
freezes.**

## Run it

```bash
python3 -m venv .venv && ./.venv/bin/pip install fastapi uvicorn

# The client is one stdlib-only file, served by the engine you report to.
curl -O http://localhost:9090/clients/python/bettersentryio.py

export BSIO_URL=http://localhost:9090
export BSIO_KEY=<the app's ingest key from Apps → TTS API>
export BSIO_EVERY=10          # short, so failures show up while you watch

./.venv/bin/uvicorn main:app --port 8080
```

`tts-batcher` appears under the app within a few seconds — nothing is registered by
hand.

## Break it

| Command | What happens in the process | What bettersentryio says | What `/health` says |
|---|---|---|---|
| `curl -XPOST :8080/break/freeze` | loop runs, produces nothing (a wedged model call) | **STALLED** after ~30s | `200 {"status":"ok"}` |
| `curl -XPOST :8080/break/kill` | loop cancelled, server up | **LATE** then **MISSING** after ~20s | `200 {"status":"ok"}` |
| `curl -XPOST :8080/fix` | back to normal | recovers, incident resolves | `200` |

Measured run, `BSIO_EVERY=10`:

```
freeze:
  t+10s  bettersentryio: ok       progress=7 beats=16 | /health: 200
  t+20s  bettersentryio: ok       progress=7 beats=26 | /health: 200
  t+30s  bettersentryio: stalled  progress=7 beats=36 | /health: 200
  t+45s  bettersentryio: stalled  progress=7 beats=56 | /health: 200
kill:
  t+20s  bettersentryio: late     | /health: 200 | /health/deep: degraded
  t+30s  bettersentryio: missing  | /health: 200 | /health/deep: degraded
```

Note the beat count climbing while progress sits still. A liveness-only heartbeat —
and every uptime checker that pings a URL — reports this as healthy. The progress
counter is what makes it visible.

## The two health endpoints

`/health` is deliberately naive: it returns `{"status": "ok"}` because the process is
up. This is the endpoint that lied for two days.

`/health/deep` is the honest version, and is worth copying: it reports whether the loop
task is alive, how long since the last batch, and the SDK's own counters. Even with
bettersentryio watching from the outside, a service should be able to answer "is my
work actually happening" itself.

## Where the beat goes

Inside the loop, after the work, in the same iteration:

```python
produced = await synthesize_batch()
state.batches_done += 1
bsio.beat("tts-batcher", progress=state.batches_done, every=EVERY,
          grace=EVERY, stall_window=3 * EVERY)
```

Beating *before* the work, or from a separate timer/thread, reports healthy through
exactly the failure you are trying to catch — the beat has to be downstream of the
thing that can stop.

## Stress test results

Run with `scripts/stress.py`, which drives load at the service while polling the engine, so
HTTP behaviour and monitor state are on the same timeline:

```bash
export BSIO_URL=http://localhost:9090 BSIO_KEY=<the app's key>
python3 scripts/stress.py --scenario errors --error-rate 0.6 --seconds 30
```

Measured, `every=10 grace=10 stall_window=30`:

| Scenario | HTTP behaviour | `/health` | bettersentryio | Caught? |
|---|---|---|---|---|
| Baseline, 16 concurrent | ~40 req/s, p95 587ms, 0 errors | 200 | `ok` | — |
| 60% of requests raise 500 | ~97 req/s, **1737 of 2931 were 500** | 200 | `ok` throughout | **No** |
| Event loop starved (300ms sync CPU/req) | **40 → 4 req/s**, p95 587ms → **10s** (timeouts) | 200 | flickered `late` once, then `ok` | **Partly** |
| Loop raises (CUDA-style death) | ~19 req/s, **0 errors** | 200 | `ok` → `late` @10s → **`missing` @20s** | **Yes, in 20s** |

Three things worth taking from that.

**The fatal exception was never logged.** `grep RuntimeError` over the service log after the
last scenario returns **zero** matches. asyncio only reports an unretrieved task exception when
the Task is garbage collected, and `app.state.loop_task` holds a reference forever — so the
loop died in total silence. No log line, no stderr, no non-zero exit. Log scraping would have
missed it; the health check answered 200; the heartbeat caught it in 20 seconds.

**Request errors are invisible to this.** 1737 HTTP 500s with the monitor green is not a bug, it
is the boundary: heartbeats watch the loop, not the handlers. Error ingest is M2. Until then,
pair this with something that watches status codes.

**It detects stopped, not slow.** Starving the event loop cost a 10× throughput drop and pushed
p95 to a 10-second timeout, and the monitor stayed mostly `ok` — because the loop kept limping,
progress kept moving, and `stall_window` never elapsed. Catching that needs a rate threshold
("fewer than N per minute"), which does not exist yet. Set `every` to what the loop achieves on
a good day and severe degradation will at least surface as `late`.
