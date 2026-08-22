"use client";

import { useCallback, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronsLeft, ChevronsRight, Plus } from "lucide-react";

import { cn } from "@/lib/utils";
import { ITEM_ICONS, projectInPath, sectionFor, type NavGroup } from "@/lib/nav";
import { platformMark } from "@/lib/platforms";
import { NAV_PANEL_COOKIE, rememberPanel } from "@/lib/panel-state";

export type PanelProject = {
  slug: string;
  name: string;
  platform: string;
  unhealthy: number;
  connected: boolean;
};

/**
 * Layer 2: what the current rail section contains.
 *
 * For Projects that is the project list, and it stays put when you pick one — the
 * project's own views open in a third column instead of replacing this. Switching
 * project is then one click from anywhere inside a project, with no back step.
 */
export function ConsoleNavPanel({
  isAdmin = false,
  projects = [],
  initialCollapsed = false,
}: {
  isAdmin?: boolean;
  projects?: PanelProject[];
  /** From the cookie, resolved on the server — see @/lib/panel-state. */
  initialCollapsed?: boolean;
}) {
  const pathname = usePathname();
  const section = sectionFor(pathname);
  const activeSlug = projectInPath(pathname);
  const [collapsed, setCollapsed] = useState(initialCollapsed);
  const collapse = useCallback((next: boolean) => {
    setCollapsed(next);
    rememberPanel(NAV_PANEL_COOKIE, next);
  }, []);

  const isActive = (href: string) => pathname === href || pathname.startsWith(`${href}/`);

  if (collapsed) {
    return (
      <IconRail label={`Expand ${section.label}`} onExpand={() => collapse(false)}>
        {section.id === "projects"
          ? projects.map((p) => (
              <IconLink
                key={p.slug}
                href={`/apps/${p.slug}`}
                title={`${p.name}${p.unhealthy > 0 ? ` — ${p.unhealthy} need attention` : ""}`}
                active={p.slug === activeSlug}
              >
                <span
                  className={cn(
                    "flex h-7 w-7 items-center justify-center rounded-md ring-1 [&_svg]:h-5 [&_svg]:w-5",
                    !p.connected
                      ? "ring-border"
                      : p.unhealthy > 0
                        ? "ring-status-down"
                        : "ring-status-active/60",
                  )}
                >
                  {platformMark(p.platform)}
                </span>
              </IconLink>
            ))
          : section.groups.flatMap((g) =>
              g.items
                .filter((i) => !i.admin || isAdmin)
                .map((item) => {
                  const Icon = ITEM_ICONS[item.href];
                  return (
                    <IconLink
                      key={item.href}
                      href={item.href}
                      title={item.label}
                      active={isActive(item.href)}
                    >
                      {Icon ? <Icon className="h-4 w-4" /> : null}
                    </IconLink>
                  );
                }),
            )}
        {section.id === "projects" && (
          <IconLink href="/apps/new" title="New project">
            <Plus className="h-4 w-4" />
          </IconLink>
        )}
      </IconRail>
    );
  }

  return (
    <div className="hidden w-56 shrink-0 flex-col border-r border-border bg-sidebar md:flex">
      <div className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border px-4">
        <div className="min-w-0">
          <p className="truncate text-[10px] font-medium tracking-[0.12em] text-muted-foreground">
            BETTERSENTRYIO
          </p>
          <span className="block truncate text-sm font-semibold tracking-tight">
            {section.label}
          </span>
        </div>
        <button
          type="button"
          onClick={() => collapse(true)}
          aria-label={`Collapse ${section.label}`}
          className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ChevronsLeft className="h-4 w-4" />
        </button>
      </div>

      <nav className="scrollbar-thin flex-1 overflow-y-auto p-2">
        {section.id === "projects" ? (
          <ProjectList projects={projects} activeSlug={activeSlug} />
        ) : (
          section.groups.map((group, gi) => (
            <Group key={gi} group={group} index={gi} isAdmin={isAdmin} isActive={isActive} />
          ))
        )}
      </nav>

      <div className="shrink-0 border-t border-border px-4 py-2">
        <p className="font-mono text-[10px] text-muted-foreground">
          v{process.env.NEXT_PUBLIC_APP_VERSION || "dev"}
        </p>
      </div>
    </div>
  );
}

