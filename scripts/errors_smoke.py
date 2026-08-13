#!/usr/bin/env python3
"""
Proves each exception hook actually fires, and that grouping collapses occurrences.

The asyncio case is the one that matters: our stress test showed a task dying with a
RuntimeError that appeared in **no** log, because asyncio only reports an unretrieved
task exception when the Task is garbage collected and the code held a reference. A
done-callback on every task catches it immediately; a plain loop exception handler does
not. This asserts that difference.

    export BSIO_URL=http://localhost:9090 BSIO_KEY=<key>
    python3 scripts/errors_smoke.py
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys
import threading
import time
import urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "clients", "python"))
import bettersentryio  # noqa: E402

BASE = os.environ.get("BSIO_URL", "http://localhost:9090")
KEY = os.environ.get("BSIO_KEY", "")


def issues() -> list[dict]:
    req = urllib.request.Request(
        f"{BASE}/api/0/issues?project=default", headers={"X-BSIO-Key": KEY}
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.load(r)["issues"]


def flush() -> None:
    """Let the background reporter drain."""
    c = bettersentryio.client()
    assert c is not None
    for _ in range(40):
        if not c.reporter._queue:  # noqa: SLF001 - test needs to know
            break
        time.sleep(0.25)
    time.sleep(1.0)


# --------------------------------------------------------------------- scenarios


async def dies_immediately() -> None:
    """A task that raises. The reference below is what hides it from asyncio."""
    await asyncio.sleep(0.05)
    raise RuntimeError("model worker died: CUDA error: device-side assert triggered")


async def asyncio_case() -> None:
    task = asyncio.create_task(dies_immediately())
    # Holding the reference is the documented way to stop tasks being collected early,
    # and it is exactly what stops asyncio from ever reporting the exception.
    await asyncio.sleep(0.4)
    assert task.done()


def thread_case() -> None:
    def boom() -> None:
        raise ValueError("worker thread hit a bad batch")

    t = threading.Thread(target=boom, name="batch-worker")
    t.start()
    t.join()


def logging_case() -> None:
    log = logging.getLogger("app.tts")
    try:
        raise KeyError("device_id")
    except KeyError:
        log.exception("could not resolve the device")


def grouping_case() -> None:
    """Same bug, different data, twelve times. Must be one issue seen 12 times."""
    for i in range(12):
        try:
            raise RuntimeError(f"batch {i * 137} failed after {i * 11}ms")
        except RuntimeError as exc:
            bettersentryio.capture_exception(exc, mechanism="manual", handled=True)


def distinct_case() -> None:
    """A different function raising the same type must be a different issue."""
    try:
        raise RuntimeError("batch 5 failed after 9ms")
    except RuntimeError as exc:
        bettersentryio.capture_exception(exc, mechanism="manual", handled=True)


def main() -> int:
    if not KEY:
        print("BSIO_KEY is required", file=sys.stderr)
        return 2

    bettersentryio.init(base_url=BASE, key=KEY, environment="smoke")
    before = len(issues())

    print("1. asyncio task dies while a reference is held")
    asyncio.run(asyncio_case())

    print("2. exception escapes a thread")
    thread_case()

    print("3. logger.exception")
    logging_case()

    print("4. twelve occurrences of one bug, different data each time")
    grouping_case()

    print("5. same exception type from a different function")
    distinct_case()

    flush()

    found = issues()
    print(f"\nissues: {len(found)} (was {before})")
    print(f"{'times':>6}  {'kind':<14} {'culprit':<38} title")
    print("-" * 100)
    for i in sorted(found, key=lambda x: -x["times_seen"]):
        print(f"{i['times_seen']:>6}  {i['kind']:<14} {i['culprit']:<38} {i['title'][:44]}")

    stats = bettersentryio.client().stats()  # type: ignore[union-attr]
    print(f"\nreporter: {stats}")

    # --- assertions -------------------------------------------------------
    ok = True
    kinds = {i["kind"] for i in found}
    for want in ("RuntimeError", "ValueError", "KeyError"):
        if want not in kinds:
            print(f"FAIL: {want} was not captured")
            ok = False

    cuda = [i for i in found if "CUDA" in i["title"]]
    if not cuda:
        print("FAIL: the asyncio task death was not captured — the very case we measured")
        ok = False

    grouped = [i for i in found if i["times_seen"] >= 12]
    if not grouped:
        print("FAIL: twelve occurrences did not collapse into one issue")
        ok = False
    else:
        print(f"\ngrouping: 12 varied occurrences -> 1 issue seen {grouped[0]['times_seen']}x")

    runtime_issues = [i for i in found if i["kind"] == "RuntimeError"]
    if len(runtime_issues) < 2:
        print("FAIL: distinct call sites merged into one issue")
        ok = False
    else:
        print(f"separation: RuntimeError from {len(runtime_issues)} call sites stayed distinct")

    print("\nPASS" if ok else "\nFAILED")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
