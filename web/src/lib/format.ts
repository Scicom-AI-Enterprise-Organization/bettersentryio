/**
 * Pure display helpers: state → tone, seconds → "3m 20s", ISO → a readable stamp.
 *
 * These live apart from `@/lib/bsio` because that module is the *server* API client —
 * it holds `BSIO_API_TOKEN` and calls `auth()`, which reaches Node-only code (the SAML
 * provider, which needs `fs`). A client component that wanted `ago()` used to import it
 * from there and drag the whole chain into the browser bundle, which fails the build
 * with `Can't resolve 'fs'` six layers deep in node_modules and no hint of the cause.
 *
 * So: anything a client component may need to *render* goes here; anything that talks
 * to the engine stays in bsio.ts. Types are still defined in bsio.ts and imported here
 * as `import type`, which is erased at compile time and creates no runtime edge.
 */

import type { StatusTone } from "@/components/ui/status-pill";
import type { Incident, Issue, MonitorStatus } from "@/lib/bsio";

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

/** Live triage state, derived the same way the engine filters. */
export function issueStatus(i: Issue): "open" | "resolved" | "archived" {
  if (i.archived_at && (!i.archived_until || Date.parse(i.archived_until) > Date.now())) {
    return "archived";
  }
  if (i.resolved_at) return "resolved";
  return "open";
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
