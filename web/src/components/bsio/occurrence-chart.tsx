"use client";

/**
 * Event volume over a window you choose — one stacked bar per bucket.
 *
 * The window is the point. "Seen 400 times" says nothing about whether it is still
 * happening, and a fixed chart cannot tell a crash loop from a weekly cron failure —
 * so range and bucket width are both controls, and they live in the URL so "this app
 * over the last 90 days" is a link somebody else can open, and so the list rendered
 * under the chart can be filtered by the same window.
 *
 * Bars, not a line: these are counts inside a bucket, and a line between two buckets
 * draws values that were never measured.
 */

import { useCallback, useTransition } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@/components/ui/select";
import { INTERVALS, RANGES } from "@/lib/ranges";

export type ChartSeries = { key: string; label: string; color: string };
export type ChartRow = { at: string } & Record<string, number | string>;

export function OccurrenceChart({
  title,
  rows,
  series,
  total,
  intervalSeconds,
  range,
  interval,
  error,
  height = "h-56",
  controls = true,
}: {
  title: string;
  rows: ChartRow[];
  series: ChartSeries[];
  /**
   * Omit it and the header prints no count. A series total sums a zero-filled axis
   * whose first bucket is floored to an interval boundary, so it can exceed an exact
   * [from, to) aggregate by one partial bucket — a page that already shows the exact
   * figure must not print a second, different one beside it.
   */
  total?: number;
  intervalSeconds: number;
  range: string;
  interval: string;
  error?: string;
  height?: string;
  /**
   * The range/interval selects in the header. Pass false when the page mounts
   * WindowControls somewhere else — two pickers for one window is two sources of truth.
   */
  controls?: boolean;
}) {
  const router = useRouter();
  const params = useSearchParams();
  const [pending, start] = useTransition();

  // Controls write to the URL, so the server component refetches and the state
  // survives a reload or a paste into somebody else's chat.
  const setParam = useCallback(
    (key: string, value: string) => {
      const next = new URLSearchParams(params.toString());
      next.set(key, value);
      start(() => router.replace(`?${next.toString()}`, { scroll: false }));
    },
    [params, router],
  );

  const data = rows.map((row) => ({ ...row, label: bucketLabel(row.at, intervalSeconds) }));
  const stacked = series.length > 1;

  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-baseline gap-3">
          <h2 className="text-base font-semibold tracking-tight">{title}</h2>
          <span className="font-mono text-[13px] tabular-nums text-muted-foreground">
            {total === undefined
              ? `${bucketWidth(intervalSeconds)} buckets`
              : `${total.toLocaleString()} in this window · ${bucketWidth(intervalSeconds)} buckets`}
          </span>
          {stacked && (
            <span className="flex flex-wrap items-center gap-3">
              {series.map((s) => (
                <span key={s.key} className="flex items-center gap-1.5 text-[13px] text-muted-foreground">
                  <span
                    className="inline-block h-2 w-2 rounded-full"
                    style={{ background: s.color }}
                    aria-hidden
                  />
                  {s.label}
                </span>
              ))}
            </span>
          )}
        </div>
        {controls && (
          <div className="flex items-center gap-2" data-pending={pending ? "" : undefined}>
          <Select value={range} onValueChange={(v) => setParam("range", v)}>
            {/* The label is rendered here rather than by SelectValue, which reads its
                text from the portalled items and so ships empty from the server — the
                dropdowns flashed blank until hydration on every page using this. */}
            <SelectTrigger className="h-8 w-[150px] text-[13px]" aria-label="Time range">
              <span className="truncate">{RANGES.find((r) => r.value === range)?.label}</span>
            </SelectTrigger>
            <SelectContent position="popper">
              {RANGES.map((r) => (
                <SelectItem key={r.value} value={r.value}>
                  {r.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={interval} onValueChange={(v) => setParam("interval", v)}>
            <SelectTrigger className="h-8 w-[140px] text-[13px]" aria-label="Bucket interval">
              <span className="truncate">{INTERVALS.find((i) => i.value === interval)?.label}</span>
            </SelectTrigger>
            <SelectContent position="popper">
              {INTERVALS.map((i) => (
                <SelectItem key={i.value} value={i.value}>
                  {i.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          </div>
        )}
      </div>

      {error ? (
        <p className="text-[13px] text-muted-foreground">{error}</p>
      ) : (
        <div className={`${height} w-full`}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
              <XAxis
                dataKey="label"
                tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                stroke="var(--border)"
                minTickGap={28}
                tickLine={false}
              />
              <YAxis
                allowDecimals={false}
                width={36}
                tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                stroke="var(--border)"
                tickLine={false}
              />
              <Tooltip
                contentStyle={{
                  background: "var(--card)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  fontSize: 12,
                  color: "var(--card-foreground)",
                }}
                labelStyle={{ color: "var(--muted-foreground)" }}
                cursor={{ fill: "var(--foreground)", opacity: 0.06 }}
                formatter={(v, name) => [Number(v ?? 0).toLocaleString(), String(name)]}
                labelFormatter={(_, payload) => {
                  const point = payload?.[0]?.payload as ChartRow | undefined;
                  return point ? fullLabel(String(point.at)) : "";
                }}
              />
              {series.map((s, n) => (
                <Bar
                  key={s.key}
                  dataKey={s.key}
                  name={s.label}
                  stackId={stacked ? "all" : undefined}
                  fill={s.color}
                  // Only the top segment of a stack gets the rounded cap.
                  radius={n === series.length - 1 ? [2, 2, 0, 0] : undefined}
                  isAnimationActive={false}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
    </section>
  );
}

/** Axis ticks: a date is noise on a one-hour chart, a clock is noise on 90 days. */
function bucketLabel(iso: string, intervalSeconds: number): string {
  const d = new Date(iso);
  if (intervalSeconds >= 86_400) {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  if (intervalSeconds >= 3_600) {
    return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit" });
  }
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** The hover label is always unambiguous, whatever the axis had room for. */
function fullLabel(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function bucketWidth(seconds: number): string {
  if (seconds % 86_400 === 0) return `${seconds / 86_400}d`;
  if (seconds % 3_600 === 0) return `${seconds / 3_600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}
