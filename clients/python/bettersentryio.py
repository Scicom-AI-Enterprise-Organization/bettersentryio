"""
bettersentryio heartbeat client — stdlib only, fire-and-forget.

Drop this file next to your service and import it. There is nothing to pip install.

    from bettersentryio import Beat

    bsio = Beat()                     # reads BSIO_URL and BSIO_KEY from the environment
    bsio.beat("tts-batcher", progress=batches_done, every=30)

Three properties matter more than features here, because this code runs inside the
loop you are trying to protect:

1. **It never raises.** A monitoring call that can throw turns an observability
   problem into an outage. Every failure is swallowed (and counted).
2. **It never blocks the caller.** Beats are handed to a single background worker
   thread. The caller returns immediately, whatever the network is doing.
3. **It cannot pile up.** The worker holds one pending beat per monitor, newest
   wins. A loop beating faster than the network can keep up drops stale beats
   instead of growing a queue or spawning threads.
"""

from __future__ import annotations

import atexit
import os
import threading
import urllib.parse
import urllib.request
from typing import Dict, Optional, Tuple

__all__ = ["Beat"]

_SHUTDOWN = object()


class Beat:
    """A heartbeat sender. Create one per process and share it."""

    def __init__(
        self,
        base_url: Optional[str] = None,
        key: Optional[str] = None,
        environment: Optional[str] = None,
        timeout: float = 2.0,
    ) -> None:
        self.base_url = (base_url or os.environ.get("BSIO_URL", "")).rstrip("/")
        self.key = key or os.environ.get("BSIO_KEY", "")
        self.environment = environment or os.environ.get("BSIO_ENV") or None
        self.timeout = timeout

        self.sent = 0
        self.failed = 0
        self.dropped = 0
        self.last_error: Optional[str] = None

        # One pending beat per monitor, newest wins.
        self._pending: Dict[Tuple[str, Optional[str]], str] = {}
        self._lock = threading.Lock()
        self._wake = threading.Event()
        self._closed = False
        self._worker = threading.Thread(
            target=self._run, name="bettersentryio", daemon=True
        )
        self._worker.start()
        atexit.register(self.close)

    # ------------------------------------------------------------------ public

    def beat(
        self,
        monitor: str,
        progress: Optional[int] = None,
        every: Optional[int] = None,
        grace: Optional[int] = None,
        stall_window: Optional[int] = None,
        environment: Optional[str] = None,
    ) -> None:
        """
        Record one heartbeat. Returns immediately; never raises.

        monitor      slug, created on the first beat. e.g. "tts-batcher"
        progress     a counter that only goes up (batches, tokens, rows). Supplying
                     it is what enables stall detection: beats arriving while this
                     stands still means the loop is alive but doing no work.
        every        seconds you expect between beats
        grace        extra seconds before the monitor is called MISSING
        stall_window seconds of frozen progress before it is called STALLED
        """
        if not self.base_url or not self.key:
            # Misconfiguration must not crash the host service; it is visible in stats().
            self.dropped += 1
            self.last_error = "BSIO_URL or BSIO_KEY is not set"
            return

        env = environment or self.environment
        params = {"key": self.key}
        if progress is not None:
            params["progress"] = str(int(progress))
        if every is not None:
            params["every"] = str(int(every))
        if grace is not None:
            params["grace"] = str(int(grace))
        if stall_window is not None:
            params["stall_window"] = str(int(stall_window))
        if env:
            params["env"] = env

        url = "{}/api/0/beat/{}?{}".format(
            self.base_url,
            urllib.parse.quote(monitor, safe=""),
            urllib.parse.urlencode(params),
        )

        with self._lock:
            if self._closed:
                return
            key = (monitor, env)
            if key in self._pending:
                self.dropped += 1  # superseded before it went out
            self._pending[key] = url
        self._wake.set()

    def stats(self) -> Dict[str, object]:
        """Counters, for exposing on your own health endpoint."""
        return {
            "sent": self.sent,
            "failed": self.failed,
            "dropped": self.dropped,
            "last_error": self.last_error,
        }

    def close(self, timeout: float = 3.0) -> None:
        """Flush pending beats and stop the worker. Safe to call twice."""
        with self._lock:
            if self._closed:
                return
            self._closed = True
        self._wake.set()
        self._worker.join(timeout=timeout)

    # ------------------------------------------------------------------ worker

    def _run(self) -> None:
        while True:
            self._wake.wait(timeout=1.0)
            self._wake.clear()

            with self._lock:
                batch = list(self._pending.values())
                self._pending.clear()
                closed = self._closed

            for url in batch:
                self._send(url)

            if closed:
                return

    def _send(self, url: str) -> None:
        try:
            req = urllib.request.Request(url, method="POST")
            req.add_header("User-Agent", "bettersentryio-python")
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                if 200 <= resp.status < 300:
                    self.sent += 1
                else:
                    self.failed += 1
                    self.last_error = "HTTP {}".format(resp.status)
        except Exception as exc:  # noqa: BLE001 - a beat must never escape
            self.failed += 1
            self.last_error = "{}: {}".format(type(exc).__name__, exc)
