import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowRight } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import {
  getApp,
  getIncidents,
  getIssues,
  getProjectAnalytics,
  getProjectSeries,
  getReleases,
  issueStatus,
  monitorTone,
  shortDuration,
  uptimeLabel,
} from "@/lib/bsio";
import type {
  AnalyticsRow,
  Incident,
  Issue,
  Monitor,
  ProjectAnalytics,
  ProjectSeries,
  ReleaseRow,
} from "@/lib/bsio";
import { DEFAULT_RANGE, RANGES, customReady, levelColor, resolveWindow } from "@/lib/ranges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { StatCard } from "@/components/stat-card";
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
import { OccurrenceChart } from "@/components/bsio/occurrence-chart";
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { WindowControls } from "@/components/bsio/window-controls";
import { Ago, StampAt } from "@/components/bsio/time";
import { Breakdown, Heatmap, Lifecycle, Matrix, NewIssuesPerDay, Sparkline } from "./panels";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return { title: `Analytics · ${slug}` };
}

/**
 * The span an hour-of-day grid is worth drawing for. Below a day it would fold six cells
 * onto one row; past a month it would mean pulling thousands of hourly buckets to answer
 * a question the thirty-day grid already answers.
 */
const HEATMAP_MIN_SECS = 86_400;
const HEATMAP_MAX_SECS = 31 * 86_400;

/** Preset spans in seconds, for the figures this page derives rather than fetches. */
const RANGE_SECONDS: Record<string, number> = {
  "1h": 3_600,
  "6h": 21_600,
  "24h": 86_400,
  "7d": 604_800,
  "30d": 2_592_000,
  "90d": 7_776_000,
};

/**
 * How many issue rows the issue-level panels read. The engine defaults to 100 and caps at
 * 500; 200 keeps the payload sane while making the cap irrelevant for any project small
 * enough for one person to be reading this page about.
 */
const ISSUE_ROWS = 200;

/**
 * What the engine's Discover field names mean to a person. The hints are the difference
 * between a panel that is read and one that is guessed at: "transaction" is Sentry's word,
 * not ours, and an empty Release panel is a configuration fact worth explaining.
 */
const DIMENSIONS: Record<string, { label: string; hint?: string }> = {
  "error.type": { label: "Exception type", hint: "The class raised, as the SDK reported it." },
  environment: { label: "Environment" },
  release: { label: "Release", hint: "From release= in sentry_sdk.init()." },
  transaction: { label: "Transaction", hint: "The endpoint or task the SDK named." },
  server_name: { label: "Host", hint: "The machine the event came from." },
};

/**
 * A dimension's name, for the fixed ones and for the tags the engine discovered. A panel
 * headed `tags[gpu]` is the query, not the question.
 */
function dimensionLabel(field: string): { label: string; hint?: string } {
  const fixed = DIMENSIONS[field];
  if (fixed) return fixed;
  const tag = /^tags\[(.+)\]$/.exec(field);
  if (tag) {
    return { label: tag[1], hint: "A tag this project's own SDK sends." };
  }
  return { label: field };
}

/**
 * The whole project, measured: errors and availability on one page.
 *
 * The issue list answers "what is broken"; this answers "how much, is it getting worse,
 * where is it concentrated, and did the loops stay up" — the questions you actually ask
 * after a release. Both time controls live in the URL, so "this project over the last 90
 * days in daily buckets" is a link somebody else can open and see the same thing.
 *
 * Every figure is fetched from the endpoint that owns it rather than recomputed here: the
 * chart from the project series, the aggregates from the analytics endpoint, session health
 * from releases, availability from the monitors and the incident log. Nothing on this page
 * derives a number a different endpoint would answer differently.
 */
