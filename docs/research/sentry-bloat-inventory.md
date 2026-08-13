# Sentry self-hosted: infrastructure & bloat inventory (research notes)

> Source: measured directly on `getsentry/sentry` @ `b815e2e0` (2026-08-07, shallow clone, 310 MB).
> Paths relative to the sentry repo root. These numbers are the quantitative case for bettersentryio.

## Headline numbers

| Metric | Count | Source |
|---|---|---|
| Devservice entries (containers + daemons) | **42** (21 containers + 21 Sentry-run daemons) | `devservices/config.yml` |
| External Git repos pulled as dependencies | **12** | `repo_link:` entries, `devservices/config.yml` |
| Services in `full` dev mode | 31 | `devservices/config.yml` (`modes.full`) |
| Kafka topics | **52** (10 DLQs) | `src/sentry/conf/types/kafka_definition.py:12` |
| Kafka consumer definitions | 20 | `src/sentry/consumers/__init__.py:307` |
| Storage engines in the data plane | **6** (Postgres, ClickHouse, Kafka, Redis, Memcached, object store/Bigtable) | devservices + `conf/server.py` |
| Python direct runtime deps | **112** (250 resolved) | `pyproject.toml:6`, `uv.lock` |
| JS runtime deps | 171 (~2,426 resolved) | `package.json`, `pnpm-lock.yaml` |
| Python LOC (`src/sentry`) | **765,592** in 4,584 files | `wc -l` |
| Frontend LOC (`static/app`) | **515,438** in 8,116 files | `wc -l` |
| Test LOC | 873,501 | `tests/` |
| Django models (concrete, silo-decorated) | **314** (297 tables) | grep `@cell_silo_model`/`@control_silo_model` |
| API endpoint classes | **701** (937 URL patterns; 11 publicly documented) | grep + `api-docs/openapi.json` |
| Background tasks | **313** across 64 queues | `@instrumented_task`, `taskworker/namespaces.py` |
| Scheduled cron entries | 66 | `conf/server.py:1026,1244` |
| Runtime options / feature flags / settings | 651 / 254 / 539 | `options/defaults.py`, `features/temporary.py`, `conf/server.py` |
| Migration history depth | #1151 | `migrations_lockfile.txt` |
| Node heap needed just to build the frontend | 5 GB | `.envrc:96` (`--max-old-space-size=5120`) |

Notes:
- Celery/RabbitMQ are gone in this version — replaced by `taskbroker` (a separate **Rust** gRPC
  broker) + taskworker fleets. The queue dependency didn't disappear; it changed shape.
- Kafka/ClickHouse/Zookeeper aren't even in sentry's own devservices file — they arrive
  transitively via the `snuba` repo. Topic names are contractually bound to an external
  `sentry-kafka-schemas` package (cross-repo coupling).
- Redis is 25 logically distinct clusters in config (`conf/server.py:180-207`).
- Minimum RAM is documented nowhere in this repo — hardware requirements are externalized to
  `getsentry/self-hosted`. Footprint is not a first-class product property.
- Even *minimal* dev mode = postgres + snuba, which transitively means ClickHouse + Kafka.

## The comparison that motivates bettersentryio

| | Sentry self-hosted | bettersentryio target |
|---|---|---|
| Processes | 42 (21 containers + 21 daemons) | **1 binary** |
| Storage engines | 6 | **1** (PostgreSQL) |
| Repos involved | 12 | 1 |
| Backend LOC | ~766k Python (+Rust relay/taskbroker, Go uptime/vroom) | **< 10k** |
| Queue | Kafka, 52 topics | none (in-process channels) |
| Detection clock | Kafka-timestamp-derived distributed clock | one `time.Ticker` |
| RAM | multi-GB fleet | **< 100 MB** |
| Features | errors, tracing, profiling, replays, uptime, crons, metrics, feedback, AI autofix… | errors + crons/heartbeats + stall detection + alerts. Nothing else. |

The point is not that Sentry is badly built — it's built for sentry.io's scale and product
breadth. The point is that **~95% of that machinery is unrelated to "tell me when my service
breaks or my loop dies"**, which is the whole job for a small team's internal deployment.
