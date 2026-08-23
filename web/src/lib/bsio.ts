/**
 * Client for the bettersentryio Go engine.
 *
 * The engine owns ingest, absence detection and alerting; this app is only its
 * operator UI. Every call here runs in a server component or server action — the
 * API key must never reach the browser.
 */

import { windowParams, type TimeWindow } from "@/lib/ranges";
import { auth } from "@/lib/auth";

/**
 * SERVER ONLY. This module holds the operator token and calls `auth()`, which reaches
 * the SAML provider and therefore Node's `fs`. Importing it from a client component
 * pulls that whole chain into the browser bundle and breaks the build with
 * `Can't resolve 'fs'` deep inside node_modules, naming nothing that would help.
 *
 * Client components need display helpers, not fetchers: those live in `@/lib/format`.
 * Types are safe to import from here anywhere — `import type` is erased and creates no
 * runtime edge.
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

/**
 * Who is doing this, for the engine's audit log.
 *
 * The engine trusts `X-BSIO-Actor` only on a request that also carries the operator
 * token — which is exactly this module and nothing that can otherwise reach the port —
 * so attaching the signed-in email here is what turns an audit row from "operator" into
 * the person who clicked the button.
 *
 * Attached to mutations only. Reads are not audited, and `auth()` is a session lookup
 * per call: the analytics page alone makes seven reads, and paying for seven session
 * reads to annotate rows nobody writes is a cost with no reader.
 *
 * It never throws. Attribution is worth having, but it is not worth being the reason a
 * delete fails — an unattributed row still records that the delete happened.
 */
async function actorHeader(): Promise<Record<string, string>> {
  try {
    const email = (await auth())?.user?.email;
    // The engine caps the header at 200 characters and ignores anything longer.
    return email ? { "X-BSIO-Actor": email.slice(0, 200) } : {};
  } catch {
    return {};
  }
}

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
      {
        method: "POST",
        headers: { "X-BSIO-Key": KEY, ...(await actorHeader()) },
        cache: "no-store",
      },
    );
    if (!res.ok) return { ok: false, error: `The engine returned ${res.status}.` };
    return { ok: true, data: await res.json() };
  } catch {
    return { ok: false, error: "Cannot reach the bettersentryio engine." };
  }
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
  /** Days of events kept; 0 keeps them forever. Swept hourly by the engine. */
  retention_days: number;
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
  archived_at: string | null;
  archived_until: string | null;
  archive_recur: boolean;
  priority: string;
  /** Client tags merged with server-derived ones (level, url, mechanism, ...). */
  tags: Record<string, string> | null;
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
  user?: Record<string, unknown> | null;
  modules?: Record<string, string> | null;
  dist?: string;
  threads?: {
    values?: {
      id?: number | string;
      name?: string;
      crashed?: boolean;
      current?: boolean;
      main?: boolean;
      state?: string;
      stacktrace?: { frames?: EventFrame[] } | null;
    }[];
  } | null;
};

export type ReleaseRow = {
  release: string;
  environment: string;
  sessions: number;
  exited: number;
  errored: number;
  crashed: number;
  abnormal: number;
  crash_free: number;
  first_seen: string;
  last_seen: string;
};

export type EventAttachment = {
  id: number;
  filename: string;
  content_type: string;
  size: number;
  received_at: string;
};

export function getReleases(project: string, days = 30) {
  return get<{ releases: ReleaseRow[]; days: number }>(
    `/api/0/releases?project=${encodeURIComponent(project)}&days=${days}`,
  );
}

export function getEventAttachments(project: string, eventUuid: string) {
  return get<{ attachments: EventAttachment[] }>(
    `/api/0/events/${encodeURIComponent(eventUuid)}/attachments?project=${encodeURIComponent(project)}`,
  );
}

/** The engine bytes for one attachment — the download proxy streams this. */
export async function fetchAttachment(id: number): Promise<Response> {
  return fetch(`${BASE}/api/0/attachments/${id}`, {
    headers: { "X-BSIO-Key": KEY },
    cache: "no-store",
  });
}

export type IssueDetail = {
  issue: Issue;
  latest_event: EventPayload;
  recent: { id: number; received_at: string; message: string }[];
};

