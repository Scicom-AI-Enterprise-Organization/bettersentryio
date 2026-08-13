"""
A dummy TTS service, shaped like the one that broke.

It is deliberately built so the real failure is reproducible on demand: the HTTP
server and the batching loop are independent, so the loop can die or freeze while
/health keeps answering 200. That is the whole point — the outage that started this
project lasted two days behind a green health check.

Run it:

    pip install fastapi uvicorn
    export BSIO_URL=http://localhost:9090
    export BSIO_KEY=<the app's ingest key>
    uvicorn main:app --port 8080

Then break it on purpose:

    curl -X POST localhost:8080/break/freeze   # loop alive, no work  -> STALLED
    curl -X POST localhost:8080/break/kill     # loop gone            -> MISSING
    curl -X POST localhost:8080/fix            # back to normal       -> recovered

/health stays 200 through all of it unless you ask it not to.
"""

from __future__ import annotations

import asyncio
import os
import random
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException

from bettersentryio import Beat

MONITOR = os.environ.get("BSIO_MONITOR", "tts-batcher")
EVERY = int(os.environ.get("BSIO_EVERY", "10"))

bsio = Beat(
    base_url=os.environ.get("BSIO_URL", "http://localhost:9090"),
    key=os.environ.get("BSIO_KEY", ""),
    environment=os.environ.get("BSIO_ENV", "production"),
)


class State:
    """Everything the loop and the endpoints both need to see."""

    def __init__(self) -> None:
        self.batches_done = 0
        self.audio_seconds = 0.0
        self.started_at = time.time()
        self.frozen = False  # loop runs, does no work: the "super silence" case
        self.last_batch_at: float | None = None

        # Failure injection, for the stress scenarios.
        self.error_rate = 0.0     # fraction of /synthesize calls that raise
        self.raise_in_loop = False  # next iteration raises, killing the task
        self.block_ms = 0         # sync CPU burn per request: starves the event loop
        self.requests = 0
        self.failures = 0


state = State()


async def synthesize_batch() -> float:
    """Pretend to run a model. Returns seconds of audio produced."""
    await asyncio.sleep(random.uniform(0.2, 0.6))
    return round(random.uniform(1.5, 4.0), 2)


async def batching_loop() -> None:
    """
    The loop that must not silently stop.

    Note where the beat goes: *after* the work, in the same iteration, carrying a
    progress counter. Beating before the work — or from a separate timer — would keep
    reporting healthy through exactly the failure we care about.

    There is deliberately no try/except around the body: an unhandled exception here
    kills the task and leaves the server serving, which is the shape of the outage this
    project was built for.
    """
    while True:
        if state.raise_in_loop:
            raise RuntimeError("model worker died: CUDA error: device-side assert triggered")

        if state.frozen:
            # A wedged model call: the loop is alive and looping, producing nothing.
            # This is what a torch.compile shape mismatch looks like from outside.
            await asyncio.sleep(1)
            bsio.beat(MONITOR, progress=state.batches_done, every=EVERY,
                      grace=EVERY, stall_window=3 * EVERY)
            continue

        produced = await synthesize_batch()
        state.batches_done += 1
        state.audio_seconds += produced
        state.last_batch_at = time.time()

        bsio.beat(
            MONITOR,
            progress=state.batches_done,
            every=EVERY,
            grace=EVERY,
            stall_window=3 * EVERY,
        )

        await asyncio.sleep(max(0.0, EVERY - 1))


@asynccontextmanager
async def lifespan(_: FastAPI):
    task = asyncio.create_task(batching_loop())
    app.state.loop_task = task
    yield
    task.cancel()


app = FastAPI(title="Dummy TTS API", lifespan=lifespan)


@app.get("/health")
def health():
    """
    Deliberately naive: it reports that the process is up, which is what most
    health checks do. This is the endpoint that lied for two days.

    The honest version is /health/deep below.
    """
    return {"status": "ok"}


@app.get("/health/deep")
def health_deep():
    """What the health check should have said all along."""
    task = getattr(app.state, "loop_task", None)
    loop_alive = task is not None and not task.done()
    stale = state.last_batch_at is None or (time.time() - state.last_batch_at) > 3 * EVERY
    return {
        "status": "degraded" if (not loop_alive or stale) else "ok",
        "loop_alive": loop_alive,
        "batches": state.batches_done,
        "seconds_since_last_batch": (
            None if state.last_batch_at is None else round(time.time() - state.last_batch_at, 1)
        ),
        "bsio": bsio.stats(),
    }


@app.post("/synthesize")
async def synthesize(text: str = "hello"):
    """
    The request path. Errors here are invisible to bettersentryio today — it watches
    the loop, not the handlers. That gap is the point of the stress test.
    """
    state.requests += 1

    if state.block_ms:
        # A sync CPU burn inside an async handler blocks the whole event loop, so the
        # batching loop cannot run either. Realistic: any non-awaited heavy call does it.
        deadline = time.perf_counter() + state.block_ms / 1000.0
        while time.perf_counter() < deadline:
            pass

    if state.error_rate and random.random() < state.error_rate:
        state.failures += 1
        raise HTTPException(status_code=500, detail="TTS backend refused the request")

    produced = await synthesize_batch()
    return {"text": text, "audio_seconds": produced}


@app.post("/break/errors")
def break_errors(rate: float = 0.5):
    """Fail a fraction of requests. The loop is untouched."""
    state.error_rate = max(0.0, min(1.0, rate))
    return {"error_rate": state.error_rate, "expect": "bettersentryio sees nothing — the loop is fine"}


@app.post("/break/block")
def break_block(ms: int = 400):
    """Burn CPU synchronously per request, starving the event loop."""
    state.block_ms = max(0, ms)
    return {"block_ms": state.block_ms, "expect": "under load the loop starves -> STALLED"}


@app.post("/break/raise")
def break_raise():
    """Make the next loop iteration raise. The task dies; the server keeps serving."""
    state.raise_in_loop = True
    return {"raise_in_loop": True, "expect": "task dies -> MISSING, /health still 200"}


@app.post("/break/freeze")
def break_freeze():
    """Keep beating, stop working. Expect STALLED after stall_window."""
    state.frozen = True
    return {"frozen": True, "expect": f"STALLED after ~{3 * EVERY}s"}


@app.post("/break/kill")
async def break_kill():
    """Kill the loop outright, leaving the server up. Expect MISSING."""
    task = getattr(app.state, "loop_task", None)
    if task:
        task.cancel()
    return {"loop_cancelled": True, "expect": f"MISSING after ~{2 * EVERY}s"}


# async def, not def: FastAPI runs sync endpoints in a worker thread, which has no
# event loop, so touching tasks from one raises "no current event loop in thread".
@app.post("/fix")
async def fix():
    """Clear every injected failure and restart the loop if it died."""
    state.frozen = False
    state.error_rate = 0.0
    state.block_ms = 0
    state.raise_in_loop = False
    task = getattr(app.state, "loop_task", None)
    if task is None or task.done() or task.cancelled():
        app.state.loop_task = asyncio.create_task(batching_loop())
        restarted = True
    else:
        restarted = False
    return {"frozen": False, "loop_restarted": restarted}
