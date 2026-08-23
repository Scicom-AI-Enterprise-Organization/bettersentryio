/**
 * Page flags: which optional surfaces this install shows.
 *
 * `BSIO_DISABLED_PAGES` is a comma-separated list; everything is ON by default and an
 * unset variable changes nothing. A production install that uses error tracking only
 * sets:
 *
 *   BSIO_DISABLED_PAGES=monitors,breached,warnings,releases
 *
 * leaving Errors & Outages, Analytics, Alerts, Setup and every settings page.
 *
 * Read server-side only (layouts, pages) — the value reaches client components as
 * props, never as NEXT_PUBLIC_*: an env var inlined into the client bundle is frozen
 * at build time, and these flags must be settable per deployment of one image.
 *
 * Hiding is not disabling, so each flagged page also guards its own route with
 * notFound() — the nav merely stops advertising what the route already refuses.
 */

export const OPTIONAL_PAGES = [
  // The global Monitors section: the wall, monitor detail, and the incident log.
  "monitors",
  // Per-project heartbeat views.
  "breached",
  "warnings",
  "releases",
] as const;

export type OptionalPage = (typeof OPTIONAL_PAGES)[number];

/** Parsed once per call; unknown names are ignored rather than fatal. */
export function disabledPages(): OptionalPage[] {
  const raw = process.env.BSIO_DISABLED_PAGES ?? "";
  const known = new Set<string>(OPTIONAL_PAGES);
  return raw
    .split(",")
    .map((p) => p.trim().toLowerCase())
    .filter((p): p is OptionalPage => known.has(p));
}

export function pageEnabled(page: OptionalPage): boolean {
  return !disabledPages().includes(page);
}

/**
 * Which optional page a project-nav href belongs to, if any. Used by the client-side
 * panels, which receive the disabled list as a prop and must decide per item.
 */
export function pageOfHref(href: string): OptionalPage | undefined {
  if (href.endsWith("/issues/breached")) return "breached";
  if (href.endsWith("/issues/warnings")) return "warnings";
  if (href.endsWith("/releases")) return "releases";
  return undefined;
}
