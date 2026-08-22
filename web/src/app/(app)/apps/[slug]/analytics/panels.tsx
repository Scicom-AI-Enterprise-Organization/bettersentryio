import type { AnalyticsMatrix, AnalyticsRow, Issue, ProjectSeries } from "@/lib/bsio";

/**
 * The panels beside the volume chart. Server components on purpose — none of this is
 * interactive, so none of it needs to reach the browser as JavaScript.
 */

/**
 * One dimension's top values: the same events sliced by release, environment, host,
 * endpoint, exception class, level, triage state or priority.
 *
 * The bar is scaled against the biggest row rather than the window total, because the
 * question a breakdown answers is "which of these dominates" — against the total, five
 * rows of 4% each are five identical slivers, and the shape says nothing.
 */
export function Breakdown({
  label,
  hint,
  rows,
  truncated,
  total,
  colorOf,
  unit = "events",
  previous,
}: {
  label: string;
  /** What the dimension actually is, for the dimensions whose name is not obvious. */
  hint?: string;
  rows: AnalyticsRow[];
  truncated?: boolean;
  total: number;
  /** Levels colour their own bars; every other dimension is a neutral magnitude. */
  colorOf?: (value: string) => string;
  /** Some panels count issues rather than events, and must not claim otherwise. */
  unit?: "events" | "issues";
  /** Same values in the previous window, keyed by value — renders a direction per row. */
  previous?: Record<string, number>;
}) {
  const max = rows.reduce((n, r) => Math.max(n, r.count), 0);

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-semibold tracking-tight">{label}</h3>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {truncated
            ? `top ${rows.length}`
            : `${rows.length} ${rows.length === 1 ? "value" : "values"}`}
        </span>
      </div>
      {hint && <p className="-mt-2 mb-3 text-[11px] text-muted-foreground">{hint}</p>}
      <ul className="space-y-2.5">
        {rows.map((r) => {
          const was = previous?.[r.value];
          return (
            <li key={r.value || "(none)"} title={tooltip(r, unit)}>
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate font-mono text-[13px]">
                  {r.value || <span className="text-muted-foreground">(not set)</span>}
                </span>
                <span className="shrink-0 font-mono text-[13px] tabular-nums text-muted-foreground">
                  {was !== undefined && <Direction now={r.count} before={was} />}
                  {r.count.toLocaleString()} · {percent(r.count, total)}
                </span>
              </div>
              <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full"
                  style={{
                    width: `${max > 0 ? Math.max(2, (r.count / max) * 100) : 0}%`,
                    background: colorOf ? colorOf(r.value) : "var(--primary)",
                  }}
                />
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/**
 * A row's direction against the window before it. Arrows rather than a signed number:
 * this sits inside a figure, and two numbers in one cell read as one wrong number.
 */
function Direction({ now, before }: { now: number; before: number }) {
  if (before === 0) {
    return <span className="mr-1.5 text-status-idle">new</span>;
  }
  const pct = ((now - before) / before) * 100;
  if (Math.abs(pct) < 5) return <span className="mr-1.5 text-muted-foreground">=</span>;
  return (
    <span className={pct > 0 ? "mr-1.5 text-status-down" : "mr-1.5 text-status-active"}>
      {pct > 0 ? "▲" : "▼"}
      {Math.abs(pct) >= 1000 ? "999+" : Math.abs(pct).toFixed(0)}%
    </span>
  );
}

/**
 * When errors arrive, rather than how many: one cell per weekday and hour, summed over
 * the whole window.
 *
 * This is the panel that separates "the service is broken" from "the 3am batch job is
 * broken" — a volume chart with a long window cannot show it, because the shape repeats
 * every day and the axis has no room to say so.
 *
 * Built from a second series call at a fixed one-hour interval: the chart's own interval
 * is a control, and at 30 days it snaps to 12h buckets, which cannot carry an hour.
 */
export function Heatmap({ series, timeZone }: { series: ProjectSeries; timeZone: string }) {
  // Monday-first: an operations week reads Mon→Sun, and Sunday-first splits the weekend.
  const grid: number[][] = Array.from({ length: 7 }, () => Array(24).fill(0));
  for (const b of series.buckets) {
    const events = Object.values(b.counts).reduce((n, v) => n + v, 0);
    if (events === 0) continue;
    const at = new Date(b.at);
    grid[(at.getDay() + 6) % 7][at.getHours()] += events;
  }

  // The busiest cell, said in words. A dark square is easy to miss and impossible to
  // read a value off; the sentence is what somebody repeats in a standup.
  let peak = { day: 0, hour: 0, count: 0 };
  const byHour = Array(24).fill(0);
  const byDay = Array(7).fill(0);
  for (let d = 0; d < 7; d++) {
    for (let h = 0; h < 24; h++) {
      const v = grid[d][h];
      byHour[h] += v;
      byDay[d] += v;
      if (v > peak.count) peak = { day: d, hour: h, count: v };
    }
  }
  const busiestHour = byHour.indexOf(Math.max(...byHour));
  const busiestDay = byDay.indexOf(Math.max(...byDay));

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-1 flex flex-wrap items-baseline justify-between gap-3">
        <div className="flex flex-wrap items-baseline gap-3">
          <h3 className="text-sm font-semibold tracking-tight">When errors arrive</h3>
          <span className="font-mono text-[13px] tabular-nums text-muted-foreground">
            hour of day · {timeZone}
          </span>
        </div>
        <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          none
          {[0, 1, 3, 8, 20].map((v) => (
            <span
              key={v}
              className="inline-block h-3 w-3 rounded-[2px]"
              style={{ background: intensity(v) }}
              aria-hidden
            />
          ))}
          many
        </div>
      </div>
      <p className="mb-3 text-[11px] text-muted-foreground">
        Every hour in the window folded onto one week. A column dark every day is a
        schedule; a single dark cell is an incident.
      </p>

      {/* Twenty-four columns do not fit a narrow window, so the grid scrolls rather than
          squeezing cells below the point where a colour is readable. */}
      <div className="overflow-x-auto">
        <div className="min-w-[560px]">
          <div className="mb-1 grid grid-cols-[2.25rem_repeat(24,minmax(0,1fr))] gap-[2px]">
            <span />
            {Array.from({ length: 24 }, (_, h) => (
              <span
                key={h}
                className="text-center font-mono text-[9px] tabular-nums text-muted-foreground"
              >
                {h % 3 === 0 ? String(h).padStart(2, "0") : ""}
              </span>
            ))}
          </div>
          {grid.map((row, d) => (
            <div
              key={d}
              className="grid grid-cols-[2.25rem_repeat(24,minmax(0,1fr))] items-center gap-[2px] pb-[2px]"
            >
              <span className="font-mono text-[10px] text-muted-foreground">{DAYS[d]}</span>
              {row.map((v, h) => (
                <span
                  key={h}
                  title={`${DAYS_LONG[d]} ${String(h).padStart(2, "0")}:00 — ${v.toLocaleString()} ${
                    v === 1 ? "event" : "events"
                  }`}
                  className="h-4 rounded-[2px]"
                  style={{ background: intensity(v) }}
                />
              ))}
            </div>
          ))}
        </div>
      </div>

      {peak.count > 0 && (
        <p className="mt-3 text-[13px] text-muted-foreground">
          Worst single hour:{" "}
          <span className="text-foreground">
            {DAYS_LONG[peak.day]} at {String(peak.hour).padStart(2, "0")}:00
          </span>{" "}
          with {peak.count.toLocaleString()} {peak.count === 1 ? "event" : "events"}. Most of
          the window&apos;s events land on {DAYS_LONG[busiestDay]}s and around{" "}
          {String(busiestHour).padStart(2, "0")}:00.
        </p>
      )}
    </section>
  );
}

/**
 * One issue's last 24 hours, as a bar per hour. Sits in the leaderboard so a row says
 * whether the issue is still going, which "91 events" on its own does not.
 */
export function Sparkline({ buckets, color }: { buckets: { at: string; count: number }[] | null; color: string }) {
  if (!buckets || buckets.length === 0) {
    return <span className="text-[11px] text-muted-foreground">—</span>;
  }
  const peak = Math.max(1, ...buckets.map((b) => b.count));
  // A row of empty stubs is indistinguishable from a rendering fault. An issue that has
  // not fired in a day is worth a word, not a flat line.
  if (buckets.every((b) => b.count === 0)) {
    return <span className="text-[11px] text-muted-foreground">quiet</span>;
  }

  return (
    <div className="flex h-6 items-end gap-[1px]" title="Events per hour, last 24 hours">
      {buckets.map((b) => (
        <span
          key={b.at}
          title={`${new Date(b.at).toLocaleTimeString([], { hour: "2-digit", hour12: false })} — ${b.count}`}
          className="w-1 rounded-sm"
          style={{
            // Empty hours keep a stub: an absent bar and a zero bar look identical, and
            // only one of them says the issue went quiet.
            height: b.count === 0 ? "2px" : `${Math.max(20, (b.count / peak) * 100)}%`,
            background: b.count === 0 ? "var(--border)" : color,
          }}
        />
      ))}
    </div>
  );
}

/**
 * Two dimensions at once. Two breakdowns side by side can say "staging is quiet" and
 * "warnings are 12% of everything"; only their intersection can say whether the warnings
 * are all in staging — which is the difference between a release problem and a noisy test
 * environment.
 */
export function Matrix({
  matrix,
  label,
  rowLabel,
  columnLabel,
  colorOf,
}: {
  matrix: AnalyticsMatrix;
  label: string;
  rowLabel: string;
  columnLabel: string;
  /** Columns carry the meaning here, so they carry the colour. */
  colorOf?: (column: string) => string;
}) {
  const max = Math.max(1, ...matrix.cells.flat());
  const colTotals = matrix.columns.map((_, c) =>
    matrix.cells.reduce((n, row) => n + (row[c] ?? 0), 0),
  );

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-semibold tracking-tight">{label}</h3>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {rowLabel} × {columnLabel}
        </span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full border-separate border-spacing-[2px] text-[13px]">
          <thead>
            <tr>
              <th className="w-[22%] min-w-[8rem]" />
              {matrix.columns.map((c) => (
                <th key={c} className="pb-1 text-right font-mono text-[11px] font-normal text-muted-foreground">
                  {c || "(not set)"}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {matrix.rows.map((row, r) => (
              <tr key={row}>
                <td className="truncate pr-2 font-mono text-[13px] text-muted-foreground">
                  {row || "(not set)"}
                </td>
                {matrix.columns.map((col, c) => {
                  const v = matrix.cells[r]?.[c] ?? 0;
                  return (
                    <td
                      key={col}
                      title={`${row || "(not set)"} · ${col || "(not set)"} — ${v.toLocaleString()} ${
                        v === 1 ? "event" : "events"
                      }`}
                      className="rounded-[3px] px-2 py-1 text-right font-mono tabular-nums"
                      style={{
                        // Shaded by weight so the eye finds the hot corner before it reads
                        // a single number; the number is still there for when it matters.
                        background:
                          v === 0
                            ? "var(--muted)"
                            : `color-mix(in oklab, ${colorOf?.(col) ?? "var(--primary)"} ${Math.round(
                                18 + 62 * Math.sqrt(v / max),
                              )}%, var(--muted))`,
                        color: v === 0 ? "var(--muted-foreground)" : "var(--foreground)",
                      }}
                    >
                      {v === 0 ? "·" : v.toLocaleString()}
                    </td>
                  );
                })}
              </tr>
            ))}
            <tr>
              <td className="pr-2 pt-1 text-[11px] text-muted-foreground">all</td>
              {colTotals.map((t, c) => (
                <td
                  key={matrix.columns[c]}
                  className="px-2 pt-1 text-right font-mono text-[11px] tabular-nums text-muted-foreground"
                >
                  {t.toLocaleString()}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  );
}

/**
 * How many issues were seen for the very first time, per day.
 *
 * Volume answers "how loud is it"; this answers "are we still introducing bugs" — a
 * project can be getting louder while introducing nothing new, and the two call for
 * completely different conversations.
 */
export function NewIssuesPerDay({ issues, windowStart }: { issues: Issue[]; windowStart: number }) {
  // One bucket per local day, oldest first. Local rather than UTC because "Tuesday" is
  // the unit somebody thinks in, and the rest of the page is already in local time.
  //
  // The axis runs from the start of the window's first day to the end of *today*, which is
  // one bucket more than the window is days long. Sizing it to the window instead dropped
  // everything first seen today into an index past the end — six of ten new issues, on the
  // dev data, silently missing from a panel whose own total then disagreed with the tile
  // beside it.
  const start = new Date(windowStart);
  start.setHours(0, 0, 0, 0);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const days = Math.floor((today.getTime() - start.getTime()) / 86_400_000) + 1;
  const buckets = Array.from({ length: Math.max(1, days) }, (_, n) => {
    const at = new Date(start);
    at.setDate(start.getDate() + n);
    return { at, count: 0 };
  });
  for (const i of issues) {
    const first = Date.parse(i.first_seen);
    if (first < start.getTime()) continue;
    const n = Math.floor((first - start.getTime()) / 86_400_000);
    if (n >= 0 && n < buckets.length) buckets[n].count += 1;
  }
  const total = buckets.reduce((n, b) => n + b.count, 0);
  const peak = Math.max(1, ...buckets.map((b) => b.count));

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <div className="flex flex-wrap items-baseline gap-3">
          <h3 className="text-sm font-semibold tracking-tight">New issues per day</h3>
          <span className="font-mono text-[13px] tabular-nums text-muted-foreground">
            {total.toLocaleString()} first seen in this window
          </span>
        </div>
        <span className="text-[11px] text-muted-foreground">busiest day: {peak}</span>
      </div>
      <div className="flex h-24 items-end gap-[2px]">
        {buckets.map((b) => (
          <span
            key={b.at.toISOString()}
            title={`${b.at.toLocaleDateString(undefined, { month: "short", day: "numeric" })} — ${
              b.count
            } new ${b.count === 1 ? "issue" : "issues"}`}
            className="min-w-0 flex-1 rounded-t-sm"
            style={{
              height: b.count === 0 ? "2px" : `${Math.max(8, (b.count / peak) * 100)}%`,
              background: b.count === 0 ? "var(--muted)" : "var(--status-idle)",
            }}
          />
        ))}
      </div>
      <div className="mt-1 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{buckets[0]?.at.toLocaleDateString(undefined, { month: "short", day: "numeric" })}</span>
        <span>
          {buckets[buckets.length - 1]?.at.toLocaleDateString(undefined, {
            month: "short",
            day: "numeric",
          })}
        </span>
      </div>
      <p className="mt-3 text-[11px] text-muted-foreground">
        A tall bar is a release that shipped bugs, or a dependency that changed under one.
        Amber rather than red: a new issue is news, not necessarily a failure.
      </p>
    </section>
  );
}

/**
 * What happened to the issues, not to the events: how many arrived, how many somebody
 * dealt with, and how many came back after being called fixed.
 *
 * Regressions are the number worth arguing about — an issue resolved and then seen again
 * means either the fix did not work or the resolve was optimistic, and both are worth
 * knowing before the next release.
 */
export function Lifecycle({ issues, windowStart }: { issues: Issue[]; windowStart: number }) {
  const fresh = issues.filter((i) => Date.parse(i.first_seen) >= windowStart).length;
  const resolved = issues.filter(
    (i) => i.resolved_at && Date.parse(i.resolved_at) >= windowStart,
  ).length;
  // Seen again after the resolve: the engine keeps resolved_at, so a later last_seen is
  // the whole test. No extra state to store and none to get wrong.
  const regressed = issues.filter(
    (i) => i.resolved_at && Date.parse(i.last_seen) > Date.parse(i.resolved_at),
  ).length;
  const archived = issues.filter((i) => i.archived_at).length;

  const cells: { label: string; value: number; tone: string; hint: string }[] = [
    { label: "New", value: fresh, tone: "text-status-idle", hint: "first seen in this window" },
    { label: "Resolved", value: resolved, tone: "text-status-active", hint: "marked fixed in this window" },
    { label: "Regressed", value: regressed, tone: "text-status-down", hint: "seen again after a resolve" },
    { label: "Archived", value: archived, tone: "text-muted-foreground", hint: "muted on purpose" },
  ];

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-semibold tracking-tight">Issue lifecycle</h3>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {issues.length} active in this window
        </span>
      </div>
      <dl className="grid grid-cols-2 gap-4">
        {cells.map((c) => (
          <div key={c.label} title={c.hint}>
            <dt className="text-xs text-muted-foreground">{c.label}</dt>
            <dd className={`font-mono text-xl tabular-nums ${c.tone}`}>{c.value.toLocaleString()}</dd>
            <dd className="text-[11px] text-muted-foreground">{c.hint}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const DAYS_LONG = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];

/**
 * A five-step ladder rather than a continuous ramp, and absolute counts rather than a
 * share of the maximum.
 *
 * Measured on real data: with one busy hour of 33 events and a long tail of ones, a
 * linear (or even sqrt) scale against the maximum put every quiet cell within a few
 * percent of every other, and the grid read as one flat wash. Doubling thresholds keep
 * "one event" and "eight events" visibly different, which is the whole point of the panel.
 */
function intensity(v: number): string {
  if (v <= 0) return "var(--muted)";
  const step = v >= 16 ? 100 : v >= 8 ? 74 : v >= 4 ? 52 : v >= 2 ? 32 : 16;
  return `color-mix(in oklab, var(--status-down) ${step}%, var(--muted))`;
}

function tooltip(r: AnalyticsRow, unit: "events" | "issues"): string {
  const name = r.value || "(not set)";
  if (unit === "issues") {
    return `${name} — ${r.count.toLocaleString()} ${r.count === 1 ? "issue" : "issues"}`;
  }
  return `${name} — ${r.count.toLocaleString()} events across ${r.issues} ${
    r.issues === 1 ? "issue" : "issues"
  }`;
}

function percent(n: number, total: number): string {
  if (total <= 0) return "—";
  const pct = (n / total) * 100;
  return `${pct >= 10 || pct === 0 ? pct.toFixed(0) : pct.toFixed(1)}%`;
}
