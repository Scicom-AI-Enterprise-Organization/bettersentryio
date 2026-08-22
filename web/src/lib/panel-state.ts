/**
 * Which sidebar columns you collapsed, remembered across reloads.
 *
 * A cookie rather than localStorage on purpose: the layout is server-rendered, so the
 * server has to know the answer *before* it sends HTML. Reading it in the browser
 * would paint the expanded sidebar first and snap it shut on hydration — the flash is
 * exactly what a persisted preference is supposed to prevent.
 *
 * Not httpOnly: the panels toggle it in the browser. It holds a layout preference, so
 * there is nothing to protect.
 */

export const NAV_PANEL_COOKIE = "bsio.nav-collapsed";
export const PROJECT_PANEL_COOKIE = "bsio.project-collapsed";

/** A year. A sidebar preference does not expire in any meaningful sense. */
const MAX_AGE = 60 * 60 * 24 * 365;

/** Absent means expanded: the first visit shows the whole sidebar. */
export function panelCollapsed(value: string | undefined): boolean {
  return value === "1";
}

/** Browser-side write. Called from the panels' toggles, never on the server. */
export function rememberPanel(name: string, collapsed: boolean): void {
  document.cookie = `${name}=${collapsed ? "1" : "0"}; path=/; max-age=${MAX_AGE}; samesite=lax`;
}