export default async function ProjectAnalyticsPage({
  params,
  searchParams,
}: {
  params: Promise<{ slug: string }>;
  searchParams: Promise<{ range?: string; interval?: string }>;
}) {
  await requireUser();
  const { slug } = await params;
  const w = resolveWindow(await searchParams);
  // Two timestamps or a span: everything below keys off the *resolved* window, never off
  // the preset name, so a custom range narrows the tables and the histogram too. A custom
  // mode with the dates not yet filled in resolves to the default span rather than to an
  // empty window (see customReady).
  const custom = customReady(w);
  const preset = custom ? null : w.range === "custom" ? DEFAULT_RANGE : w.range;
  const windowSecs = custom
    ? (Date.parse(w.end!) - Date.parse(w.start!)) / 1000
    : (RANGE_SECONDS[preset!] ?? RANGE_SECONDS[DEFAULT_RANGE]);
  const windowStart = custom ? Date.parse(w.start!) : Date.now() - windowSecs * 1000;
  const windowEnd = custom ? Date.parse(w.end!) : Date.now();
  // "vs previous 30 days" for a preset; "vs previous window" when the user picked two
  // timestamps, because there is no name for how long that is.
  const windowLabel = custom
    ? "window"
    : (RANGES.find((r) => r.value === preset)?.label ?? preset!).replace(/^Last /, "");

  const [appResult, seriesResult, statsResult, heatResult, issueResult, incidentResult, releaseResult] =
    await Promise.all([
      getApp(slug),
      getProjectSeries(slug, w),
      getProjectAnalytics(slug, w),
      // Fixed 1h buckets: the chart's interval is a control, and at 30 days it snaps to
      // 12h, which cannot carry an hour of the day.
      windowSecs >= HEATMAP_MIN_SECS && windowSecs <= HEATMAP_MAX_SECS
        ? getProjectSeries(slug, { ...w, interval: "1h" })
        : Promise.resolve(null),
      getIssues(slug, { resolved: true, archived: true, window: w, limit: ISSUE_ROWS }),
      getIncidents(),
      getReleases(slug, Math.max(1, Math.min(90, Math.round(windowSecs / 86_400)))),
    ]);

  if (!appResult.ok) {
    if (appResult.error.includes("404")) notFound();
    return (
      <Alert variant="destructive">
        <AlertTitle>The engine is unreachable</AlertTitle>
        <AlertDescription>{appResult.error}</AlertDescription>
      </Alert>
    );
  }
  const { app, monitors } = appResult.data;
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  // Incidents are global, so they are narrowed to this project's own monitors and to the
  // window on the page — the same two filters the issue views apply.
  const mine = new Set(monitors.map((m) => m.slug));
  const incidents = (incidentResult.ok ? incidentResult.data.incidents : []).filter((i) => {
    const at = Date.parse(i.opened_at);
    return mine.has(i.monitor) && at >= windowStart && at <= windowEnd;
  });
  const issues = issueResult.ok ? issueResult.data.issues : [];
  const releases = releaseResult.ok ? releaseResult.data.releases : [];

  return (
    <div className="space-y-6">
      <ProjectHeader
        app={app}
        title="Analytics"
        subtitle="Event volume for the whole project over a window and a bucket width you choose, the same events sliced by release, host and endpoint, when in the week they arrive, and whether the loops behind them stayed up."
      />

      {/* WindowControls is a fragment of controls, so the row is the page's to provide.
          It sits under the header rather than in the chart's corner because the window
          governs everything below it — tiles, panels, tables — not just the chart. */}
      <div className="flex flex-wrap items-center gap-2">
        <WindowControls window={w} />
      </div>

      {!app.connected ? (
        <div className="rounded-xl border border-border bg-card p-8 shadow-xs">
          <h2 className="text-lg font-semibold tracking-tight">{app.name} has never reported</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            There is nothing to measure yet. Point <code className="font-mono">sentry_sdk</code> at
            this project&apos;s DSN and the charts here fill themselves in — no further
            configuration, and nothing to switch on.
          </p>
          <Button asChild size="sm" className="mt-4">
            <Link href={`/apps/${app.slug}/setup`}>
              Finish setup
              <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </Button>
        </div>
      ) : (
        <>
          {statsResult.ok && (
            <Tiles
              stats={statsResult.data}
              windowLabel={windowLabel}
              issues={issueResult.ok ? issues : null}
              windowStart={windowStart}
              incidents={incidentResult.ok ? incidents : null}
              monitors={monitors}
            />
          )}

          {!seriesResult.ok ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load the volume chart</AlertTitle>
              <AlertDescription>{seriesResult.error}</AlertDescription>
            </Alert>
          ) : (
            <div className="space-y-2">
              <OccurrenceChart
                title="Events over time"
                rows={seriesResult.data.buckets.map((b) => ({
                  at: b.at,
                  // Zero-fill: a level absent from one bucket is a gap in the stack, and
                  // recharts renders a gap as a hole rather than as nothing.
                  ...Object.fromEntries(
                    seriesResult.data.levels.map((l) => [l, b.counts[l] ?? 0]),
                  ),
                }))}
                series={seriesResult.data.levels.map((l) => ({
                  key: l,
                  label: l,
                  color: levelColor(l),
                }))}
                intervalSeconds={seriesResult.data.interval_s}
                range={w.range}
                interval={w.interval ?? "auto"}
                controls={false}
                height="h-64"
              />
              <Peak series={seriesResult.data} />
            </div>
          )}

          {heatResult?.ok && heatResult.data.buckets.length > 0 && (
            <Heatmap series={heatResult.data} timeZone={timeZone} />
          )}

          {!statsResult.ok ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load the figures</AlertTitle>
              <AlertDescription>{statsResult.error}</AlertDescription>
            </Alert>
          ) : (
            <Breakdowns stats={statsResult.data} issues={issueResult.ok ? issues : null} />
          )}

          {issueResult.ok && issues.length > 0 && (
            <div className="grid gap-4 lg:grid-cols-3">
              {windowSecs >= 7 * 86_400 && (
                <div className="lg:col-span-2">
                  <NewIssuesPerDay issues={issues} windowStart={windowStart} />
                </div>
              )}
              <Lifecycle issues={issues} windowStart={windowStart} />
            </div>
          )}

          {releases.length > 0 && <ReleaseHealth slug={app.slug} releases={releases} />}

          {monitors.length > 0 && (
            <LoopReliability slug={app.slug} monitors={monitors} incidents={incidents} />
          )}

          {statsResult.ok && (
            <>
              <TopIssues slug={app.slug} stats={statsResult.data} issues={issues} />
              <p className="text-[13px] text-muted-foreground">
                Event figures cover <StampAt iso={statsResult.data.start} /> to{" "}
                <StampAt iso={statsResult.data.end} /> exactly; the chart&apos;s first bucket is
                floored to the interval, so it can start a little earlier. Breakdowns are grouped
                from the events themselves — a dimension no SDK sets is left out rather than shown
                empty. Issue-level panels read the window&apos;s issue list, which is fetched{" "}
                {ISSUE_ROWS} rows deep, and session health comes from the SDK&apos;s own session
                tracking rather than from events.
              </p>
            </>
          )}
        </>
      )}
    </div>
  );
}

