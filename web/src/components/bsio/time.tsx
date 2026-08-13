import { ago, clock, shortDuration, stamp } from "@/lib/bsio";

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

export function ClockAt({ iso }: { iso: string | null }) {
  return <span suppressHydrationWarning>{clock(iso)}</span>;
}

export function StampAt({ iso }: { iso: string | null }) {
  return <span suppressHydrationWarning>{stamp(iso)}</span>;
}
