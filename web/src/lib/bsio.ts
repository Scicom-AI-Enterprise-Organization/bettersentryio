/**
 * Client for the bettersentryio Go engine.
 *
 * The engine owns ingest, absence detection and alerting; this app is only its
 * operator UI. Every call here runs in a server component or server action — the
 * API key must never reach the browser.
 */

const BASE = process.env.BSIO_API_URL ?? "http://localhost:9090";
// The operator token (BSIO_API_TOKEN), not an app ingest key: it may create and
// delete apps, so it stays server-side and never reaches the browser.
const KEY = process.env.BSIO_API_TOKEN ?? "";

export type MonitorStatus = "waiting" | "ok" | "late" | "missing" | "stalled";

export type Bucket = {
  at: string;
  beats: number;
  progress_delta: number;
};

export type Monitor = {
  slug: string;
  app: string;
  app_name: string;
  environment: string;
  kind: string;
  status: MonitorStatus;
  last_beat_at: string | null;
  last_progress: number | null;
  next_expected_at: string | null;
  every_secs: number;
  grace_secs: number;
  stall_window_secs: number;
  muted: boolean;
  created_at: string;
  open_incident_since: string | null;
  /** Measured by the engine, not the browser — three clocks disagree. */
  open_incident_secs: number | null;
  uptime_pct: number;
  uptime_observed_secs: number;
  beats_24h: number;
  activity: Bucket[];
};

export type Incident = {
  id: number;
  monitor: string;
  environment: string;
  kind: "missing" | "stalled" | "failed_checkins";
  opened_at: string;
  resolved_at: string | null;
  duration_secs: number;
  alerts_delivered: number;
};

export type Summary = {
  total: number;
  ok: number;
  late: number;
  missing: number;
  stalled: number;
  waiting: number;
  open_incidents: number;
  unhealthy: number;
};

/** EngineDown is returned instead of throwing so a page can say so plainly. */
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

async function get<T>(path: string): Promise<Result<T>> {
  try {
    const res = await fetch(`${BASE}${path}`, {
      headers: { "X-BSIO-Key": KEY },
      // Monitoring data is worthless when cached: the whole point is current state.
      cache: "no-store",
    });
    if (res.status === 401 || res.status === 403) {
      return { ok: false, error: "The engine rejected our API key. Check BSIO_API_TOKEN." };
    }
    if (!res.ok) {
      return { ok: false, error: `The engine returned ${res.status}.` };
    }
    return { ok: true, data: (await res.json()) as T };
  } catch {
    return {
      ok: false,
      error: `Cannot reach the bettersentryio engine at ${BASE}. Is it running?`,
    };
  }
}

export function getOverview() {
  return get<{ summary: Summary; monitors: Monitor[] }>("/api/0/overview");
}

export function getMonitor(slug: string) {
  return get<{
    monitor: Monitor;
    config: Record<string, unknown>;
    incidents: Incident[];
  }>(`/api/0/monitors/${encodeURIComponent(slug)}`);
}

export function getIncidents() {
  return get<{ incidents: Incident[] }>("/api/0/incidents");
}

export async function setMuted(slug: string, muted: boolean): Promise<Result<unknown>> {
  try {
    const res = await fetch(
      `${BASE}/api/0/monitors/${encodeURIComponent(slug)}/mute?muted=${muted}`,
      { method: "POST", headers: { "X-BSIO-Key": KEY }, cache: "no-store" },
    );
    if (!res.ok) return { ok: false, error: `The engine returned ${res.status}.` };
    return { ok: true, data: await res.json() };
  } catch {
    return { ok: false, error: "Cannot reach the bettersentryio engine." };
  }
}

/* ---- presentation helpers -------------------------------------------------- */

import type { StatusTone } from "@/components/ui/status-pill";

/**
 * Maps monitor state onto the shared four-tone status system.
 *
 * `late` is deliberately `idle` (amber) rather than `down`: overdue inside its
 * grace window is a warning, not an outage. `stalled` is amber too — the loop is
 * alive, just not working — which keeps red meaning "no heartbeat at all".
 */
export function monitorTone(status: MonitorStatus): StatusTone {
  switch (status) {
    case "ok":
      return "active";
    case "late":
      return "idle";
    case "stalled":
      return "idle";
    case "missing":
      return "down";
    default:
      return "muted";
  }
}

export function incidentTone(kind: Incident["kind"]): StatusTone {
  return kind === "stalled" ? "idle" : "down";
}

export function shortDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

export function ago(iso: string | null): string {
  if (!iso) return "never";
  return `${shortDuration((Date.now() - new Date(iso).getTime()) / 1000)} ago`;
}

export function clock(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleTimeString([], { hour12: false });
}