/**
 * The headline figures, each against the window before it. A count alone says nothing
 * about direction: 180 errors is reassuring after 900 and alarming after 4.
 *
 * Volume and quality come from the analytics endpoint's level split, which partitions the
 * window exactly. Availability comes from the monitors and the incident log, because a
 * project that reported no errors because it stopped running is not a healthy project.
 */
function Tiles({
  stats,
  windowLabel,
  issues,
  windowStart,
  incidents,
  monitors,
}: {
  stats: ProjectAnalytics;
  windowLabel: string;
  issues: Issue[] | null;
  windowStart: number;
  incidents: Incident[] | null;
  monitors: Monitor[];
}) {
  const of = (...levels: string[]) =>
    stats.levels.filter((l) => levels.includes(l.level)).reduce((n, l) => n + l.count, 0);
  const serious = of("fatal", "error");
  const warnings = of("warning");
  // "New" is all-time first_seen inside this window — an issue seen for the first time,
  // not merely an issue seen again. The window's own first sighting would call a weekly
  // cron failure new every week.
  const fresh = issues?.filter((i) => Date.parse(i.first_seen) >= windowStart).length ?? null;

  const resolved = (incidents ?? []).filter((i) => i.resolved_at);
  const stillOpen = (incidents ?? []).length - resolved.length;
  const mttr = resolved.length
    ? resolved.reduce((n, i) => n + i.duration_secs, 0) / resolved.length
    : null;
  const watched = monitors.filter((m) => m.uptime_observed_secs > 0);
  const uptime = watched.length
    ? watched.reduce((n, m) => n + m.uptime_pct, 0) / watched.length
    : null;

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <StatCard
        label="Events"
        value={stats.total.toLocaleString()}
        sub={delta(stats.total, stats.previous.total, windowLabel)}
        tone={trend(stats.total, stats.previous.total)}
      />
      <StatCard
        label="Issues affected"
        value={stats.issues.toLocaleString()}
        sub={delta(stats.issues, stats.previous.issues, windowLabel)}
        tone={stats.issues > 0 ? "default" : "muted"}
      />
      <StatCard
        label="New issues"
        value={fresh === null ? "—" : fresh.toLocaleString()}
        sub={fresh === null ? "issue list unavailable" : "first ever seen in this window"}
        tone={fresh ? "warning" : "muted"}
      />
      <StatCard
        label="Errors & fatals"
        value={serious.toLocaleString()}
        sub={`${percent(serious, stats.total)} of events`}
        tone={serious > 0 ? "negative" : "muted"}
      />
      <StatCard
        label="Warnings"
        value={warnings.toLocaleString()}
        sub={`${percent(warnings, stats.total)} of events`}
        tone={warnings > 0 ? "warning" : "muted"}
      />
      <StatCard
        label="Incidents"
        value={incidents === null ? "—" : incidents.length.toLocaleString()}
        sub={
          incidents === null
            ? "incident log unavailable"
            : incidents.length === 0
              ? "no loop went missing"
              : stillOpen > 0
                ? `${stillOpen} still open`
                : "all resolved"
        }
        tone={stillOpen > 0 ? "negative" : incidents?.length ? "warning" : "muted"}
      />
      <StatCard
        label="Mean time to resolve"
        value={mttr === null ? "—" : shortDuration(mttr)}
        sub={
          mttr === null
            ? "nothing resolved in this window"
            : `across ${resolved.length} ${resolved.length === 1 ? "incident" : "incidents"}`
        }
        tone={mttr === null ? "muted" : "default"}
      />
      <StatCard
        label="Loop uptime"
        value={uptime === null ? "—" : uptimeLabel(uptime)}
        sub={
          uptime === null
            ? monitors.length === 0
              ? "no monitors yet"
              : "not observed long enough"
            : `mean across ${watched.length} ${watched.length === 1 ? "monitor" : "monitors"}`
        }
        tone={uptime === null ? "muted" : uptime >= 99.5 ? "positive" : uptime >= 95 ? "warning" : "negative"}
      />
    </div>
  );
}

