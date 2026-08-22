# The smallest FastAPI service that reports errors

One file, one endpoint, a stock `sentry_sdk`. It exists to prove the claim in D14 with
nothing else in the way: **the DSN is the only difference** between reporting to
sentry.io and reporting here — no bettersentryio client, no snippet, no shim.

```bash
python3 -m venv .venv && ./.venv/bin/pip install fastapi uvicorn sentry-sdk
./.venv/bin/uvicorn main:app --port 8090

curl localhost:8090/sentry-debug        # raises ZeroDivisionError
```

The issue appears under **Apps → the app → Errors** within a second or two, with the
stacktrace, the local variables around the failing frame, and the tags the SDK derives
(`level`, `environment`, `release`, `server_name`, `handled: no`).

## The DSN

```python
dsn="http://<ingest key>@localhost:9090/1"
```

The key is the app's **ingest key** — Apps → the app → Setup shows it, filled into a
snippet. The trailing `/1` is the project id, and the host is the engine, not the UI.
A DSN public key is not a secret: it authorises writing events and nothing else.

## What it does not do

`examples/fastapi-tts` is the interesting one — a background batching loop, heartbeats
with a progress counter, and endpoints that break it on purpose so you can watch
`/health` answer 200 while the work has stopped. This example only covers error
capture.

Note that no asyncio integration is wired up here. A task that dies while something
still holds a reference to it is reported only if `AsyncioIntegration` is attached to
the *running* loop, and uvicorn imports this module outside it — so a service with
background tasks needs that patch applied on startup. This one has no background
tasks, so it does not.
