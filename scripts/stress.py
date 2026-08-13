#!/usr/bin/env python3
"""
Hammer a service while watching what bettersentryio reports about it.

The point is not throughput. It is to put the two side by side — HTTP behaviour on the
left, monitor state on the right — so it is obvious which failures inside-out monitoring
catches and which it cannot see. Both answers are useful; a monitoring tool you believe
covers more than it does is worse than one whose gaps you know.

    export BSIO_URL=http://localhost:9090
    export BSIO_KEY=<the app's ingest key>
    python3 scripts/stress.py --target http://127.0.0.1:8080 --monitor tts-batcher \
        --scenario errors --seconds 60

Scenarios map onto the failure-injection endpoints of examples/fastapi-tts:

  baseline  healthy load, nothing injected
  errors    a fraction of requests return HTTPException(500) — deliberate, so not a crash
  crash     a fraction raise a genuine unhandled exception — captured as an issue
  block     each request burns CPU synchronously, starving the event loop
  raise     the loop raises and its task dies; the server keeps serving

Stdlib only, so it runs anywhere the client does.
"""

from __future__ import annotations

import argparse
import json
import os
import statistics
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor


def post(url: str, timeout: float = 10.0) -> tuple[int, float]:
    """Returns (status, seconds). 0 means the request never got a status."""
    started = time.perf_counter()
    req = urllib.request.Request(url, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            r.read()
            return r.status, time.perf_counter() - started
    except urllib.error.HTTPError as e:
        e.read()
        return e.code, time.perf_counter() - started
    except Exception:
        return 0, time.perf_counter() - started


def issue_count(base: str, key: str, project: str) -> int:
    """Unresolved issues, so the verdict can tell 'not detected' from 'detected as an error'."""
    url = f"{base.rstrip('/')}/api/0/issues?project={urllib.parse.quote(project)}"
    req = urllib.request.Request(url, headers={"X-BSIO-Key": key})
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            return int(json.load(r)["counts"]["events"])
    except Exception:
        return -1


def engine_state(base: str, key: str, monitor: str) -> dict:
    """One monitor's state, as the engine sees it."""
    url = f"{base.rstrip('/')}/api/0/overview"
    req = urllib.request.Request(url, headers={"X-BSIO-Key": key})
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            body = json.load(r)
    except Exception as e:  # the engine being down is itself a result worth printing
        return {"status": f"engine unreachable ({e.__class__.__name__})"}
    for m in body.get("monitors", []):
        if m["slug"] == monitor:
            return m
    return {"status": "no such monitor"}


class Load:
    """Fixed-concurrency load, with a stop flag."""

    def __init__(self, url: str, concurrency: int) -> None:
        self.url = url
        self.concurrency = concurrency
        self.stop = threading.Event()
        self.codes: Counter[int] = Counter()
        self.latencies: list[float] = []
        self._lock = threading.Lock()

    def _worker(self) -> None:
        while not self.stop.is_set():
            code, secs = post(self.url)
            with self._lock:
                self.codes[code] += 1
                self.latencies.append(secs)

    def run(self) -> ThreadPoolExecutor:
        pool = ThreadPoolExecutor(max_workers=self.concurrency)
        for _ in range(self.concurrency):
            pool.submit(self._worker)
        return pool

    def window(self) -> tuple[Counter[int], list[float]]:
        """Take and reset the counters, for per-tick reporting."""
        with self._lock:
            codes, lat = self.codes, self.latencies
            self.codes, self.latencies = Counter(), []
        return codes, lat


def pct(values: list[float], p: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    idx = min(len(ordered) - 1, int(round((p / 100) * (len(ordered) - 1))))
    return ordered[idx]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", default="http://127.0.0.1:8080")
    ap.add_argument("--engine", default=os.environ.get("BSIO_URL", "http://localhost:9090"))
    ap.add_argument("--key", default=os.environ.get("BSIO_KEY", ""))
    ap.add_argument("--monitor", default=os.environ.get("BSIO_MONITOR", "tts-batcher"))
    ap.add_argument("--project", default=os.environ.get("BSIO_PROJECT", "default"))
    ap.add_argument("--scenario", default="baseline",
                    choices=["baseline", "errors", "crash", "block", "raise"])
    ap.add_argument("--seconds", type=int, default=60)
    ap.add_argument("--concurrency", type=int, default=24)
    ap.add_argument("--error-rate", type=float, default=0.5)
    ap.add_argument("--crash-rate", type=float, default=0.5)
    ap.add_argument("--block-ms", type=int, default=250)
    args = ap.parse_args()

    if not args.key:
        print("BSIO_KEY is required to read monitor state", file=sys.stderr)
        return 2

    target = args.target.rstrip("/")

    # Clear anything left over, then inject this scenario's failure.
    post(f"{target}/fix")
    injected = "nothing"
    if args.scenario == "errors":
        post(f"{target}/break/errors?rate={args.error_rate}")
        injected = f"{args.error_rate:.0%} of requests raise 500"
    elif args.scenario == "crash":
        post(f"{target}/break/crash?rate={args.crash_rate}")
        injected = f"{args.crash_rate:.0%} of requests raise an unhandled exception"
    elif args.scenario == "block":
        post(f"{target}/break/block?ms={args.block_ms}")
        injected = f"{args.block_ms}ms sync CPU per request"
    elif args.scenario == "raise":
        post(f"{target}/break/raise")
        injected = "the loop raises and its task dies"

    events_before = issue_count(args.engine, args.key, args.project)

    print(f"scenario : {args.scenario} — {injected}")
    print(f"load     : {args.concurrency} concurrent POST {target}/synthesize for {args.seconds}s")
    print(f"watching : {args.monitor} via {args.engine}")
    print()
    print(f"{'t':>5}  {'req/s':>7}  {'5xx':>6}  {'fail':>5}  {'p50':>7}  {'p95':>7}  "
          f"{'bsio':<9} {'progress':>8}  {'beats':>6}")
    print("-" * 78)

    load = Load(f"{target}/synthesize", args.concurrency)
    pool = load.run()

    timeline = []
    started = time.time()
    try:
        while time.time() - started < args.seconds:
            time.sleep(5)
            elapsed = int(time.time() - started)
            codes, lat = load.window()
            total = sum(codes.values())
            ok = codes.get(200, 0)
            server_err = sum(n for c, n in codes.items() if 500 <= c < 600)
            dead = codes.get(0, 0)
            m = engine_state(args.engine, args.key, args.monitor)
            row = {
                "t": elapsed,
                "rps": total / 5,
                "5xx": server_err,
                "failed": dead,
                "p50": pct(lat, 50),
                "p95": pct(lat, 95),
                "status": m.get("status", "?"),
                "progress": m.get("last_progress"),
                "beats": m.get("beats_24h"),
            }
            timeline.append(row)
            print(f"{elapsed:>4}s  {row['rps']:>7.1f}  {server_err:>6}  {dead:>5}  "
                  f"{row['p50']*1000:>6.0f}m  {row['p95']*1000:>6.0f}m  "
                  f"{str(row['status']):<9} {str(row['progress']):>8}  {str(row['beats']):>6}")
    finally:
        load.stop.set()
        pool.shutdown(wait=False)

    # Let the detector's sweep catch up before the verdict: absence needs a tick.
    print("\nload stopped; letting the detector settle…")
    for _ in range(6):
        time.sleep(5)
        m = engine_state(args.engine, args.key, args.monitor)
        print(f"       bsio={m.get('status')}  progress={m.get('last_progress')}")

    statuses = {r["status"] for r in timeline}
    total_5xx = sum(r["5xx"] for r in timeline)
    events_after = issue_count(args.engine, args.key, args.project)
    new_events = events_after - events_before if events_before >= 0 <= events_after else -1

    print()
    print(f"served    : ~{sum(r['rps'] for r in timeline) * 5:.0f} requests, {total_5xx} of them 5xx")
    print(f"monitors  : {', '.join(sorted(statuses))}")
    print(f"issues    : {new_events if new_events >= 0 else '?'} new error events captured")

    healthy_throughout = statuses <= {"ok"}
    if not healthy_throughout:
        print("verdict   : DETECTED by the monitor — the failure reached the loop.")
    elif new_events > 0:
        print("verdict   : DETECTED as errors — the loop was fine, the code raised, and the")
        print("            exception hooks reported it with a stacktrace.")
    elif total_5xx:
        print("verdict   : NOT DETECTED. The loop kept working and nothing raised, so neither")
        print("            half saw it. HTTPException is the usual reason: a status you chose")
        print("            to return is not a crash, and is deliberately not reported.")
    else:
        print("verdict   : healthy throughout, nothing to detect.")

    post(f"{target}/fix")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