/**
 * The chart's own shape, in words: which bucket was the worst and how much of the window
 * was quiet. A tall bar is obvious at a glance but its value is not, and "half the window
 * had nothing in it" is invisible on a chart whose baseline is full of them.
 */
function Peak({ series }: { series: ProjectSeries }) {
  const totals = series.buckets.map((b) => Object.values(b.counts).reduce((n, v) => n + v, 0));
  if (totals.length === 0) return null;
  const peak = totals.reduce((best, v, n) => (v > totals[best] ? n : best), 0);
  const quiet = totals.filter((v) => v === 0).length;
  if (totals[peak] === 0) {
    return (
      <p className="px-1 text-[13px] text-muted-foreground">
        Nothing arrived in any of the {totals.length} buckets in this window.
      </p>
    );
  }

  return (
    <p className="px-1 text-[13px] text-muted-foreground">
      Busiest bucket <StampAt iso={series.buckets[peak].at} /> with{" "}
      <span className="font-mono tabular-nums text-foreground">
        {totals[peak].toLocaleString()}
      </span>{" "}
      {totals[peak] === 1 ? "event" : "events"} · {quiet} of {totals.length} buckets were empty.
    </p>
  );
}

/**
 * The same events, sliced. Levels first — it is the split the tiles above summarise, and
 * the only one that carries a direction, because the endpoint returns the previous
 * window's split alongside this one's.
 */
