import Link from "next/link";

import { platformMark } from "@/lib/platforms";
import { StatusPill } from "@/components/ui/status-pill";
import type { App } from "@/lib/bsio";

/**
 * The page header every project screen shares: which project, and is it healthy.
 *
 * Navigation deliberately is not here — the sidebar is scoped to the project, so a tab
 * strip would be a second copy of the same nav in a different shape.
 */
export function ProjectHeader({
  app,
  title,
  subtitle,
  actions,
  link,
}: {
  app: App;
  title: string;
  subtitle?: string;
  actions?: React.ReactNode;
  /** An inline follow-on, e.g. the part of the guide that explains this view. */
  link?: { href: string; label: string };
}) {
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex min-w-0 items-center gap-3">
          <span className="shrink-0">{platformMark(app.platform)}</span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="text-xl font-semibold tracking-tight">{app.name}</h1>
              {!app.connected ? (
                <StatusPill tone="init">not connected</StatusPill>
              ) : app.open_incident ? (
                <StatusPill tone="down">incident</StatusPill>
              ) : app.unhealthy > 0 ? (
                <StatusPill tone="idle">degraded</StatusPill>
              ) : (
                <StatusPill tone="active">healthy</StatusPill>
              )}
            </div>
            <p className="font-mono text-xs text-muted-foreground">
              {app.slug}
              {app.monitors > 0 &&
                ` · ${app.monitors} ${app.monitors === 1 ? "monitor" : "monitors"}`}
            </p>
          </div>
        </div>
        {actions}
      </div>

      <div className="border-b border-border pb-3">
        <h2 className="text-xl font-medium leading-snug tracking-tight" style={{ textWrap: "balance" }}>
          {title}
        </h2>
        {subtitle && (
          <p className="mt-1 text-[15px] text-muted-foreground">
            {subtitle}
            {link && (
              <>
                {" "}
                <Link href={link.href} className="text-primary hover:underline">
                  {link.label}
                </Link>
              </>
            )}
          </p>
        )}
      </div>
    </div>
  );
}