export function getIssues(
  project: string,
  opts?: { resolved?: boolean; archived?: boolean; window?: TimeWindow; limit?: number },
) {
  const q = new URLSearchParams({ project });
  if (opts?.resolved) q.set("resolved", "true");
  if (opts?.archived) q.set("archived", "true");
  // Absent, the engine returns its default 100 — fine for a list somebody reads, not for
  // a figure summed from it. The engine's own ceiling is 500.
  if (opts?.limit) q.set("limit", String(opts.limit));
  // The list honours the same window as the chart above it, or the two disagree about
  // what "the last 30 days" contains.
  if (opts?.window) for (const [k, v] of windowParams(opts.window)) q.set(k, v);
  return get<{ issues: Issue[]; counts: IssueCounts }>(`/api/0/issues?${q}`);
}

export function resolveIssue(id: number, resolved: boolean) {
  return write<{ issue: number }>(`/api/0/issues/${id}/resolve?resolved=${resolved}`, "POST");
}

export function archiveIssue(id: number, mode: "forever" | "for" | "recur" | "off", hours?: number) {
  return write<{ issue: number }>(`/api/0/issues/${id}/archive`, "POST", { mode, hours });
}

export function setIssuePriority(id: number, priority: string) {
  return write<{ issue: number }>(`/api/0/issues/${id}/priority`, "POST", { priority });
}

export function deleteIssue(id: number) {
  return write<{ deleted: number }>(`/api/0/issues/${id}`, "DELETE");
}

export function getIssueEvent(issueID: number | string, eventID: number | string) {
  return get<{ id: number; payload: EventPayload }>(`/api/0/issues/${issueID}/events/${eventID}`);
}

export type IssueSeries = {
  issue_id: number;
  start: string;
  end: string;
  interval_s: number;
  total: number;
  buckets: { at: string; count: number }[];
};

/**
 * Occurrence volume for one issue. `range` is a Sentry-style span (30d, 24h) and
 * `interval` may be "auto", in which case the engine fits the buckets to the range.
 */
export function getIssueSeries(id: number | string, w: TimeWindow) {
  return get<IssueSeries>(
    `/api/0/issues/${encodeURIComponent(String(id))}/series?${windowParams(w)}`,
  );
}

export type ProjectSeries = {
  project: string;
  start: string;
  end: string;
  interval_s: number;
  total: number;
  /** Levels present in this window, biggest first — the stack order. */
  levels: string[];
  buckets: { at: string; counts: Record<string, number> }[];
};

/** Event volume for a whole app, split by level. */
export function getProjectSeries(slug: string, w: TimeWindow) {
  return get<ProjectSeries>(
    `/api/0/apps/${encodeURIComponent(slug)}/series?${windowParams(w)}`,
  );
}

export type AnalyticsLevel = { level: string; count: number; issues: number };

export type AnalyticsIssue = {
  id: number;
  title: string;
  culprit: string;
  level: string;
  count: number;
  last_seen: string;
};

export type AnalyticsRow = { value: string; count: number; issues: number };

/** One grouped top-N over the window. `truncated` means there were more values than rows. */
export type AnalyticsBreakdown = { field: string; rows: AnalyticsRow[]; truncated: boolean };

/**
 * The preceding window of the same length, for period-over-period deltas. It carries its
 * own level split, so a panel can say which level grew rather than only that the total did.
 */
export type AnalyticsWindow = {
  start: string;
  end: string;
  total: number;
  issues: number;
  levels: AnalyticsLevel[];
};

/**
 * One cross-tab: the same events counted against two dimensions at once, pivoted by the
 * engine so the axes arrive ordered by weight and zero-filled.
 */
export type AnalyticsMatrix = {
  row_field: string;
  column_field: string;
  rows: string[];
  columns: string[];
  /** Row-major, aligned to `rows` × `columns`. */
  cells: number[][];
};

export type ProjectAnalytics = {
  project: string;
  start: string;
  end: string;
  total: number;
  issues: number;
  previous: AnalyticsWindow;
  levels: AnalyticsLevel[];
  breakdowns: AnalyticsBreakdown[];
  matrix: AnalyticsMatrix;
  top_issues: AnalyticsIssue[];
};

/**
 * The figures beside the analytics chart: events, distinct issues, the level split and
 * the ten issues producing the most of it. Deliberately not a series — getProjectSeries
 * owns the buckets, so there is only ever one shape of the same data on the page.
 *
 * Its totals are an exact [start, end) aggregate, where a series total sums a
 * zero-filled axis whose first bucket is floored to an interval boundary. The two can
 * differ by one partial bucket, so a page showing both must print this one.
 */