export function stamp(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString([], {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function uptimeLabel(pct: number): string {
  return pct >= 99.995 ? "100%" : `${(Math.floor(pct * 100) / 100).toFixed(2)}%`;
}

/* ---- apps ------------------------------------------------------------------ */

export type App = {
  id: number;
  slug: string;
  name: string;
  platform: string;
  created_at: string;
  ingest_key: string;
  monitors: number;
  unhealthy: number;
  last_beat_at: string | null;
  open_incident: boolean;
  open_issues: number;
  last_event_at: string | null;
  connected: boolean;
};

export function getApps() {
  return get<{ apps: App[] }>("/api/0/apps");
}

/* ---- error tracking (M2/D14) ------------------------------------------------ */

export type Issue = {
  id: number;
  project: string;
  project_name: string;
  fingerprint: string;
  environment: string;
  kind: string;
  culprit: string;
  title: string;
  level: string;
  times_seen: number;
  first_seen: string;
  last_seen: string;
  resolved_at: string | null;
  /** Last 24h of events, one bucket per hour, oldest first. */
  activity: { at: string; count: number }[] | null;
};

export type IssueCounts = { open: number; resolved: number };

/**
 * The stored event payload — the SDK's own bytes, verbatim. Everything is
 * optional: events arrive from the stock sentry_sdk (envelope) and from the
 * legacy vendored client, and the two shapes differ at the edges.
 */
export type EventFrame = {
  filename?: string;
  abs_path?: string;
  function?: string;
  module?: string;
  lineno?: number;
  in_app?: boolean;
  context_line?: string | string[];
  pre_context?: string[];
  post_context?: string[];
  vars?: Record<string, unknown>;
};

export type EventPayload = {
  event_id?: string;
  timestamp?: string | number;
  level?: string;
  logger?: string;
  message?: string;
  logentry?: { message?: string; formatted?: string };
  environment?: string;
  release?: string;
  server_name?: string;
  transaction?: string;
  tags?: Record<string, string> | null;
  extra?: Record<string, unknown> | null;
  request?: {
    method?: string;
    url?: string;
    query_string?: string;
    headers?: Record<string, string>;
  } | null;
  exception?: { values?: { type?: string; value?: string; module?: string; mechanism?: { type?: string; handled?: boolean }; stacktrace?: { frames?: EventFrame[] } }[] } | null;
  contexts?: Record<string, Record<string, unknown>> | null;
  breadcrumbs?: { values?: { timestamp?: string | number; type?: string; category?: string; level?: string; message?: string }[] } | { timestamp?: string | number; category?: string; level?: string; message?: string }[] | null;
  sdk?: { name?: string; version?: string } | null;
};

export type IssueDetail = {
  issue: Issue;
  latest_event: EventPayload;
  recent: { id: number; received_at: string; message: string }[];
};

export function getIssues(project: string, includeResolved = false) {
  const q = includeResolved ? "&resolved=true" : "";
  return get<{ issues: Issue[]; counts: IssueCounts }>(
    `/api/0/issues?project=${encodeURIComponent(project)}${q}`,
  );
}

export function getIssue(id: number | string) {
  return get<IssueDetail>(`/api/0/issues/${id}`);
}

/* ---- alerting ---------------------------------------------------------------- */

export type TeamsAlert = { configured: boolean; url_masked: string };

export function getTeamsAlert() {
  return get<TeamsAlert>("/api/0/alerts/teams");
}

/** Empty url disables the channel without forgetting it. */
export async function setTeamsAlert(url: string): Promise<Result<TeamsAlert>> {
  try {
    const res = await fetch(`${BASE}/api/0/alerts/teams`, {
      method: "PUT",
      headers: { "X-BSIO-Key": KEY, "Content-Type": "application/json" },
      body: JSON.stringify({ url }),
      cache: "no-store",
    });
    if (!res.ok) {
      const body = (await res.json().catch(() => null)) as { error?: string } | null;
      return { ok: false, error: body?.error ?? `The engine returned ${res.status}.` };
    }
    return { ok: true, data: (await res.json()) as TeamsAlert };
  } catch {
    return { ok: false, error: "Cannot reach the bettersentryio engine." };
  }
}

export function getApp(slug: string) {
  return get<{ app: App; monitors: Monitor[] }>(`/api/0/apps/${encodeURIComponent(slug)}`);
}

export async function createApp(
  name: string,
  platform = "",
): Promise<Result<{ slug: string; name: string; platform: string; ingest_key: string }>> {
  try {
    const res = await fetch(`${BASE}/api/0/apps`, {
      method: "POST",
      headers: { "X-BSIO-Key": KEY, "Content-Type": "application/json" },
      body: JSON.stringify({ name, platform }),
      cache: "no-store",
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, error: (body as { error?: string }).error ?? `Engine returned ${res.status}.` };
    }
    return {
      ok: true,
      data: body as { slug: string; name: string; platform: string; ingest_key: string },
    };
  } catch {
    return { ok: false, error: `Cannot reach the engine at ${BASE}. Is it running?` };
  }
}

/** Mirrors store.Slugify in the engine so the form can preview the slug. */
export function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64)
    .replace(/-+$/g, "");
}

export async function deleteApp(slug: string): Promise<Result<{ monitors_removed: number }>> {
  try {
    const res = await fetch(`${BASE}/api/0/apps/${encodeURIComponent(slug)}`, {
      method: "DELETE",
      headers: { "X-BSIO-Key": KEY },
      cache: "no-store",
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, error: (body as { error?: string }).error ?? `Engine returned ${res.status}.` };
    }
    return { ok: true, data: body as { monitors_removed: number } };
  } catch {
    return { ok: false, error: "Cannot reach the bettersentryio engine." };
  }
}
