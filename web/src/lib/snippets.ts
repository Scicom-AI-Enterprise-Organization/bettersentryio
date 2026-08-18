/**
 * Integration snippets, generated per platform with the app's real key already in
 * them. Nothing here should need hand-editing beyond the monitor name.
 *
 * The `progress` option is the one that matters. Without it you get liveness only —
 * the service is reachable, the loop is beating. With it you also get stall detection,
 * which is the failure a health check cannot see: beats arriving while no work happens.
 */

import type { PlatformId } from "@/lib/platforms";

export type Block = { filename: string; language: string; code: string };
export type Step = { title: string; body: string; blocks: Block[] };
export type Integration = { install: Step | null; configure: Step; monitor: string };

export type Options = { progress: boolean };

/** The engine URL a *service* should call — not necessarily what this UI uses. */
export function ingestBase(): string {
  return process.env.BSIO_PUBLIC_URL ?? process.env.BSIO_API_URL ?? "http://localhost:9090";
}

/**
 * The app's Sentry-compatible DSN (D14): the stock sentry_sdk pointed at this
 * URL reports here instead of sentry.io. Same key as beats, numeric project id
 * in the path — exactly Sentry's own DSN shape, different host.
 */
export function dsnFor(app: { id: number; ingest_key: string }): string {
  const base = new URL(ingestBase());
  const port = base.port ? `:${base.port}` : "";
  return `${base.protocol}//${app.ingest_key}@${base.hostname}${port}/${app.id}`;
}

/**
 * Error tracking is the official sentry_sdk with our DSN — nothing of ours to
 * install or import (D14, docs/design/sentry-compat.md). The snippet mirrors
 * Sentry's own FastAPI onboarding, plus the one measured caveat: capturing a
 * dying background task needs AsyncioIntegration attached from INSIDE the
 * running loop, because uvicorn imports the module outside it.
 */
export function errorTracking(app: { id: number; ingest_key: string }): Step {
  const dsn = dsnFor(app);
  return {
    title: "Report errors — the official sentry_sdk",
    body:
      "Errors use the stock sentry_sdk, not our client: point its DSN here and every " +
      "unhandled exception, logger.error and its full context (locals, source lines, " +
      "breadcrumbs, request) lands in this project. Same init you would write for " +
      "sentry.io — only the DSN differs.",
    blocks: [
      {
        filename: "terminal",
        language: "bash",
        code: "pip install sentry-sdk",
      },
      {
        filename: "main.py",
        language: "python",
        code: `import sentry_sdk

sentry_sdk.init(
    dsn="${dsn}",
    environment="production",
    traces_sample_rate=0,   # errors only; transactions are dropped server-side
    send_default_pii=True,
)

from fastapi import FastAPI

app = FastAPI()


# Optional but measured-necessary for background tasks: a task that dies with
# its reference held is captured only if AsyncioIntegration attaches to the
# RUNNING loop — uvicorn imports this module outside it, so attach on startup.
@app.on_event("startup")
async def _sentry_asyncio():
    from sentry_sdk.integrations.asyncio import patch_asyncio
    patch_asyncio()


@app.get("/sentry-debug")
async def trigger_error():
    return 1 / 0  # shows up as one grouped issue; run it twice -> count=2`,
      },
    ],
  };
}

const SDK_INSTALL: Step = {
  title: "Install",
  body: "One stdlib-only file, served by the engine you report to. There is nothing to pip install, and no version to keep in sync.",
  blocks: [
    {
      filename: "terminal",
      language: "bash",
      code: "", // filled in per app so the URL is correct
    },
  ],
};