export function getProjectAnalytics(project: string, w: TimeWindow) {
  const q = new URLSearchParams({ project });
  // The same window the chart is drawn from. A page whose figures take a preset while
  // its chart takes two timestamps would be showing two windows at once.
  for (const [k, v] of windowParams(w)) q.set(k, v);
  return get<ProjectAnalytics>(`/api/0/analytics?${q}`);
}

export function getIssue(id: number | string) {
  return get<IssueDetail>(`/api/0/issues/${id}`);
}

/* ---- api tokens -------------------------------------------------------------- */

/**
 * A Sentry-style bearer token, as the engine describes it — never the secret itself.
 * The plaintext exists once, in the response to its creation, and creation goes through
 * a server action for exactly that reason: it must not pass through a client component.
 */
export type ApiToken = {
  id: number;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
};

export function getApiTokens() {
  return get<{ tokens: ApiToken[] }>("/api/0/tokens");
}

/**
 * Mints a token. The secret in this response is the only copy the engine will ever hand
 * out — it stores a hash — so a caller that loses it has to revoke and mint again. Call
 * it from a server action, never from a client component: the plaintext must not travel
 * through the browser as page data.
 */
export function createApiToken(name: string) {
  return write<{ token: ApiToken; secret: string }>("/api/0/tokens", "POST", { name });
}

export function revokeApiToken(id: number) {
  return write<{ revoked: number }>(`/api/0/tokens/${id}`, "DELETE");
}

/* ---- retention and audit ------------------------------------------------------ */

/** 0 = keep forever (the default). Changing it is an admin action and lands in the audit log. */
export function setAppRetention(slug: string, days: number) {
  return write<{ slug: string; retention_days: number }>(
    `/api/0/apps/${encodeURIComponent(slug)}/retention`,
    "PUT",
    { days },
  );
}

/**
 * One mutating call, as the engine recorded it. `actor` is an email when the UI made the
 * call (see actorHeader), and otherwise says which credential did: "operator", a token
 * prefix, or an ingest key prefix.
 */
export type AuditEntry = {
  id: number;
  at: string;
  actor: string;
  via: "session" | "operator" | "token" | "key" | "none";
  action: string;
  status: number;
  remote_addr: string;
  detail: unknown;
};

/**
 * One page of the log, plus whether there is more in either direction.
 *
 * Keyset rather than offset: the table is appended to while somebody reads it, and with
 * OFFSET a row arriving at the top shifts page 2 down — so the reader re-reads a row or
 * skips one. "Did I see everything" has to be answerable in an audit log.
 */
export type AuditPage = {
  entries: AuditEntry[];
  has_older: boolean;
  has_newer: boolean;
};

export function getAuditLog(opts?: {
  actor?: string;
  action?: string;
  limit?: number;
  /** Defaults to the last 30 days at the call site; omit only to ask for all of history. */
  window?: TimeWindow;
  /**
   * Page cursors: pass at most one. `before` walks toward older rows, `after` back.
   *
   * A cursor is `"<entry.at>,<entry.id>"` — the whole sort key, not just the id, because
   * the log is ordered by time and an id alone only agrees with that while nothing has
   * ever backfilled a row with an explicit timestamp.
   */
  before?: string;
  after?: string;
}) {
  const q = opts?.window ? windowParams(opts.window) : new URLSearchParams();
  // The audit log has no chart, so a bucket interval would be noise in the URL.
  q.delete("interval");
  if (opts?.actor) q.set("actor", opts.actor);
  if (opts?.action) q.set("action", opts.action);
  if (opts?.limit) q.set("limit", String(opts.limit));
  if (opts?.before) q.set("before", opts.before);
  if (opts?.after) q.set("after", opts.after);
  return get<AuditPage>(`/api/0/audit?${q}`);
}

/* ---- alerting ---------------------------------------------------------------- */

export type TeamsAlert = { configured: boolean; url_masked: string };

export function getTeamsAlert() {
  return get<TeamsAlert>("/api/0/alerts/teams");
}

export type Channel = {
  id: number;
  name: string;
  type: string;
  url_masked: string;
  enabled: boolean;
  /** Only meaningful on a global channel read in a project's context. */
  imported?: boolean;
};

