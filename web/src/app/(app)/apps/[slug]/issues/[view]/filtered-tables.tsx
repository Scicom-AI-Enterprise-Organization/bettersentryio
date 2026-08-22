"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useMemo, useState, useTransition } from "react";
import { Search } from "lucide-react";

import type { Issue, Monitor } from "@/lib/bsio";
import { issueStatus, monitorTone, shortDuration } from "@/lib/bsio";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/status-pill";
import type { StatusTone } from "@/components/ui/status-pill";
import {
  Table,
  TableBody,
  TableCard,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ActivityBars } from "@/components/bsio/activity-bars";
import { ConfirmDialog } from "@/components/bsio/confirm-dialog";
import { SelectBox } from "@/components/bsio/select-box";
import { WindowControls } from "@/components/bsio/window-controls";
import type { TimeWindow } from "@/lib/ranges";
import { Age, Ago, ClockAt, Since } from "@/components/bsio/time";
import { bulkArchive, bulkDelete, bulkPriority, bulkResolve, type BulkResult } from "./actions";

/* Client-side filtering on the already-fetched page of rows: instant, no
 * round-trip, and honest at this scale (the list endpoint caps at 100). */

function levelTone(level: string): StatusTone {
  switch (level) {
    case "fatal":
    case "error":
      return "down";
    case "warning":
      return "idle";
    default:
      return "init";
  }
}

function statusTone(status: "open" | "resolved" | "archived"): StatusTone {
  if (status === "resolved") return "active";
  if (status === "archived") return "muted";
  return "init";
}

/**
 * A filter that lives in the URL without asking the server for anything.
 *
 * These four narrow rows the browser already has, so a navigation would be a round
 * trip for data we are holding — `history.replaceState` updates the address bar (and
 * Next's useSearchParams) so the view is linkable and survives a reload, at no cost.
 * The window controls are the opposite case and do navigate: changing the window
 * changes what the engine must read.
 */
function useUrlFilter(key: string, initial: string, fallback: string) {
  const [value, setValue] = useState(initial);
  const set = useCallback(
    (next: string) => {
      setValue(next);
      const url = new URL(window.location.href);
      if (next === fallback) url.searchParams.delete(key);
      else url.searchParams.set(key, next);
      window.history.replaceState(null, "", url);
    },
    [key, fallback],
  );
  return [value, set] as const;
}

function FilterBar({
  search,
  setSearch,
  selects,
  defaults,
  trailing,
}: {
  search: string;
  setSearch: (v: string) => void;
  selects: {
    label: string;
    value: string;
    options: string[];
    set: (v: string) => void;
    allLabel?: string;
  }[];
  /** The value each select returns to when "cleared" (e.g. status -> open). */
  defaults?: Record<string, string>;
  /** The window picker, on the same row: one line of controls, not two. */
  trailing?: React.ReactNode;
}) {
  const dirty =
    search.trim() !== "" || selects.some((s) => s.value !== (defaults?.[s.label] ?? ""));
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search…"
          className="h-9 w-64 bg-background pl-8 text-sm"
        />
      </div>
      {selects.map((s) => (
        <SelectBox
          key={s.label}
          value={s.value}
          active={s.value !== (defaults?.[s.label] ?? "")}
          onChange={(e) => s.set(e.target.value)}
        >
          <option value="">{s.allLabel ?? `${s.label}: all`}</option>
          {s.options.map((o) => (
            <option key={o} value={o}>
              {s.label}: {o}
            </option>
          ))}
        </SelectBox>
      ))}
      {trailing}
      {dirty && (
        <button
          type="button"
          onClick={() => {
            setSearch("");
            for (const s of selects) s.set(defaults?.[s.label] ?? "");
          }}
          className="text-sm text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        >
          Clear
        </button>
      )}
    </div>
  );
}