export function integration(
  app: { slug: string; ingest_key: string },
  platform: PlatformId,
  opts: Options = { progress: true },
): Integration {
  const base = ingestBase();
  const key = app.ingest_key;
  const monitor = platform === "celery" ? `${app.slug}-worker` : `${app.slug}-loop`;
  const p = opts.progress;

  const install = (): Step => ({
    ...SDK_INSTALL,
    blocks: [
      {
        filename: "terminal",
        language: "bash",
        code: `curl -O ${base}/clients/python/bettersentryio.py`,
      },
    ],
  });

  switch (platform) {
    case "fastapi":
      return {
        monitor,
        install: install(),
        configure: {
          title: "Configure",
          body: "Start the loop in the lifespan handler and beat from inside it, after the work. Beating from a separate timer would keep reporting healthy through exactly the failure you are trying to catch.",
          blocks: [
            {
              filename: "main.py",
              language: "python",
              code: `import asyncio
from contextlib import asynccontextmanager

from fastapi import FastAPI
from bettersentryio import Beat

bsio = Beat(
    base_url="${base}",
    key="${key}",
    environment="production",
)

batches_done = 0


async def batching_loop():
    global batches_done
    while True:
        await run_one_batch()
        batches_done += 1
${
  p
    ? `
        # progress is a counter that only goes up. Beats arriving while it sits
        # still means the loop is alive but doing nothing -> STALLED.
        bsio.beat(
            "${monitor}",
            progress=batches_done,
            every=30,
            grace=30,
            stall_window=180,
        )`
    : `
        # Liveness only: proves the loop is running, not that it is working.
        bsio.beat("${monitor}", every=30, grace=30)`
}

        await asyncio.sleep(30)


@asynccontextmanager
async def lifespan(app: FastAPI):
    task = asyncio.create_task(batching_loop())
    yield
    task.cancel()


app = FastAPI(lifespan=lifespan)`,
            },
          ],
        },
      };

    case "python":
      return {
        monitor,
        install: install(),
        configure: {
          title: "Configure",
          body: "One call per iteration, at the end of the iteration. It never raises and never blocks, so it is safe inside the loop you are protecting.",
          blocks: [
            {
              filename: "worker.py",
              language: "python",
              code: `from bettersentryio import Beat

bsio = Beat(base_url="${base}", key="${key}")

processed = 0
while True:
    processed += handle_next_item()
${
  p
    ? `    bsio.beat("${monitor}", progress=processed, every=60, stall_window=300)`
    : `    bsio.beat("${monitor}", every=60)`
}`,
            },
          ],
        },
      };

    case "celery":
      return {
        monitor,
        install: install(),
        configure: {
          title: "Configure",
          body: "Hook task_postrun so every completed task is a beat. A worker that is up but draining nothing — a wedged pool, a lost broker connection — shows as STALLED rather than healthy.",
          blocks: [
            {
              filename: "celery_app.py",
              language: "python",
              code: `from celery import Celery
from celery.signals import task_postrun

from bettersentryio import Beat

app = Celery("tasks", broker="redis://localhost:6379/0")
bsio = Beat(base_url="${base}", key="${key}")

tasks_done = 0


@task_postrun.connect
def report(**_):
    """Runs after every task, in the worker process."""
    global tasks_done
    tasks_done += 1
${
  p
    ? `    bsio.beat("${monitor}", progress=tasks_done, every=60, stall_window=300)`
    : `    bsio.beat("${monitor}", every=60)`
}`,
            },
          ],
        },
      };

    case "shell":
      return {
        monitor,
        install: null,
        configure: {
          title: "Configure",
          body: "No SDK. The && matters: beat only if the job actually succeeded, so a failing script reads as MISSING instead of quietly passing.",
          blocks: [
            {
              filename: "crontab",
              language: "cron",
              code: `*/5 * * * * /opt/jobs/export.sh && curl -fsS "${base}/api/0/beat/${monitor}?key=${key}&every=300&grace=120" >/dev/null`,
            },
            {
              filename: "with progress",
              language: "bash",
              code: p
                ? `# Pass something that grows — rows written, files shipped. A job that runs
# every night and exports zero rows is caught as STALLED.
ROWS=$(wc -l < /var/lib/export/out.csv)
curl -fsS "${base}/api/0/beat/${monitor}?key=${key}&every=86400&progress=$ROWS" >/dev/null`
                : `curl -fsS "${base}/api/0/beat/${monitor}?key=${key}&every=86400" >/dev/null`,
            },
          ],
        },
      };

    case "docker":
      return {
        monitor,
        install: install(),
        configure: {
          title: "Configure",
          body: "Pass the key in as environment so it is never baked into an image layer. The client reads BSIO_URL and BSIO_KEY with no arguments.",
          blocks: [
            {
              filename: "docker-compose.yml",
              language: "yaml",
              code: `services:
  ${app.slug}:
    environment:
      BSIO_URL: ${base}
      BSIO_KEY: \${BSIO_KEY:?put it in .env, not in the image}
      BSIO_ENV: production`,
            },
            {
              filename: ".env",
              language: "dotenv",
              code: `BSIO_KEY=${key}`,
            },
          ],
        },
      };

    case "kubernetes":
      return {
        monitor,
        install: install(),
        configure: {
          title: "Configure",
          body: "The key as a Secret, referenced by the pod. Nothing else changes — the monitor still registers itself on the first beat, so no CRD or config is needed up front.",
          blocks: [
            {
              filename: "secret.yaml",
              language: "yaml",
              code: `apiVersion: v1
kind: Secret
metadata:
  name: ${app.slug}-bsio
stringData:
  key: ${key}`,
            },
            {
              filename: "deployment.yaml",
              language: "yaml",
              code: `env:
  - name: BSIO_URL
    value: ${base}
  - name: BSIO_KEY
    valueFrom:
      secretKeyRef:
        name: ${app.slug}-bsio
        key: key`,
            },
          ],
        },
      };
  }
}
