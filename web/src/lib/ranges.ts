/**
 * The window and bucket choices every volume chart offers.
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

/** Anything the URL offers that is not on the list is ignored, not passed through. */
export function resolveRange(v: string | undefined): string {
  return RANGES.find((r) => r.value === v)?.value ?? DEFAULT_RANGE;
}

export function resolveInterval(v: string | undefined): string {
  return INTERVALS.find((i) => i.value === v)?.value ?? DEFAULT_INTERVAL;
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