function Breakdowns({ stats, issues }: { stats: ProjectAnalytics; issues: Issue[] | null }) {
  const levelRows: AnalyticsRow[] = stats.levels.map((l) => ({
    value: l.level,
    count: l.count,
    issues: l.issues,
  }));
  const wasByLevel = Object.fromEntries(stats.previous.levels.map((l) => [l.level, l.count]));

  // Triage state and priority are properties of the issue, not of an event, so these two
  // panels count issues — and say so, rather than implying a share of events. Each is shown
  // only when it has more than one value: a card reading "open: 100%" is a card that says
  // nothing the tiles above have not already said.
  const statusRows = issues ? tally(issues.map((i) => issueStatus(i))) : [];
  const priorityRows = issues ? tally(issues.map((i) => i.priority || "unset")) : [];

  return (
    <div className="space-y-4">
      {stats.matrix.rows.length > 0 && stats.matrix.columns.length > 0 && (
        <Matrix
          matrix={stats.matrix}
          label="Environment × level"
          rowLabel="environment"
          columnLabel="level"
          colorOf={levelColor}
        />
      )}
      {/* items-start so a two-row panel is two rows tall, rather than stretched to match
          the tallest card in its row. */}
      <div className="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
      {levelRows.length > 0 && (
        <Breakdown
          label="Level"
          hint="As the SDK classified it, against the previous window."
          rows={levelRows}
          total={stats.total}
          colorOf={levelColor}
          previous={wasByLevel}
        />
      )}
      {stats.breakdowns.map((b) => (
        <Breakdown
          key={b.field}
          label={dimensionLabel(b.field).label}
          hint={dimensionLabel(b.field).hint}
          rows={b.rows}
          truncated={b.truncated}
          total={stats.total}
        />
      ))}
      {statusRows.length > 1 && (
        <Breakdown
          label="Triage state"
          hint="Issues active in this window, by what somebody did with them."
          rows={statusRows}
          total={statusRows.reduce((n, r) => n + r.count, 0)}
          unit="issues"
          colorOf={statusColor}
        />
      )}
      {priorityRows.length > 1 && (
        <Breakdown
          label="Priority"
          hint="Issues active in this window, by assigned priority."
          rows={priorityRows}
          total={priorityRows.reduce((n, r) => n + r.count, 0)}
          unit="issues"
        />
      )}
      </div>
    </div>
  );
}

/**
 * Release health, from the SDK's own session tracking rather than from events: an error
 * count cannot tell a release that crashed twice as often from one that simply ran twice
 * as much, and crash-free sessions can.
 */