export function listChannels() {
  return get<{ channels: Channel[] }>("/api/0/channels");
}

/**
 * A project's alerting: the channels it owns, the global catalogue with a flag per
 * row saying whether this project routes to it, and how long it waits before
 * folding a burst into one digest.
 */
export type ProjectAlerts = {
  channels: Channel[];
  globals: Channel[];
  patience_seconds: number;
  patience_choices: number[];
};

export function getProjectAlerts(slug: string) {
  return get<ProjectAlerts>(`/api/0/apps/${encodeURIComponent(slug)}/alerts`);
}

async function write<T>(path: string, method: string, body?: unknown): Promise<Result<T>> {
  try {
    const res = await fetch(`${BASE}${path}`, {
      method,
      headers: { "X-BSIO-Key": KEY, "Content-Type": "application/json", ...(await actorHeader()) },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const parsed = (await res.json().catch(() => null)) as { error?: string } | null;
      return { ok: false, error: parsed?.error ?? `The engine returned ${res.status}.` };
    }
    return { ok: true, data: (await res.json()) as T };
  } catch {
    return { ok: false, error: "Cannot reach the bettersentryio engine." };
  }
}

export function createChannel(name: string, type: string, url: string) {
  return write<Channel>("/api/0/channels", "POST", { name, type, url });
}

export function updateChannel(
  id: number,
  patch: { name?: string; url?: string; enabled?: boolean },
) {
  return write<{ updated: number }>(`/api/0/channels/${id}`, "PUT", patch);
}

export function deleteChannel(id: number) {
  return write<{ deleted: number }>(`/api/0/channels/${id}`, "DELETE");
}

/**
 * Delivers a probe through the live notification path to a channel that has not been
 * saved. A failure answers with an HTTP error, so `write` folds the upstream's own words
 * — "404 from teams", "connection refused" — into the Result every caller already
 * handles: those two send someone to different places, and a bare false sends them
 * nowhere.
 */
export function testChannel(type: string, url: string) {
  return write<{ tested: true }>("/api/0/channels/test", "POST", { type, url });
}

/* ---- project-level alerting --------------------------------------------------- */

const app = (slug: string) => `/api/0/apps/${encodeURIComponent(slug)}`;

export function createProjectChannel(slug: string, name: string, type: string, url: string) {
  return write<Channel>(`${app(slug)}/channels`, "POST", { name, type, url });
}

export function updateProjectChannel(
  slug: string,
  id: number,
  patch: { name?: string; url?: string; enabled?: boolean },
) {
  return write<{ updated: number }>(`${app(slug)}/channels/${id}`, "PUT", patch);
}

export function deleteProjectChannel(slug: string, id: number) {
  return write<{ deleted: number }>(`${app(slug)}/channels/${id}`, "DELETE");
}

/** Import is a reference: the URL stays in the catalogue, so rotating it there
 *  rotates it for every project that imported it. */
export function importChannels(slug: string, channelIDs: number[]) {
  return write<{ imported: number[] }>(`${app(slug)}/channels/import`, "POST", {
    channel_ids: channelIDs,
  });
}

export function unimportChannel(slug: string, id: number) {
  return write<{ unimported: number }>(`${app(slug)}/channels/import/${id}`, "DELETE");
}

export function setAlertPatience(slug: string, seconds: number) {
  return write<{ patience_seconds: number }>(`${app(slug)}/alerts/patience`, "PUT", { seconds });
}

/** Empty url disables the channel without forgetting it. */
export async function setTeamsAlert(url: string): Promise<Result<TeamsAlert>> {
  try {
    const res = await fetch(`${BASE}/api/0/alerts/teams`, {
      method: "PUT",
      headers: { "X-BSIO-Key": KEY, "Content-Type": "application/json", ...(await actorHeader()) },
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
      headers: { "X-BSIO-Key": KEY, "Content-Type": "application/json", ...(await actorHeader()) },
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

export async function deleteApp(slug: string): Promise<Result<{ monitors_removed: number }>> {
  try {
    const res = await fetch(`${BASE}/api/0/apps/${encodeURIComponent(slug)}`, {
      method: "DELETE",
      headers: { "X-BSIO-Key": KEY, ...(await actorHeader()) },
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
