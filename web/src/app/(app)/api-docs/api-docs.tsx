"use client";

/**
 * API reference, modelled on gpuplatform's: a searchable endpoint nav on the left,
 * and per-endpoint sections that split into docs + copyable samples. The samples
 * track the real wire shapes — change an endpoint's shape and its entry in
 * ENDPOINTS below is part of the change.
 *
 * House style, not gpuplatform's: method and status badges use the status tokens
 * (`bg-status-X/15 text-status-X`), because Tailwind tint pairs do not survive the
 * theme switch (DEVELOPING, design conventions).
 */

import { useMemo, useState } from "react";
import Link from "next/link";
import { Check, Copy, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

function CopyBtn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="ghost"
      size="sm"
      className="absolute right-1.5 top-1.5 h-7 w-7 p-0 opacity-50 hover:opacity-100"
      onClick={() => {
        navigator.clipboard?.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      title={copied ? "Copied" : "Copy"}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
    </Button>
  );
}

function CodeBlock({ children, label }: { children: string; label?: string }) {
  return (
    <div className="relative rounded-md border border-border bg-card p-3">
      {label && (
        <p className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </p>
      )}
      <pre className="scrollbar-thin overflow-x-auto pr-8 font-mono text-xs leading-relaxed text-foreground/90">
        {children}
      </pre>
      <CopyBtn text={children} />
    </div>
  );
}

type Method = "GET" | "POST" | "PUT" | "DELETE";

const METHOD_TONE: Record<Method, string> = {
  GET: "bg-status-init/15 text-status-init",
  POST: "bg-status-active/15 text-status-active",
  PUT: "bg-status-idle/15 text-status-idle",
  DELETE: "bg-status-down/15 text-status-down",
};

function MethodBadge({ method }: { method: Method }) {
  return (
    <span
      className={`inline-flex h-5 items-center rounded px-1.5 font-mono text-[10px] font-semibold tracking-wider ${METHOD_TONE[method]}`}
    >
      {method}
    </span>
  );
}

function StatusBadge({ code, label }: { code: number; label: string }) {
  const tone = code < 300 ? "bg-status-active/15 text-status-active" : "bg-status-down/15 text-status-down";
  return (
    <span className={`inline-flex h-5 items-center rounded px-1.5 font-mono text-[10px] font-semibold ${tone}`}>
      {code} {label}
    </span>
  );
}

/* ---- the reference data --------------------------------------------------------- */

type Endpoint = {
  id: string;
  group: string;
  method: Method;
  path: string;
  title: string;
  auth: "ingest key" | "any read credential" | "operator / session" | "none";
  description: React.ReactNode;
  parameters?: Array<{ name: string; in: "query" | "body" | "path"; type: string; required?: boolean; doc: React.ReactNode }>;
  request: string;
  responses: Array<{ code: number; codeLabel: string; doc?: React.ReactNode; sample: string }>;
};

type Group = { id: string; title: string; blurb: React.ReactNode };

const GROUPS: Group[] = [
  {
    id: "auth",
    title: "Authentication",
    blurb: (
      <>
        Three credentials, all sent as <code>Authorization: Bearer …</code> (ingest keys also accept{" "}
        <code>X-BSIO-Key</code> or <code>?key=</code>). An <b>ingest key</b> (per app, from Settings) writes events
        and heartbeats. An <b>API token</b> (<code>bsiot_…</code>, from Settings → API tokens) reads everything and
        changes nothing — it is what a dashboard holds. The <b>operator token</b> administers and stays in the
        engine&apos;s environment.
      </>
    ),
  },
  {
    id: "ingest",
    title: "Ingest",
    blurb: (
      <>
        The write path. Errors arrive either through the stock <code>sentry_sdk</code> pointed at this engine (the
        DSN on the app&apos;s Settings page — no code change beyond the DSN) or through the native event endpoint.
        Heartbeats are one GET/POST per loop iteration, downstream of the work.
      </>
    ),
  },
  {
    id: "errors",
    title: "Errors & analytics",
    blurb: (
      <>
        Windowed reads. Every endpoint here takes <code>statsPeriod=30d</code>-style spans or explicit{" "}
        <code>start</code>/<code>end</code> instants (RFC3339, zoneless, or unix seconds), matching what the charts
        in this UI send.
      </>
    ),
  },
  {
    id: "lookup",
    title: "Correlation lookup",
    blurb: (
      <>
        From an identifier in a log line or a trace panel to the exact error it produced. Searches per-event
        identity — issue tags only keep the latest event&apos;s tags, so this is the only honest way to find a
        request-scoped id.
      </>
    ),
  },
  {
    id: "monitors",
    title: "Monitors & incidents",
    blurb: <>The loop-liveness surface: every monitor&apos;s state, and the incident log behind the alerts.</>,
  },
  {
    id: "apps",
    title: "Apps",
    blurb: <>Projects. Creating one mints its ingest key; deleting one removes everything under it.</>,
  },
  {
    id: "ops",
    title: "Operations",
    blurb: (
      <>
        Tokens, the audit log, retention, and the probes. <code>/-/health</code>, <code>/-/ready</code> and{" "}
        <code>/-/metrics</code> are unauthenticated and meant for in-cluster use.
      </>
    ),
  },
];

const ENDPOINTS: Endpoint[] = [
  /* ---- auth -------------------------------------------------------------------- */
  {
    id: "list-tokens",
    group: "auth",
    method: "GET",
    path: "/api/0/tokens",
    title: "List API tokens",
    auth: "operator / session",
    description: (
      <>
        Live tokens first, then revoked ones — a revoked row is kept so &quot;who had access, and when did it
        stop&quot; survives the revocation. The secret is never returned; <code>prefix</code> identifies a row.
      </>
    ),
    request: `curl -s "$BSIO/api/0/tokens" -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "tokens": [
    {
      "id": 6,
      "name": "grafana",
      "prefix": "bsiot_feb4b8da",
      "created_at": "2026-08-22T13:03:14+08:00",
      "last_used_at": "2026-08-24T16:20:11+08:00",
      "revoked_at": null
    }
  ]
}`,
      },
    ],
  },
  {
    id: "create-token",
    group: "auth",
    method: "POST",
    path: "/api/0/tokens",
    title: "Create an API token",
    auth: "operator / session",
    description: (
      <>
        Mints a read-only token. The plaintext <code>secret</code> is in this response and nowhere else, ever again —
        the engine stores a SHA-256. A lost token is revoked and replaced, not recovered.
      </>
    ),
    parameters: [{ name: "name", in: "body", type: "string", required: true, doc: "What will hold it — grafana, oncall-dashboard." }],
    request: `curl -s -X POST "$BSIO/api/0/tokens" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "grafana"}'`,
    responses: [
      {
        code: 201,
        codeLabel: "Created",
        sample: `{
  "secret": "bsiot_1cf03818e4a2558a18a609b36e948dbe…",
  "token": { "id": 7, "name": "grafana", "prefix": "bsiot_1cf03818", "created_at": "…" }
}`,
      },
    ],
  },
  {
    id: "revoke-token",
    group: "auth",
    method: "DELETE",
    path: "/api/0/tokens/{id}",
    title: "Revoke an API token",
    auth: "operator / session",
    description: <>Immediate: the next request with that token is a 401. Revoking an already-revoked token is a no-op 200.</>,
    request: `curl -s -X DELETE "$BSIO/api/0/tokens/7" -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN"`,
    responses: [
      { code: 200, codeLabel: "OK", sample: `{ "revoked": 7 }` },
      { code: 404, codeLabel: "Not Found", sample: `{ "error": "no such token" }` },
    ],
  },

  /* ---- ingest ------------------------------------------------------------------ */
  {
    id: "beat",
    group: "ingest",
    method: "POST",
    path: "/api/0/beat/{monitor}",
    title: "Send a heartbeat",
    auth: "ingest key",
    description: (
      <>
        One line per loop iteration, <b>after</b> the work — beating before it reports healthy through exactly the
        failure you want caught. The first beat creates the monitor; nothing is registered by hand. GET works too,
        so a shell loop can beat with bare curl.
      </>
    ),
    parameters: [
      { name: "monitor", in: "path", type: "slug", required: true, doc: "Monitor name, created on first beat." },
      { name: "every", in: "query", type: "seconds", doc: "Expected interval. Missing this + grace → MISSING." },
      { name: "grace", in: "query", type: "seconds", doc: "Slack after `every` before LATE/MISSING." },
      { name: "progress", in: "query", type: "int64", doc: <>Monotonic work counter. Beats arriving while this freezes → <b>STALLED</b> — the vLLM case.</> },
      { name: "stall_window", in: "query", type: "seconds", doc: "How long progress may sit still before STALLED." },
      { name: "env", in: "query", type: "string", doc: "Environment; default production." },
    ],
    request: `curl -s -X POST "$BSIO/api/0/beat/tts-batcher?every=30&grace=30&progress=$BATCHES&stall_window=90" \\
  -H "X-BSIO-Key: $BSIO_INGEST_KEY"`,
    responses: [
      { code: 200, codeLabel: "OK", sample: `{ "monitor": "tts-batcher", "status": "ok", "next_expected_at": "2026-08-24T16:32:11Z" }` },
      { code: 403, codeLabel: "Forbidden", sample: `{ "error": "unknown ingest key" }` },
    ],
  },
  {
    id: "envelope",
    group: "ingest",
    method: "POST",
    path: "/api/{projectID}/envelope",
    title: "Sentry envelope (what the SDK calls)",
    auth: "ingest key",
    description: (
      <>
        The endpoint a stock <code>sentry_sdk</code> DSN points at — you never call it by hand. Accepts error
        events, sessions, attachments and cron check-ins; transactions are dropped server-side. Rate-limited at
        50/s per project (burst 200) with <code>429 + Retry-After</code>, which the SDK honours.
      </>
    ),
    request: `# not called directly — configure the SDK instead:
sentry_sdk.init(dsn="http://$BSIO_INGEST_KEY@bettersentryio.internal:9090/1")`,
    responses: [{ code: 200, codeLabel: "OK", sample: `{ "id": "4906788bffe74e5faaa944d2ae7d21ab" }` }],
  },
  {
    id: "native-error",
    group: "ingest",
    method: "POST",
    path: "/api/0/errors",
    title: "Report an error (native)",
    auth: "ingest key",
    description: (
      <>
        For anything that cannot carry the SDK. Same grouping pipeline: events collapse into issues by fingerprint,
        per environment. 512&nbsp;KB cap per event.
      </>
    ),
    request: `curl -s -X POST "$BSIO/api/0/errors" \\
  -H "X-BSIO-Key: $BSIO_INGEST_KEY" -H "Content-Type: application/json" \\
  -d '{
    "message": "shape mismatch: got [1,512], expected [1,256]",
    "level": "error",
    "environment": "production",
    "exception": {"values": [{"type": "RuntimeError", "value": "shape mismatch",
      "stacktrace": {"frames": [{"filename": "vllm/worker.py", "function": "execute", "lineno": 812, "in_app": true}]}}]},
    "tags": {"correlation_id": "req-8f31", "gpu": "0"}
  }'`,
    responses: [
      { code: 202, codeLabel: "Accepted", sample: `{ "issue_id": 12, "is_new": false, "times_seen": 91, "culprit": "vllm/worker.py in execute" }` },
    ],
  },

  /* ---- errors & analytics -------------------------------------------------------- */
  {
    id: "issues",
    group: "errors",
    method: "GET",
    path: "/api/0/issues",
    title: "List issues",
    auth: "any read credential",
    description: (
      <>
        Newest sighting first. With a window, only issues that actually <i>fired inside it</i> — an event in the
        window, not a lifetime overlapping it — so the list always agrees with the chart above it.
      </>
    ),
    parameters: [
      { name: "project", in: "query", type: "slug", required: true, doc: "App slug." },
      { name: "statsPeriod", in: "query", type: "span", doc: <><code>24h</code>, <code>30d</code> — or explicit <code>start</code>/<code>end</code>.</> },
      { name: "resolved / archived", in: "query", type: "bool", doc: "Include those states too." },
      { name: "tag", in: "query", type: "key:value", doc: "Filter on issue tags. Repeatable." },
      { name: "limit", in: "query", type: "int", doc: "Default 100, cap 500." },
    ],
    request: `curl -s "$BSIO/api/0/issues?project=default&statsPeriod=7d&tag=release:tts-api@2.4.1" \\
  -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "issues": [
    {
      "id": 5, "title": "RuntimeError: CUDA out of memory…", "culprit": "vllm/worker.py in execute",
      "level": "error", "environment": "production", "times_seen": 91,
      "first_seen": "…", "last_seen": "…", "resolved_at": null,
      "tags": {"release": "tts-api@2.4.1", "server_name": "gpu-01", "handled": "no"},
      "activity": [{"at": "…", "count": 3}]
    }
  ],
  "counts": { "unresolved": 6, "events": 200 }
}`,
      },
    ],
  },
  {
    id: "series",
    group: "errors",
    method: "GET",
    path: "/api/0/apps/{slug}/series",
    title: "Event volume over time",
    auth: "any read credential",
    description: (
      <>
        Zero-filled buckets split by level — what the volume charts draw. An explicit <code>interval</code>{" "}
        (<code>15m</code>, <code>1h</code>) is honoured verbatim; absent, the engine picks a width that divides a
        day and keeps the window under ~100 buckets.
      </>
    ),
    request: `curl -s "$BSIO/api/0/apps/default/series?statsPeriod=24h&interval=1h" \\
  -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "interval_s": 3600, "total": 24, "levels": ["error", "warning"],
  "buckets": [{ "at": "2026-08-24T09:00:00Z", "counts": {"error": 3, "warning": 1} }]
}`,
      },
    ],
  },
  {
    id: "analytics",
    group: "errors",
    method: "GET",
    path: "/api/0/analytics",
    title: "Project analytics",
    auth: "any read credential",
    description: (
      <>
        The aggregates beside a chart: window totals, distinct issues, the previous window of equal length (for
        period-over-period), level split, breakdowns, and the top-10 leaderboard. Deliberately no time series —
        buckets come from <code>/series</code>, so the two cannot disagree.
      </>
    ),
    request: `curl -s "$BSIO/api/0/analytics?project=default&statsPeriod=30d" -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "total": 200, "issues": 7,
  "previous": { "start": "…", "end": "…", "total": 51, "issues": 4 },
  "levels": [{ "level": "error", "count": 176, "issues": 6 }],
  "breakdowns": [{ "field": "error.type", "rows": [{"value": "RuntimeError", "count": 91, "issues": 1}], "truncated": false }],
  "top_issues": [{ "id": 5, "title": "RuntimeError: …", "level": "error", "count": 91, "last_seen": "…" }]
}`,
      },
    ],
  },
  {
    id: "issue-workflow",
    group: "errors",
    method: "POST",
    path: "/api/0/issues/{id}/resolve",
    title: "Issue workflow",
    auth: "operator / session",
    description: (
      <>
        Sibling endpoints: <code>/resolve</code>, <code>/archive</code> (forever, for N hours, or until it recurs),{" "}
        <code>/priority</code>, and <code>DELETE /api/0/issues/{"{id}"}</code>. A recurrence reopens a resolved
        issue automatically — resolving is a claim, not a suppression.
      </>
    ),
    request: `curl -s -X POST "$BSIO/api/0/issues/5/resolve" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN" -H "Content-Type: application/json" \\
  -d '{"resolved": true}'`,
    responses: [{ code: 200, codeLabel: "OK", sample: `{ "id": 5, "resolved": true }` }],
  },

  /* ---- lookup ------------------------------------------------------------------ */
  {
    id: "event-search",
    group: "lookup",
    method: "GET",
    path: "/api/0/events/search",
    title: "Find events by correlation / trace id",
    auth: "any read credential",
    description: (
      <>
        Per-event identity search: <code>tag=key:value</code> (repeatable, all must match) or{" "}
        <code>trace=…</code> (matches <code>contexts.trace.trace_id</code>, where the SDK puts it). At least one is
        required. Each hit carries a <code>url</code> deep-linking to the issue page opened at that exact event —
        the same link every consumer uses.
      </>
    ),
    parameters: [
      { name: "tag", in: "query", type: "key:value", doc: <>e.g. <code>correlation_id:req-8f31</code>. GIN-indexed.</> },
      { name: "trace", in: "query", type: "hex", doc: "The SDK trace id." },
      { name: "project", in: "query", type: "slug", doc: "Optional — an id is searched across every app by default." },
      { name: "statsPeriod / start / end", in: "query", type: "window", doc: "Default: the last 24 hours." },
    ],
    request: `curl -s "$BSIO/api/0/events/search?tag=correlation_id:req-8f31&statsPeriod=7d" \\
  -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "events": [
    {
      "id": 200, "event_id": "4906788bffe74e5faaa944d2ae7d21ab",
      "received_at": "…", "message": "ZeroDivisionError: division by zero",
      "issue_id": 2, "issue_title": "ZeroDivisionError: division by zero",
      "level": "error", "environment": "production", "project": "default",
      "url": "https://bettersentryio…/apps/default/errors/2?event=200"
    }
  ]
}`,
      },
      { code: 400, codeLabel: "Bad Request", sample: `{ "error": "pass ?tag=key:value (repeatable) or ?trace=<trace id>" }` },
    ],
  },

  /* ---- monitors ---------------------------------------------------------------- */
  {
    id: "overview",
    group: "monitors",
    method: "GET",
    path: "/api/0/overview",
    title: "Monitors overview",
    auth: "any read credential",
    description: (
      <>
        Every monitor with its full state: status (<code>ok / late / missing / stalled / waiting</code>), last beat,
        progress, uptime over its observed window, and 24h activity buckets. The lean shape for shell loops is{" "}
        <code>GET /api/0/monitors</code>; one monitor&apos;s detail is <code>GET /api/0/monitors/{"{slug}"}</code>.
      </>
    ),
    request: `curl -s "$BSIO/api/0/overview" -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "summary": { "total": 1, "ok": 0, "late": 0, "missing": 1, "stalled": 0 },
  "monitors": [
    { "slug": "tts-batcher", "app": "default", "status": "missing",
      "environment": "production", "last_beat_at": "…", "uptime_pct": 98.4, "beats_24h": 0 }
  ]
}`,
      },
    ],
  },
  {
    id: "incidents",
    group: "monitors",
    method: "GET",
    path: "/api/0/incidents",
    title: "Incident log",
    auth: "any read credential",
    description: (
      <>
        Open and resolved incidents. <code>alerts_delivered</code> counts confirmed deliveries — 0 on an open
        incident means it has not reached a channel yet.
      </>
    ),
    request: `curl -s "$BSIO/api/0/incidents" -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "incidents": [
    { "id": 3, "monitor": "tts-batcher", "environment": "production", "kind": "missing",
      "opened_at": "…", "resolved_at": null, "duration_secs": 189494, "alerts_delivered": 0 }
  ]
}`,
      },
    ],
  },
  {
    id: "mute",
    group: "monitors",
    method: "POST",
    path: "/api/0/monitors/{slug}/mute",
    title: "Mute / unmute a monitor",
    auth: "operator / session",
    description: <>A muted monitor keeps recording state transitions but stops alerting on them.</>,
    request: `curl -s -X POST "$BSIO/api/0/monitors/tts-batcher/mute" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN" -H "Content-Type: application/json" \\
  -d '{"muted": true}'`,
    responses: [{ code: 200, codeLabel: "OK", sample: `{ "monitor": "tts-batcher", "muted": true }` }],
  },

  /* ---- apps -------------------------------------------------------------------- */
  {
    id: "list-apps",
    group: "apps",
    method: "GET",
    path: "/api/0/apps",
    title: "List apps",
    auth: "any read credential",
    description: <>Every app with its health rollup, ingest key, and retention setting.</>,
    request: `curl -s "$BSIO/api/0/apps" -H "Authorization: Bearer $BSIO_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "apps": [
    { "slug": "default", "name": "Default", "platform": "", "ingest_key": "c4cef…",
      "monitors": 1, "unhealthy": 1, "open_issues": 6, "connected": true, "retention_days": 0 }
  ]
}`,
      },
    ],
  },
  {
    id: "create-app",
    group: "apps",
    method: "POST",
    path: "/api/0/apps",
    title: "Create an app",
    auth: "operator / session",
    description: <>Mints the app and its ingest key in one move. The key is in this response and on the app&apos;s Settings page.</>,
    parameters: [
      { name: "name", in: "body", type: "string", required: true, doc: "Display name; the slug is derived." },
      { name: "platform", in: "body", type: "string", doc: "fastapi, python, shell… — picks the snippet shown on Settings." },
    ],
    request: `curl -s -X POST "$BSIO/api/0/apps" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN" -H "Content-Type: application/json" \\
  -d '{"name": "TTS API", "platform": "fastapi"}'`,
    responses: [
      { code: 201, codeLabel: "Created", sample: `{ "slug": "tts-api", "name": "TTS API", "ingest_key": "f1a2dd3f7c5941…" }` },
    ],
  },
  {
    id: "retention",
    group: "apps",
    method: "PUT",
    path: "/api/0/apps/{slug}/retention",
    title: "Set data retention",
    auth: "operator / session",
    description: (
      <>
        How long the app keeps error events. <b>0 (the default) keeps forever.</b> An hourly sweep removes events,
        attachments and fully-expired issues past the cutoff; issue counts survive the events they summarise. The
        change lands in the audit log.
      </>
    ),
    request: `curl -s -X PUT "$BSIO/api/0/apps/default/retention" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN" -H "Content-Type: application/json" \\
  -d '{"days": 90}'`,
    responses: [{ code: 200, codeLabel: "OK", sample: `{ "slug": "default", "retention_days": 90 }` }],
  },
  {
    id: "delete-app",
    group: "apps",
    method: "DELETE",
    path: "/api/0/apps/{slug}",
    title: "Delete an app",
    auth: "operator / session",
    description: <>Removes the app and everything under it — monitors, incidents, issues, events, attachments. Not recoverable.</>,
    request: `curl -s -X DELETE "$BSIO/api/0/apps/tts-api" -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN"`,
    responses: [{ code: 200, codeLabel: "OK", sample: `{ "slug": "tts-api", "monitors_removed": 3 }` }],
  },

  /* ---- ops --------------------------------------------------------------------- */
  {
    id: "audit",
    group: "ops",
    method: "GET",
    path: "/api/0/audit",
    title: "Audit log",
    auth: "operator / session",
    description: (
      <>
        Every control-plane mutation, recorded by the engine itself — actor, how they authenticated, action, status.
        Windowed like the stats endpoints; pages by keyset cursor (<code>before</code>/<code>after</code> row ids),
        so a row landing mid-read cannot shift what you see.
      </>
    ),
    request: `curl -s "$BSIO/api/0/audit?statsPeriod=7d&action=DELETE&limit=50" \\
  -H "Authorization: Bearer $BSIO_OPERATOR_TOKEN"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "entries": [
    { "id": 41, "at": "…", "actor": "husein.zolkepli@scicom.com.my", "via": "session",
      "action": "DELETE /api/0/apps/retention-check", "status": 200, "remote_addr": "127.0.0.1", "detail": {} }
  ],
  "has_older": true, "has_newer": false
}`,
      },
    ],
  },
  {
    id: "health",
    group: "ops",
    method: "GET",
    path: "/-/health",
    title: "Health",
    auth: "none",
    description: (
      <>
        The engine&apos;s honest self-report: detector tick age and failures, alerter queue and delivery counters,
        database reachability. On a standby replica <code>leader</code> is false and a stale tick is <i>not</i> a
        problem. <code>/-/ready</code> is the readiness probe; <code>/-/metrics</code> is Prometheus text.
      </>
    ),
    request: `curl -s "$BSIO/-/health"`,
    responses: [
      {
        code: 200,
        codeLabel: "OK",
        sample: `{
  "status": "ok", "version": "0.1.0", "uptime_s": 86400,
  "detector": { "leader": true, "ticks": 17280, "last_tick_age_s": 2, "consecutive_failures": 0 },
  "alerter": { "queue_depth": 0, "sent": 42, "failed": 0, "dropped": 0 },
  "database": "ok"
}`,
      },
      { code: 503, codeLabel: "Degraded", sample: `{ "status": "degraded", "problems": ["detector tick is stale (2m10s)"] }` },
    ],
  },
];

