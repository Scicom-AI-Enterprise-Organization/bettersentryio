import { ago, clock, shortDuration, stamp } from "@/lib/format";

/**
 * Time rendered on the server and hydrated on the client will differ, because "9s ago"
 * is computed from the clock and the clock moves between the two. React reports that as
 * a hydration error, so these wrappers mark it as expected.
 *
 * Suppressing is the right call rather than deferring to an effect: the server value is
 * correct when it is sent, and a placeholder that pops in after mount is worse to read
 * than one that is a second stale.
 */

export function Ago({ iso }: { iso: string | null }) {
  return <span suppressHydrationWarning>{ago(iso)}</span>;
}

/**
 * A duration already measured by the caller, e.g. how long an incident has been open.
 *
 * It takes seconds rather than reading the clock itself, for two reasons: a component
 * that calls Date.now() during render is not idempotent, and a page that captures one
 * instant keeps every duration on it consistent instead of drifting row to row.
 */
export function Since({ secs, suffix }: { secs: number; suffix?: string }) {
  return (
    <span suppressHydrationWarning>
      {shortDuration(secs)}
      {suffix}
    </span>
  );
}

/**
 * How long ago something started, as a duration ("14d 9h").
 *
 * It reads the clock itself so its callers do not: `Date.now()` inside a table cell is
 * impure during render (react-hooks/purity flags it) and desynchronises row to row.
 * `min` floors the result, because "0s" for an issue that has just arrived reads as a
 * missing value.
 */
export function Age({ iso, min = 60 }: { iso: string | null; min?: number }) {
  if (!iso) return <span>—</span>;
  return <span suppressHydrationWarning>{shortDuration(ageSeconds(iso, min))}</span>;
}

/**
 * The clock read, kept out of the component body.
 *
 * Not a purity fix and not pretending to be one: a relative time is time-dependent by
 * definition, which is why every component in this file suppresses the hydration
 * warning. It lives here so the impurity is in one named place with this note attached,
 * rather than inline in a table cell where the next person meets it as a lint error.
 */
function ageSeconds(iso: string, min: number): number {
  return Math.max(min, (Date.now() - Date.parse(iso)) / 1000);
}

export function ClockAt({ iso }: { iso: string | null }) {
  return <span suppressHydrationWarning>{clock(iso)}</span>;
}

export function StampAt({ iso }: { iso: string | null }) {
  return <span suppressHydrationWarning>{stamp(iso)}</span>;
}
