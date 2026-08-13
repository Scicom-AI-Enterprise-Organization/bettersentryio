/**
 * The Issues views, and what each one actually means here.
 *
 * bettersentryio detects three distinct things, and they deserve separate lists because
 * they demand different responses:
 *
 *   Errors & Outages   the loop stopped        -> page someone
 *   Breached Metrics   the loop stopped working -> page someone, and it is subtler
 *   Warnings           overdue but inside grace -> watch it, do not wake anyone
 *
 * The middle one is the reason this project exists: a stalled loop answers every health
 * check correctly while producing nothing.
 */

import type { Incident, Monitor, MonitorStatus } from "@/lib/bsio";

export type IssueViewId = "outages" | "breached" | "warnings";

export type IssueView = {
  id: IssueViewId;
  label: string;
  /** Sentence shown under the page title. */
  description: string;
  /** Monitor states that belong in this view. */
  statuses: MonitorStatus[];
  /** Incident kinds that belong in this view. */
  kinds: Incident["kind"][];
  /** Empty-state line: what it means that this list is empty. */
  empty: string;
};

export const ISSUE_VIEWS: IssueView[] = [
  {
    id: "outages",
    label: "Errors & Outages",
    description:
      "Heartbeats stopped arriving. The loop is gone — crashed, cancelled, deadlocked, or the whole process is down.",
    statuses: ["missing"],
    kinds: ["missing", "failed_checkins"],
    empty: "No loop has gone silent. Every monitor beat within its window.",
  },
  {
    id: "breached",
    label: "Breached Metrics",
    description:
      "Heartbeats are still arriving, but the progress counter has not moved. The loop is alive and doing no work — the failure a /health check reports as fine.",
    statuses: ["stalled"],
    kinds: ["stalled"],
    empty: "No monitor is beating without progress. Work is actually moving.",
  },
  {
    id: "warnings",
    label: "Warnings",
    description:
      "Overdue, but still inside the grace window. Nothing has broken yet; this is what a slow loop looks like before it becomes an outage.",
    statuses: ["late"],
    kinds: [],
    empty: "Nothing is running late.",
  },
];

export function issueView(id: string): IssueView | undefined {
  return ISSUE_VIEWS.find((v) => v.id === id);
}

export function monitorsFor(view: IssueView, monitors: Monitor[]): Monitor[] {
  return monitors.filter((m) => view.statuses.includes(m.status));
}

export function incidentsFor(view: IssueView, incidents: Incident[]): Incident[] {
  return incidents.filter((i) => view.kinds.includes(i.kind));
}

