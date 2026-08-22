"""
The smallest thing that proves error ingest works: a stock sentry_sdk pointed at
bettersentryio and one endpoint that raises.

No bettersentryio client, no snippet, no shim — the DSN is the only change from a
service that reports to sentry.io, which is the whole claim of D14. Hit
/sentry-debug and the ZeroDivisionError shows up under Apps → the app → Issues.

    pip install fastapi uvicorn sentry-sdk
    uvicorn main:app --port 8090
    curl localhost:8090/sentry-debug
"""

import sentry_sdk

sentry_sdk.init(
    dsn="http://c4cef10f170a4401355f8f41ab7aed8c@localhost:9090/1",
    environment="production",
    traces_sample_rate=0,   # errors only; transactions are dropped server-side
    send_default_pii=True,
)

from fastapi import FastAPI

app = FastAPI()


@app.get("/sentry-debug")
async def trigger_error():
    return 1 / 0
