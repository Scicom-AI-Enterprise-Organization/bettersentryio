#!/usr/bin/env bash
#
# Start everything on the host: Postgres, the engine, the UI. No Docker.
#
# Docker is optional for this project and has been a liability in practice — a full disk
# corrupted its image store twice and took the daemon with it. The engine is a static
# binary and Postgres is one brew service, so the native path is the shorter one.
#
#   ./scripts/dev.sh up      start everything, print the URLs
#   ./scripts/dev.sh down    stop the engine and the UI (Postgres keeps running)
#   ./scripts/dev.sh status  what is listening, and is it healthy
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PG_BIN=/opt/homebrew/opt/postgresql@16/bin
# 5432 is not always ours. On a machine where another project's container already holds it,
# a hardcoded port silently points every psql/createdb here at *their* database, so the port
# is overridable — set BSIO_PG_PORT in .env (and in the cluster's postgresql.conf) to match.
[ -f .env ] && . ./.env
PG_PORT="${BSIO_PG_PORT:-5432}"
PG_DSN="postgres://bettersentryio:bettersentryio@127.0.0.1:$PG_PORT/bettersentryio?sslmode=disable"
ENGINE_PORT=9090
WEB_PORT=3100
ENGINE_LOG=/tmp/bsio-engine.log
WEB_LOG=/tmp/bsio-web.log

export PATH="$PG_BIN:$PATH"

say()  { printf '  %s\n' "$*"; }
fail() { printf '  !! %s\n' "$*" >&2; }

port_pid() { lsof -nP -tiTCP:"$1" -sTCP:LISTEN 2>/dev/null | head -1; }

wait_http() { # url, seconds
  local i
  for ((i = 0; i < ${2:-30}; i++)); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' -m 2 "$1")" = "200" ] && return 0
    sleep 1
  done
  return 1
}

ensure_postgres() {
  if pg_isready -h 127.0.0.1 -p "$PG_PORT" >/dev/null 2>&1; then
    say "postgres already running"
  else
    say "starting postgres@16"
    brew services start postgresql@16 >/dev/null 2>&1
    local i
    for ((i = 0; i < 30; i++)); do
      pg_isready -h 127.0.0.1 -p "$PG_PORT" >/dev/null 2>&1 && break
      sleep 1
    done
    pg_isready -h 127.0.0.1 -p "$PG_PORT" >/dev/null 2>&1 || { fail "postgres did not start"; return 1; }
  fi

  psql -h 127.0.0.1 -p "$PG_PORT" -d postgres -tAc \
    "select 1 from pg_roles where rolname='bettersentryio'" | grep -q 1 ||
    psql -h 127.0.0.1 -p "$PG_PORT" -d postgres -qc \
      "create role bettersentryio login password 'bettersentryio'"

  psql -h 127.0.0.1 -p "$PG_PORT" -d postgres -tAc \
    "select 1 from pg_database where datname='bettersentryio'" | grep -q 1 ||
    createdb -h 127.0.0.1 -p "$PG_PORT" -O bettersentryio bettersentryio

  # Postgres 15+ revoked CREATE on schema public from everyone but the database owner,
  # so a database owned by your login account leaves the app role unable to migrate.
  psql -h 127.0.0.1 -p "$PG_PORT" -d postgres -qc \
    "alter database bettersentryio owner to bettersentryio" 2>/dev/null
}

engine_env() {
  # The operator token lives in .env (gitignored). Generate one on first run rather than
  # shipping a default, so an unset token can never quietly become a shared secret.
  if [ ! -f .env ]; then
    say "writing .env with a fresh operator token"
    printf '# Operator token for the engine admin API. Gitignored.\nBSIO_API_TOKEN=%s\n' \
      "$(openssl rand -hex 24)" > .env
  fi
  # shellcheck disable=SC1091
  . ./.env
  export BSIO_API_TOKEN
  export BSIO_DATABASE_URL="$PG_DSN"
}

up() {
  ensure_postgres || return 1
  engine_env

  if [ -n "$(port_pid $ENGINE_PORT)" ]; then
    say "engine already on :$ENGINE_PORT"
  else
    say "building engine"
    go build -o bin/bettersentryio ./cmd/bettersentryio || { fail "build failed"; return 1; }
    say "starting engine on :$ENGINE_PORT"
    # base-url is where deep links (alerts, /events/search) send people: the UI,
    # not the engine port.
    nohup ./bin/bettersentryio serve --tick-interval 5s \
      --base-url "http://localhost:$WEB_PORT" >"$ENGINE_LOG" 2>&1 &
    wait_http "http://localhost:$ENGINE_PORT/-/health" 30 ||
      { fail "engine unhealthy — see $ENGINE_LOG"; tail -3 "$ENGINE_LOG"; return 1; }
  fi

  if [ -d web/node_modules ]; then
    if [ -n "$(port_pid $WEB_PORT)" ]; then
      say "UI already on :$WEB_PORT"
    else
      # `next dev`, not `next start`: start serves a production build, so every edit
      # needs an explicit `npm run build` and a restart — which is not what anyone
      # means by a dev stack. BSIO_WEB_MODE=start opts back in to serving the built
      # bundle (and needs `npm run build` in web/ first).
      if [ "${BSIO_WEB_MODE:-dev}" = "start" ]; then
        say "starting UI on :$WEB_PORT (production build, no hot reload)"
        ( cd web && set -a && . ./.env && set +a && \
          nohup npx next start -p "$WEB_PORT" >"$WEB_LOG" 2>&1 & )
      else
        say "starting UI on :$WEB_PORT (hot reload)"
        ( cd web && set -a && . ./.env && set +a && \
          nohup npx next dev -p "$WEB_PORT" >"$WEB_LOG" 2>&1 & )
      fi
      # A cold `next dev` compiles the route on first request, so allow longer.
      wait_http "http://localhost:$WEB_PORT/login" 90 || fail "UI did not come up — see $WEB_LOG"
    fi
  else
    say "web/node_modules missing — run 'npm install' in web/ to get the UI"
  fi

  printf '\n'
  say "engine   http://localhost:$ENGINE_PORT/-/health"
  say "UI       http://localhost:$WEB_PORT  (admin@scicom.com.my / 12345 — development only)"
  say "key      $(psql -h 127.0.0.1 -p "$PG_PORT" -U bettersentryio -d bettersentryio -tAc \
                  "select public_key from ingest_keys where revoked_at is null order by id limit 1" \
                  2>/dev/null | tr -d '[:space:]')"
}

down() {
  for port in $ENGINE_PORT $WEB_PORT; do
    pid="$(port_pid "$port")"
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null && say "stopped :$port (pid $pid)"
    else
      say ":$port was not listening"
    fi
  done
  say "postgres left running — 'brew services stop postgresql@16' to stop it too"
}

status() {
  pg_isready -h 127.0.0.1 -p "$PG_PORT" >/dev/null 2>&1 &&
    say "postgres  ready" || say "postgres  DOWN"
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://localhost:$ENGINE_PORT/-/health")"
  say "engine    :$ENGINE_PORT -> ${code}"
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 3 "http://localhost:$WEB_PORT/login")"
  say "UI        :$WEB_PORT -> ${code}"
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  status) status ;;
  *) printf 'usage: %s {up|down|status}\n' "$0" >&2; exit 2 ;;
esac
