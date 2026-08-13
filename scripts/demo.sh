#!/usr/bin/env bash
# End-to-end demo of the two incidents bettersentryio exists to catch.
#
#   1. A background loop dies while its HTTP health check stays green  (the TTS outage)
#   2. A loop keeps beating but stops making progress                  (the vLLM "super silence")
#
# Requires: a reachable PostgreSQL and python3. Timings are compressed so the
# whole run takes about a minute; production defaults are far less twitchy.
#
#   ./scripts/demo.sh
set -euo pipefail

DB_URL="${BSIO_DATABASE_URL:-postgres://localhost/bettersentryio_dev?sslmode=disable}"
PORT="${BSIO_PORT:-9090}"
SINK_PORT="${BSIO_SINK_PORT:-9099}"
BASE="http://localhost:${PORT}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cleanup() {
    [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true
    [[ -n "${SINK_PID:-}" ]] && kill "$SINK_PID" 2>/dev/null || true
}
trap cleanup EXIT

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
wall() { python3 "$ROOT/scripts/monitors.py" "$BASE"; }

step "building"
(cd "$ROOT" && go build -o /tmp/bsio-demo ./cmd/bettersentryio)

step "starting webhook sink on :$SINK_PORT"
python3 "$ROOT/scripts/webhook-sink.py" "$SINK_PORT" >/tmp/bsio-demo-sink.log 2>&1 &
SINK_PID=$!
sleep 1

step "starting bettersentryio on :$PORT (2s detector tick)"
/tmp/bsio-demo serve \
    --database-url "$DB_URL" \
    --listen ":$PORT" \
    --base-url "$BASE" \
    --tick-interval 2s \
    --alert-webhook "http://localhost:${SINK_PORT}/hook" \
    >/tmp/bsio-demo-server.log 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 30); do
    curl -fsS "$BASE/-/ready" >/dev/null 2>&1 && break
    sleep 0.5
done

KEY="$(psql "${DB_URL%%\?*}" -tAc 'select public_key from ingest_keys limit 1' | tr -d '[:space:]')"
echo "   ingest key: $KEY"
BEAT="$BASE/api/0/beat"

# ─────────────────────────────────────────────────────────────────────────────
step "INCIDENT 1 — the TTS outage: a loop that dies silently"
echo "   loop beats every 1s, declares itself as every=5s with a 5s grace window"
for i in 1 2 3; do
    curl -fsS "$BEAT/tts-batcher?key=$KEY&every=5&grace=5&progress=$i" -o /dev/null
    sleep 1
done
wall

echo
echo "   ...now the loop wedges. Its HTTP server would still answer /health with 200."
echo "   waiting 14s (5s interval + 5s grace + detector tick)"
sleep 14
wall

# ─────────────────────────────────────────────────────────────────────────────
step "the loop is restarted — recovery should be immediate, not a tick later"
curl -fsS "$BEAT/tts-batcher?key=$KEY&every=5&grace=5&progress=4" -o /dev/null
sleep 1
wall

# ─────────────────────────────────────────────────────────────────────────────
step "INCIDENT 2 — the vLLM 'super silence': beating, but doing no work"
echo "   beats keep arriving every 1s, but the progress counter never moves"
for i in $(seq 1 10); do
    curl -fsS "$BEAT/vllm-decode?key=$KEY&every=3&grace=6&stall_window=6&progress=500" -o /dev/null
    sleep 1
done
wall
echo
echo "   note: never MISSING — the beats arrived on time the whole way through."

step "work resumes (counter moves again)"
curl -fsS "$BEAT/vllm-decode?key=$KEY&every=3&grace=6&stall_window=6&progress=501" -o /dev/null
sleep 1
wall

# ─────────────────────────────────────────────────────────────────────────────
step "alerts delivered"
grep -v 'listening' /tmp/bsio-demo-sink.log || echo "   (none — that would be a bug)"

step "our own health endpoint"
curl -s "$BASE/-/health"

printf '\n\nMonitors wall: %s/   (server still up for a few seconds)\n' "$BASE"
sleep 3