/**
 * A collapsed column. It keeps the icons rather than becoming an empty strip, so a
 * collapsed column is still navigable — the point of collapsing is to reclaim width,
 * not to hide where things are.
 */
export function IconRail({
  label,
  onExpand,
  children,
}: {
  label: string;
  onExpand: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="hidden w-12 shrink-0 flex-col items-center border-r border-border bg-sidebar md:flex">
      <div className="flex h-14 shrink-0 items-center">
        <button
          type="button"
          onClick={onExpand}
          aria-label={label}
          title={label}
          className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ChevronsRight className="h-4 w-4" />
        </button>
      </div>
      <div className="scrollbar-thin flex flex-1 flex-col items-center gap-1 overflow-y-auto border-t border-border py-2">
        {children}
      </div>
    </div>
  );
}

export function IconLink({
  href,
  title,
  active,
  children,
}: {
  href: string;
  title: string;
  active?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Link
      href={href}
      title={title}
      aria-label={title}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex h-8 w-8 items-center justify-center rounded-md transition-colors",
        active
          ? "bg-accent text-accent-foreground"
          : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
      )}
    >
      {children}
    </Link>
  );
}

/**
 * The dot carries the state so a broken project is visible without opening it: red for
 * something wrong, grey for never-reported, green for fine.
 */
function ProjectList({
  projects,
  activeSlug,
}: {
  projects: PanelProject[];
  activeSlug: string | null;
}) {
  return (
    <>
      {projects.length === 0 ? (
        <p className="px-2 py-3 text-sm text-muted-foreground">No projects yet.</p>
      ) : (
        <ul className="space-y-0.5">
          {projects.map((p) => {
            const on = p.slug === activeSlug;
            return (
              <li key={p.slug}>
                <Link
                  href={`/apps/${p.slug}`}
                  aria-current={on ? "page" : undefined}
                  className={cn(
                    "flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm transition-colors",
                    on
                      ? "bg-accent font-medium text-accent-foreground"
                      : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                  )}
                >
                  <span
                    className={cn(
                      "h-2 w-2 shrink-0 rounded-full",
                      !p.connected
                        ? "bg-muted-foreground/50"
                        : p.unhealthy > 0
                          ? "bg-status-down"
                          : "bg-status-active",
                    )}
                  />
                  <span className="truncate">{p.name}</span>
                  {p.unhealthy > 0 && (
                    <span className="ml-auto rounded bg-status-down/15 px-1.5 font-mono text-[11px] text-status-down">
                      {p.unhealthy}
                    </span>
                  )}
                </Link>
              </li>
            );
          })}
        </ul>
      )}

      <Link
        href="/apps/new"
        className="mt-2 flex items-center gap-2 rounded-md border border-dashed border-border px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:border-muted-foreground/50 hover:text-foreground"
      >
        <Plus className="h-3.5 w-3.5" />
        New project
      </Link>
    </>
  );
}

function Group({
  group,
  index,
  isAdmin,
  isActive,
}: {
  group: NavGroup;
  index: number;
  isAdmin: boolean;
  isActive: (href: string) => boolean;
}) {
  const items = group.items.filter((i) => !i.admin || isAdmin);
  if (items.length === 0) return null;
  return (
    <div className={cn(index > 0 && "mt-4 border-t border-border pt-3")}>
      {group.label && (
        <p className="px-2 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          {group.label}
        </p>
      )}
      <ul className="space-y-0.5">
        {items.map((item) => {
          const Icon = ITEM_ICONS[item.href];
          const on = isActive(item.href);
          return (
            <li key={item.href}>
              <Link
                href={item.href}
                aria-current={on ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors",
                  on
                    ? "bg-accent font-medium text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
              >
                {Icon && <Icon className="h-4 w-4 shrink-0" />}
                <span className="truncate">{item.label}</span>
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