function ReleaseHealth({ slug, releases }: { slug: string; releases: ReleaseRow[] }) {
  const shown = [...releases].sort((a, b) => b.sessions - a.sessions).slice(0, 5);
  // Weighted by sessions, not a mean of the percentages: a release with four sessions
  // would otherwise count as much as one with forty thousand.
  const sessions = releases.reduce((n, r) => n + r.sessions, 0);
  const crashed = releases.reduce((n, r) => n + r.crashed, 0);
  const crashFree = sessions > 0 ? ((sessions - crashed) / sessions) * 100 : null;

  return (
    <section>
      <div className="mb-3 flex items-baseline justify-between gap-3 border-b border-border pb-2">
        <div className="flex items-baseline gap-3">
          <h2 className="text-base font-medium">Release health</h2>
          <span className="text-xs text-muted-foreground">
            busiest {shown.length} of {releases.length} in this window ·{" "}
            {sessions.toLocaleString()} {sessions === 1 ? "session" : "sessions"}
            {crashFree !== null && ` · ${crashFree.toFixed(2)}% crash-free overall`}
          </span>
        </div>
        <Link
          href={`/apps/${slug}/releases`}
          className="text-[13px] text-primary hover:underline"
        >
          All releases
        </Link>
      </div>
      <TableCard>
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[34%]">Release</TableHead>
              <TableHead className="w-[16%]">Environment</TableHead>
              <TableHead className="w-[14%] text-right">Crash-free</TableHead>
              <TableHead className="w-[12%] text-right">Sessions</TableHead>
              <TableHead className="w-[12%] text-right">Crashed</TableHead>
              <TableHead className="w-[12%] text-right">Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((r) => (
              <TableRow key={`${r.release}|${r.environment}`}>
                <TableCell className="truncate font-mono text-[13px]">
                  {r.release || "(no release)"}
                </TableCell>
                <TableCell className="truncate font-mono text-[13px] text-muted-foreground">
                  {r.environment}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums">
                  <span
                    className={
                      r.crash_free >= 99.5
                        ? "text-status-active"
                        : r.crash_free >= 95
                          ? "text-status-idle"
                          : "text-status-down"
                    }
                  >
                    {r.crash_free.toFixed(2)}%
                  </span>
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums">
                  {r.sessions.toLocaleString()}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums text-muted-foreground">
                  {r.crashed.toLocaleString()}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-muted-foreground">
                  <Ago iso={r.last_seen} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableCard>
    </section>
  );
}

/**
 * Availability beside volume, because they explain each other: a project whose error count
 * fell to zero while a loop went missing did not get better.
 *
 * Uptime is the engine's own measure over its observed window, not a figure this page
 * computes — three clocks would disagree.
 */
function LoopReliability({
  slug,
  monitors,
  incidents,
}: {
  slug: string;
  monitors: Monitor[];
  incidents: Incident[];
}) {
  const perMonitor = new Map<string, number>();
  for (const i of incidents) {
    perMonitor.set(i.monitor, (perMonitor.get(i.monitor) ?? 0) + 1);
  }
  // duration_secs is measured by the engine and runs for an incident that is still open,
  // so total downtime includes it. It is not recomputed from opened_at against the
  // browser clock: the two machines disagree, and this is a number people act on.
  const downtime = incidents.reduce((n, i) => n + i.duration_secs, 0);
  const open = incidents.filter((i) => !i.resolved_at);

  return (
    <section>
      <div className="mb-3 flex items-baseline justify-between gap-3 border-b border-border pb-2">
        <div className="flex items-baseline gap-3">
          <h2 className="text-base font-medium">Loop reliability</h2>
          <span className="text-xs text-muted-foreground">
            {monitors.length} {monitors.length === 1 ? "monitor" : "monitors"} ·{" "}
            {incidents.length} {incidents.length === 1 ? "incident" : "incidents"} in this window
            {downtime > 0 && ` · ${shortDuration(downtime)} of downtime`}
            {open.length > 0 &&
              ` · ${open.length} still open, ${shortDuration(
                open.reduce((n, i) => n + i.duration_secs, 0),
              )} and counting`}
          </span>
        </div>
        <Link href="/incidents" className="text-[13px] text-primary hover:underline">
          Incident log
        </Link>
      </div>
      <TableCard>
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[24%]">Monitor</TableHead>
              <TableHead className="w-[12%]">State</TableHead>
              <TableHead className="w-[16%]">Last hour</TableHead>
              <TableHead className="w-[12%] text-right">Uptime</TableHead>
              <TableHead className="w-[12%] text-right">Beats 24h</TableHead>
              <TableHead className="w-[12%] text-right">Incidents</TableHead>
              <TableHead className="w-[12%] text-right">Last beat</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {monitors.map((m) => (
              <TableRow key={m.slug}>
                <TableCell className="truncate font-mono text-[13px]">
                  <Link
                    href={`/monitors/${encodeURIComponent(m.slug)}`}
                    className="hover:text-primary hover:underline"
                  >
                    {m.slug}
                  </Link>
                </TableCell>
                <TableCell>
                  <StatusPill tone={monitorTone(m.status)}>{m.status}</StatusPill>
                </TableCell>
                <TableCell>
                  <ActivityBars buckets={m.activity} className="h-6" />
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums">
                  {m.uptime_observed_secs > 0 ? uptimeLabel(m.uptime_pct) : "—"}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums text-muted-foreground">
                  {m.beats_24h.toLocaleString()}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] tabular-nums">
                  {perMonitor.get(m.slug) ?? 0}
                </TableCell>
                <TableCell className="text-right font-mono text-[13px] text-muted-foreground">
                  <Ago iso={m.last_beat_at} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableCard>
      <p className="mt-2 text-[13px] text-muted-foreground">
        Uptime is measured over each monitor&apos;s own observed window, which starts at its
        first beat — a monitor created yesterday cannot report a month.{" "}
        <Link href={`/apps/${slug}/issues/breached`} className="text-primary hover:underline">
          Breached metrics
        </Link>{" "}
        explains what each state means.
      </p>
    </section>
  );
}

/**
 * The leaderboard, which is the part of analytics that leads somewhere: a share of events
 * is a number, but the issue behind it is a page you can act on. The sparkline is why the
 * row is worth reading — 91 events says nothing about whether it is still happening.
 */
function TopIssues({
  slug,
  stats,
  issues,
}: {
  slug: string;
  stats: ProjectAnalytics;
  issues: Issue[];
}) {
  if (stats.top_issues.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-border p-8">
        <h2 className="text-lg font-semibold tracking-tight">Nothing reported in this window</h2>
        <p className="mt-1 text-[15px] text-muted-foreground">
          No events arrived between <StampAt iso={stats.start} /> and now. Widen the window above,
          or check a quiet project against its{" "}
          <Link href={`/apps/${slug}/issues/outages`} className="text-primary hover:underline">
            issue list
          </Link>
          .
        </p>
      </div>
    );
  }

  const byID = new Map(issues.map((i) => [i.id, i]));
  const top3 = stats.top_issues.slice(0, 3).reduce((n, i) => n + i.count, 0);

  return (
    <section>
      <div className="mb-3 flex flex-wrap items-baseline gap-3 border-b border-border pb-2">
        <h2 className="text-base font-medium">Busiest issues</h2>
        <span className="text-xs text-muted-foreground">
          top {stats.top_issues.length} of {stats.issues.toLocaleString()} in this window · the
          busiest three are {percent(top3, stats.total)} of every event
        </span>
      </div>
      <TableCard>
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[40%]">Issue</TableHead>
              <TableHead className="w-[10%]">Level</TableHead>
              <TableHead className="w-[10%]">State</TableHead>
              <TableHead className="w-[10%] text-right">Events</TableHead>
              <TableHead className="w-[8%] text-right">Share</TableHead>
              <TableHead className="w-[10%]">Last 24h</TableHead>
              <TableHead className="w-[12%] text-right">Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {stats.top_issues.map((i) => {
              const live = byID.get(i.id);
              return (
                <TableRow key={i.id}>
                  <TableCell className="min-w-0">
                    <Link
                      href={`/apps/${slug}/errors/${i.id}`}
                      className="block truncate font-medium hover:text-primary hover:underline"
                    >
                      {i.title}
                    </Link>
                    <span className="block truncate font-mono text-xs text-muted-foreground">
                      {i.culprit}
                      {live && (
                        <>
                          {i.culprit && " · "}first seen <Ago iso={live.first_seen} />
                        </>
                      )}
                    </span>
                  </TableCell>
                  <TableCell>
                    <StatusPill tone={levelTone(i.level)}>{i.level}</StatusPill>
                  </TableCell>
                  <TableCell>
                    {live ? (
                      <StatusPill tone={stateTone(issueStatus(live))}>
                        {issueStatus(live)}
                      </StatusPill>
                    ) : (
                      <span className="text-[13px] text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums">
                    {i.count.toLocaleString()}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums text-muted-foreground">
                    {percent(i.count, stats.total)}
                  </TableCell>
                  <TableCell>
                    <Sparkline buckets={live?.activity ?? null} color={levelColor(i.level)} />
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] text-muted-foreground">
                    <Ago iso={i.last_seen} />
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </TableCard>
    </section>
  );
}

/** Counts occurrences of a value, biggest first — the shape a Breakdown consumes. */
function tally(values: string[]): AnalyticsRow[] {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  return [...counts.entries()]
    .map(([value, count]) => ({ value, count, issues: count }))
    .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value));
}

/**
 * Period-over-period in words. A percentage against zero is not a percentage, so those two
 * cases say what actually happened instead of printing ∞% or a silent dash.
 */
function delta(now: number, before: number, windowLabel: string): string {
  if (before === 0 && now === 0) return `nothing in this or the previous ${windowLabel}`;
  if (before === 0) return "first activity in this window";
  if (now === 0) return `all quiet — ${before.toLocaleString()} in the previous ${windowLabel}`;
  const pct = ((now - before) / before) * 100;
  if (Math.abs(pct) < 1) return `level with the previous ${windowLabel}`;
  const sign = pct > 0 ? "+" : "−";
  return `${sign}${Math.abs(pct).toFixed(0)}% vs previous ${windowLabel}`;
}

/** Direction, not judgement: fewer events is good news, more is not. */
function trend(now: number, before: number): "default" | "positive" | "negative" {
  if (before === 0 || now === before) return "default";
  return now > before ? "negative" : "positive";
}

/** A share reads as "—" when there is nothing to divide, never as 0% or NaN%. */
function percent(n: number, total: number): string {
  if (total <= 0) return "—";
  const pct = (n / total) * 100;
  return `${pct >= 10 || pct === 0 ? pct.toFixed(0) : pct.toFixed(1)}%`;
}

function levelTone(level: string): StatusTone {
  switch (level) {
    case "fatal":
    case "error":
      return "down";
    case "warning":
      return "idle";
    case "info":
      return "init";
    default:
      return "muted";
  }
}

function stateTone(state: "open" | "resolved" | "archived"): StatusTone {
  if (state === "resolved") return "active";
  if (state === "archived") return "muted";
  return "init";
}

/** Triage state keeps the same colours the pills use, so the panel matches the table. */
function statusColor(state: string): string {
  if (state === "resolved") return "var(--status-active)";
  if (state === "archived") return "var(--muted-foreground)";
  return "var(--status-init)";
}
