/**
 * The time window every volume chart and the list under it share.
 *
 * A plain module on purpose: a "use client" file exports client *references*, so a
 * server component importing RANGES from one gets a proxy instead of an array — which
 * fails at request time, not at build time.
 */

export const RANGES = [
  { value: "1h", label: "Last hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "90d", label: "Last 90 days" },
  // Two timestamps instead of a span. Selecting it reveals the from/to inputs; the
  // window is then carried by ?start=&end= and the preset is only a mode marker.
  { value: "custom", label: "Custom range…" },
] as const;

export const INTERVALS = [
  { value: "auto", label: "Auto interval" },
  { value: "1m", label: "1 minute" },
  { value: "5m", label: "5 minutes" },
  { value: "30m", label: "30 minutes" },
  { value: "1h", label: "1 hour" },
  { value: "6h", label: "6 hours" },
  { value: "1d", label: "1 day" },
] as const;

/** Thirty days: long enough to say whether a bug is ongoing or was a one-off. */
export const DEFAULT_RANGE = "30d";
export const DEFAULT_INTERVAL = "auto";

/**
 * A window as the URL carries it. `range: "custom"` means start/end are authoritative;
 * any other range is a span ending now and start/end are ignored.
 */
export type TimeWindow = {
  range: string;
  interval?: string;
  /**
   * An absolute instant with its offset attached (`2026-08-20T13:00:00+08:00`), not a
   * local wall-clock string.
   *
   * The picker converts the browser's local input into this before it reaches the URL,
   * and the input converts it back for display. That conversion has to happen in the
   * browser: everything that reads a window — bsio.ts, the pages — is server-side, and
   * a server in UTC resolving "13:00" would move a +08 user's afternoon by eight hours.
   */
  start?: string;
  end?: string;
};

/** Anything the URL offers that is not on the list is ignored, not passed through. */
export function resolveRange(v: string | undefined): string {
  return RANGES.find((r) => r.value === v)?.value ?? DEFAULT_RANGE;
}

export function resolveInterval(v: string | undefined): string {
  return INTERVALS.find((i) => i.value === v)?.value ?? DEFAULT_INTERVAL;
}

/**
 * Reads a window out of a page's searchParams.
 *
 * A custom range with nothing filled in yet falls back to the default span rather than
 * asking the engine for an empty window: the user has selected the mode but not the
 * dates, and an empty chart would look like an outage.
 */
export function resolveWindow(sp: {
  range?: string;
  interval?: string;
  start?: string;
  end?: string;
}): TimeWindow {
  const range = resolveRange(sp.range);
  const interval = resolveInterval(sp.interval);
  if (range !== "custom") return { range, interval };
  const start = localStamp(sp.start);
  const end = localStamp(sp.end);
  if (!start || !end) return { range: "custom", interval, start, end };
  return { range: "custom", interval, start, end };
}

/** Rejects anything that is not a timestamp, rather than passing it on to the engine. */
function localStamp(v: string | undefined): string | undefined {
  if (!v) return undefined;
  const shape = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?(Z|[+-]\d{2}:\d{2})?$/;
  return shape.test(v) && !Number.isNaN(Date.parse(v)) ? v : undefined;
}

/** True when the window is the custom mode *and* usable — both ends present and ordered. */
export function customReady(w: TimeWindow): boolean {
  return (
    w.range === "custom" && !!w.start && !!w.end && Date.parse(w.start) < Date.parse(w.end)
  );
}

/**
 * The query the engine wants: either a span, or two absolute timestamps.
 *
 * The stamps are passed through exactly as the URL holds them, offset included. This
 * runs server-side, so it must not touch the local zone — the browser already resolved
 * it when the picker wrote the URL.
 */
export function windowParams(w: TimeWindow): URLSearchParams {
  const q = new URLSearchParams();
  if (customReady(w)) {
    q.set("start", w.start!);
    q.set("end", w.end!);
  } else {
    q.set("statsPeriod", w.range === "custom" ? DEFAULT_RANGE : w.range);
  }
  // "auto" is the absence of an interval, not a value the engine parses.
  if (w.interval && w.interval !== DEFAULT_INTERVAL) q.set("interval", w.interval);
  return q;
}

/* ---- browser-only conversions ---------------------------------------------
 * Both read the machine's zone, so both belong to the picker. Calling them from a
 * server component would resolve the timestamp in the server's zone, which is the bug
 * the offset in the URL exists to prevent.
 */

/** `<input type="datetime-local">` value → an absolute instant with its offset. */
export function fromLocalInput(local: string): string {
  const d = new Date(local);
  if (Number.isNaN(d.getTime())) return local;
  const mins = -d.getTimezoneOffset();
  const sign = mins >= 0 ? "+" : "-";
  const pad = (n: number) => String(Math.floor(Math.abs(n))).padStart(2, "0");
  const stamp = local.length === 16 ? `${local}:00` : local;
  return `${stamp}${sign}${pad(mins / 60)}:${pad(mins % 60)}`;
}

/** The reverse, for putting a window from the URL back into the inputs. */
export function toLocalInput(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export type SeriesBucket = { at: string; count: number };

/**
 * Level colours come from the status tokens, never the chart ramp: `--chart-*` flips
 * to orange in dark mode, so an "error" series painted with it reads as a warning
 * (see DEVELOPING, design conventions).
 */
export function levelColor(level: string): string {
  switch (level) {
    case "fatal":
    case "error":
      return "var(--status-down)";
    case "warning":
      return "var(--status-idle)";
    case "info":
      return "var(--status-init)";
    default:
      return "var(--muted-foreground)";
  }
}