export function ErrorIssuesFiltered({
  slug,
  issues,
  chart,
  window: w,
  initial,
}: {
  slug: string;
  issues: Issue[];
  /**
   * The volume chart, composed by the server component and slotted in here so it sits
   * under the filter row. It is windowed by its own range/interval controls, not by the
   * filters above it — those narrow the already-fetched rows in the browser.
   */
  chart?: React.ReactNode;
  /** The window the rows and the chart were fetched for; the picker sits in the row. */
  window?: TimeWindow;
  /** Filter values read from the URL on the server, so a link opens filtered. */
  initial?: { q?: string; status?: string; level?: string; env?: string };
}) {
  const router = useRouter();
  const [search, setSearch] = useUrlFilter("q", initial?.q ?? "", "");
  const [level, setLevel] = useUrlFilter("level", initial?.level ?? "", "");
  const [env, setEnv] = useUrlFilter("env", initial?.env ?? "", "");
  const [status, setStatus] = useUrlFilter("status", initial?.status ?? "open", "open");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [notice, setNotice] = useState<BulkResult | null>(null);
  const [pending, startTransition] = useTransition();

  const levels = useMemo(() => [...new Set(issues.map((i) => i.level))].sort(), [issues]);
  const envs = useMemo(() => [...new Set(issues.map((i) => i.environment))].sort(), [issues]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return issues.filter(
      (i) =>
        (!status || issueStatus(i) === status) &&
        (!level || i.level === level) &&
        (!env || i.environment === env) &&
        (!q || i.title.toLowerCase().includes(q) || i.culprit.toLowerCase().includes(q)),
    );
  }, [issues, search, level, env, status]);

  const open = issues.filter((i) => issueStatus(i) === "open").length;
  const visibleIds = filtered.map((i) => i.id);
  const allSelected = visibleIds.length > 0 && visibleIds.every((id) => selected.has(id));

  const act = (fn: (ids: number[]) => Promise<BulkResult>) => {
    const ids = [...selected];
    if (ids.length === 0) return;
    startTransition(async () => {
      setNotice(await fn(ids));
      setSelected(new Set());
      router.refresh();
    });
  };

  return (
    <section>
      <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
        <h2 className="text-base font-medium">Errors</h2>
        <span className="text-xs text-muted-foreground">
          {issues.length === 0
            ? "nothing reported"
            : `${open} open · showing ${filtered.length}`}
        </span>
      </div>

      {issues.length === 0 ? (
        <>
          <p className="text-sm text-muted-foreground">
            No error has been reported in this window. When the SDK sends one, it groups into
            an issue here.
          </p>
          {/* Kept even with nothing to list: the range control lives in the chart, so
              dropping it would strand anyone who narrowed the window to an hour. */}
          {chart && <div className="mt-4">{chart}</div>}
        </>
      ) : (
        <>
          <FilterBar
            search={search}
            setSearch={setSearch}
            selects={[
              {
                label: "status",
                value: status,
                options: ["open", "resolved", "archived"],
                set: setStatus,
                allLabel: "status: all",
              },
              { label: "level", value: level, options: levels, set: setLevel },
              { label: "env", value: env, options: envs, set: setEnv },
            ]}
            defaults={{ status: "open" }}
            trailing={w && <WindowControls window={w} />}
          />

          {chart && <div className="mb-4">{chart}</div>}

          {selected.size > 0 && (
            <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2">
              <span className="text-sm font-medium">{selected.size} selected</span>
              <Button size="sm" disabled={pending} onClick={() => act((ids) => bulkResolve(ids, true))}>
                Resolve
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={pending}
                onClick={() => act((ids) => bulkResolve(ids, false))}
              >
                Unresolve
              </Button>
              <SelectBox
                disabled={pending}
                value=""
                onChange={(e) => {
                  const v = e.target.value;
                  if (v === "forever") act((ids) => bulkArchive(ids, "forever"));
                  else if (v === "1d") act((ids) => bulkArchive(ids, "for", 24));
                  else if (v === "1w") act((ids) => bulkArchive(ids, "for", 168));
                  else if (v === "recur") act((ids) => bulkArchive(ids, "recur"));
                  else if (v === "off") act((ids) => bulkArchive(ids, "off"));
                }}
                className="h-8"
              >
                <option value="">Archive…</option>
                <option value="forever">Forever</option>
                <option value="1d">For 1 day</option>
                <option value="1w">For 1 week</option>
                <option value="recur">Until it occurs again</option>
                <option value="off">Unarchive</option>
              </SelectBox>
              <SelectBox
                disabled={pending}
                value=""
                onChange={(e) => {
                  const v = e.target.value;
                  if (v) act((ids) => bulkPriority(ids, v === "none" ? "" : v));
                }}
                className="h-8"
              >
                <option value="">Set priority…</option>
                <option value="high">High</option>
                <option value="med">Med</option>
                <option value="low">Low</option>
                <option value="none">Clear</option>
              </SelectBox>
              <Button
                size="sm"
                variant="ghost"
                className="text-status-down hover:text-status-down"
                disabled={pending}
                onClick={() => setConfirmingDelete(true)}
              >
                Delete
              </Button>
              {pending && <span className="text-xs text-muted-foreground">working…</span>}
              <ConfirmDialog
                open={confirmingDelete}
                onOpenChange={setConfirmingDelete}
                title={`Delete ${selected.size} ${selected.size === 1 ? "issue" : "issues"}?`}
                description="Every stored event goes with them, stacktraces and attachments included. Resolving or archiving keeps the history; this does not."
                confirmLabel="Delete"
                destructive
                pending={pending}
                onConfirm={() => {
                  setConfirmingDelete(false);
                  act(bulkDelete);
                }}
              />
            </div>
          )}
          {notice && (
            <p className={`mb-2 text-sm ${notice.ok ? "text-status-active" : "text-status-down"}`}>
              {notice.message}
            </p>
          )}

          {filtered.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nothing matches the filter.</p>
          ) : (
            <TableCard>
              {/* table-fixed: a long unbroken title must truncate, not push the
                  other columns into an invisible horizontal scroll. */}
              <Table className="table-fixed">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8">
                      <input
                        type="checkbox"
                        checked={allSelected}
                        onChange={() =>
                          setSelected(allSelected ? new Set() : new Set(visibleIds))
                        }
                      />
                    </TableHead>
                    <TableHead>Issue</TableHead>
                    <TableHead className="w-28">Level</TableHead>
                    <TableHead className="w-36">Trend · 24h</TableHead>
                    <TableHead className="w-20 text-right">Events</TableHead>
                    <TableHead className="w-24 text-right">Age</TableHead>
                    <TableHead className="w-28">Last seen</TableHead>
                    <TableHead className="w-32">Environment</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((i) => {
                    const st = issueStatus(i);
                    return (
                      <TableRow key={i.id}>
                        <TableCell>
                          <input
                            type="checkbox"
                            checked={selected.has(i.id)}
                            onChange={() => {
                              const next = new Set(selected);
                              if (next.has(i.id)) next.delete(i.id);
                              else next.add(i.id);
                              setSelected(next);
                            }}
                          />
                        </TableCell>
                        <TableCell className="max-w-0">
                          <Link
                            href={`/apps/${slug}/errors/${i.id}`}
                            title={i.title}
                            className="block truncate text-sm font-medium hover:underline"
                          >
                            {i.title}
                          </Link>
                          <div className="truncate font-mono text-xs text-muted-foreground">
                            {i.culprit}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col items-start gap-1">
                            <StatusPill tone={levelTone(i.level)}>{i.level}</StatusPill>
                            {st !== "open" && (
                              <StatusPill tone={statusTone(st)}>{st}</StatusPill>
                            )}
                            {i.priority && (
                              <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-semibold uppercase text-muted-foreground">
                                {i.priority}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <ActivityBars
                            buckets={(i.activity ?? []).map((b) => ({
                              at: b.at,
                              beats: b.count,
                              progress_delta: 0,
                            }))}
                            className="h-6 w-28"
                          />
                        </TableCell>
                        <TableCell className="text-right font-mono text-sm tabular-nums">
                          {i.times_seen}×
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">
                          <Age iso={i.first_seen} />
                        </TableCell>
                        <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                          <Ago iso={i.last_seen} />
                        </TableCell>
                        <TableCell className="truncate font-mono text-xs text-muted-foreground">
                          {i.environment}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </TableCard>
          )}
        </>
      )}
    </section>
  );
}

export function MonitorsFiltered({
  monitors,
  viewId,
}: {
  monitors: Monitor[];
  viewId: string;
}) {
  const [search, setSearch] = useState("");
  const [env, setEnv] = useState("");

  const envs = useMemo(() => [...new Set(monitors.map((m) => m.environment))].sort(), [monitors]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return monitors.filter(
      (m) => (!env || m.environment === env) && (!q || m.slug.toLowerCase().includes(q)),
    );
  }, [monitors, search, env]);

  return (
    <div>
      <FilterBar
        search={search}
        setSearch={setSearch}
        selects={[{ label: "env", value: env, options: envs, set: setEnv }]}
      />
      {filtered.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing matches the filter.</p>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Monitor</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Last beat</TableHead>
                <TableHead className="text-right">Progress</TableHead>
                <TableHead>Activity · 1h</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((m) => (
                <TableRow key={`${m.slug}:${m.environment}`}>
                  <TableCell>
                    <Link
                      href={`/monitors/${encodeURIComponent(m.slug)}`}
                      className="font-mono text-sm font-medium hover:underline"
                    >
                      {m.slug}
                    </Link>
                    <div className="text-xs text-muted-foreground">
                      {m.environment} · expects a beat every {shortDuration(m.every_secs)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <StatusPill tone={monitorTone(m.status)}>{m.status}</StatusPill>
                    {m.open_incident_secs !== null && (
                      <div className="mt-1 text-xs text-muted-foreground">
                        for <Since secs={m.open_incident_secs} />
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">
                    <ClockAt iso={m.last_beat_at} />
                    <div className="text-xs text-muted-foreground">
                      <Ago iso={m.last_beat_at} />
                    </div>
                  </TableCell>
                  <TableCell className="text-right font-mono text-sm tabular-nums">
                    {m.last_progress ?? "—"}
                    {viewId === "breached" && <div className="text-xs text-status-idle">frozen</div>}
                  </TableCell>
                  <TableCell>
                    <ActivityBars buckets={m.activity} className="h-7 w-36" />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}
    </div>
  );
}