/* ---- the page ------------------------------------------------------------------- */

export function ApiDocs({ base }: { base: string }) {
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const hits = !q
      ? ENDPOINTS
      : ENDPOINTS.filter(
          (e) =>
            e.title.toLowerCase().includes(q) ||
            e.path.toLowerCase().includes(q) ||
            e.method.toLowerCase().includes(q)
        );
    return GROUPS.map((group) => ({ group, items: hits.filter((e) => e.group === group.id) })).filter(
      (g) => g.items.length > 0
    );
  }, [query]);

  return (
    <div className="grid grid-cols-1 gap-8 lg:grid-cols-[240px_minmax(0,1fr)]">
      {/* ---- endpoint nav ---- */}
      <aside className="hidden lg:block">
        <div className="scrollbar-thin sticky top-0 max-h-[calc(100vh-6rem)] space-y-4 overflow-y-auto pr-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search endpoints…"
              className="h-9 bg-card pl-8 text-sm"
            />
          </div>
          {filtered.map(({ group, items }) => (
            <div key={group.id}>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                {group.title}
              </p>
              <ul className="space-y-0.5">
                {items.map((e) => (
                  <li key={e.id}>
                    <a
                      href={`#${e.id}`}
                      className="flex items-center gap-2 rounded px-2 py-1 text-[13px] hover:bg-accent"
                    >
                      <MethodBadge method={e.method} />
                      <span className="truncate">{e.title}</span>
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </aside>

      {/* ---- reference ---- */}
      <div className="min-w-0">
        <h1 className="text-3xl font-bold tracking-tight">API docs</h1>
        <p className="mt-2 text-muted-foreground">
          Everything a service or a dashboard calls on the engine. Windowed reads share one vocabulary
          (<code>statsPeriod</code>, or <code>start</code>/<code>end</code>), writes land in the{" "}
          <Link href="/admin/audit" className="text-primary hover:underline">
            audit log
          </Link>
          , and tokens come from{" "}
          <Link href="/admin/tokens" className="text-primary hover:underline">
            API tokens
          </Link>
          .
        </p>

        <div className="mt-4">
          <CodeBlock label="base URL — export once, paste any sample">{`export BSIO="${base}"
export BSIO_TOKEN="bsiot_…"          # read-only, from Settings → API tokens
export BSIO_OPERATOR_TOKEN="…"       # admin; keep it in the engine's environment`}</CodeBlock>
        </div>

        {filtered.map(({ group, items }) => (
          <section key={group.id} className="mt-10">
            <h2 className="text-xl font-semibold tracking-tight">{group.title}</h2>
            <p className="mt-1 text-sm text-muted-foreground">{group.blurb}</p>
            {items.map((e) => (
              <EndpointSection key={e.id} e={e} />
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}

function EndpointSection({ e }: { e: Endpoint }) {
  return (
    <section id={e.id} className="mt-5 grid scroll-mt-4 gap-5 border-t border-border py-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,460px)]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <MethodBadge method={e.method} />
          <code className="font-mono text-[13px] font-semibold">{e.path}</code>
        </div>
        <h3 className="mt-2 text-base font-semibold tracking-tight">{e.title}</h3>
        <p className="mt-1 text-sm text-muted-foreground">{e.description}</p>
        <p className="mt-2 text-[12px] text-muted-foreground">
          Auth: <span className="font-mono">{e.auth}</span>
        </p>
        {e.parameters && e.parameters.length > 0 && (
          <div className="mt-3 space-y-1.5">
            {e.parameters.map((p) => (
              <div key={p.name} className="flex gap-3 text-[13px] leading-5">
                <code className="w-40 shrink-0 font-mono text-foreground/90">
                  {p.name}
                  {p.required && <span className="text-status-down">*</span>}
                </code>
                <span className="w-14 shrink-0 font-mono text-[11px] text-muted-foreground">{p.in}</span>
                <span className="min-w-0 text-muted-foreground">{p.doc}</span>
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="min-w-0 space-y-3">
        <CodeBlock label="request">{e.request}</CodeBlock>
        {e.responses.map((r) => (
          <div key={r.code}>
            <div className="mb-1.5 flex items-center gap-2">
              <StatusBadge code={r.code} label={r.codeLabel} />
              {r.doc && <span className="text-[12px] text-muted-foreground">{r.doc}</span>}
            </div>
            <CodeBlock>{r.sample}</CodeBlock>
          </div>
        ))}
      </div>
    </section>
  );
}
