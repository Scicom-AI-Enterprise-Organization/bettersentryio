"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { Search } from "lucide-react";

import type { Issue, Monitor } from "@/lib/bsio";
import { monitorTone, shortDuration } from "@/lib/bsio";
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
import { Ago, ClockAt, Since } from "@/components/bsio/time";

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

function FilterBar({
  search,
  setSearch,
  selects,
}: {
  search: string;
  setSearch: (v: string) => void;
  selects: {
    label: string;
    value: string;
    options: string[];
    set: (v: string) => void;
  }[];
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search…"
          className="h-8 w-64 pl-8 text-sm"
        />
      </div>
      {selects.map((s) => (
        <select
          key={s.label}
          value={s.value}
          onChange={(e) => s.set(e.target.value)}
          className="h-8 rounded-md border border-input bg-transparent px-2 text-sm text-foreground"
        >
          <option value="">{s.label}: all</option>
          {s.options.map((o) => (
            <option key={o} value={o}>
              {s.label}: {o}
            </option>
          ))}
        </select>
      ))}
    </div>
  );
}

export function ErrorIssuesFiltered({ slug, issues }: { slug: string; issues: Issue[] }) {
  const [search, setSearch] = useState("");
  const [level, setLevel] = useState("");
  const [env, setEnv] = useState("");

  const levels = useMemo(() => [...new Set(issues.map((i) => i.level))].sort(), [issues]);
  const envs = useMemo(() => [...new Set(issues.map((i) => i.environment))].sort(), [issues]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return issues.filter(
      (i) =>
        (!level || i.level === level) &&
        (!env || i.environment === env) &&
        (!q || i.title.toLowerCase().includes(q) || i.culprit.toLowerCase().includes(q)),
    );
  }, [issues, search, level, env]);

  return (
    <section>
      <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
        <h2 className="text-base font-medium">Errors</h2>
        <span className="text-xs text-muted-foreground">
          {issues.length === 0
            ? "nothing reported"
            : filtered.length === issues.length
              ? `${issues.length} open issue${issues.length === 1 ? "" : "s"}`
              : `${filtered.length} of ${issues.length} issues`}
        </span>
      </div>

      {issues.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No error has been reported. When the SDK sends one, it groups into an issue here.
        </p>
      ) : (
        <>
          <FilterBar
            search={search}
            setSearch={setSearch}
            selects={[
              { label: "level", value: level, options: levels, set: setLevel },
              { label: "env", value: env, options: envs, set: setEnv },
            ]}
          />
          {filtered.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nothing matches the filter.</p>
          ) : (
            <TableCard>
              {/* table-fixed: a long unbroken title must truncate, not push the
                  other columns into an invisible horizontal scroll. */}
              <Table className="table-fixed">
                <TableHeader>
                  <TableRow>
                    <TableHead>Issue</TableHead>
                    <TableHead className="w-24">Level</TableHead>
                    <TableHead className="w-36">Trend · 24h</TableHead>
                    <TableHead className="w-20 text-right">Events</TableHead>
                    <TableHead className="w-24 text-right">Age</TableHead>
                    <TableHead className="w-28">Last seen</TableHead>
                    <TableHead className="w-32">Environment</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((i) => (
                    <TableRow key={i.id}>
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
                        <StatusPill tone={levelTone(i.level)}>{i.level}</StatusPill>
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
                        {shortDuration(Math.max(60, (Date.now() - Date.parse(i.first_seen)) / 1000))}
                      </TableCell>
                      <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                        <Ago iso={i.last_seen} />
                      </TableCell>
                      <TableCell className="truncate font-mono text-xs text-muted-foreground">
                        {i.environment}
                      </TableCell>
                    </TableRow>
                  ))}
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
