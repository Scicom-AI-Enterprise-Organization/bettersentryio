"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu } from "lucide-react";
import { useSidebarState } from "./sidebar-state";

// Human-readable labels for the known app routes. The topbar shows the
// section name as a breadcrumb; anything not listed falls back to a
// title-cased version of the last path segment.
const LABELS: Record<string, string> = {
  "/dashboard": "Dashboard",
  "/apps": "Projects",
  "/learn": "How it works",
  "/apps/new": "Create a project",
  "/monitors": "All monitors",
  "/incidents": "Incident log",
  "/ai": "AI Workspace",
  "/profile": "Profile",
  "/admin/users": "Users",
  "/admin/roles": "Roles",
  "/admin/alerts": "Alerts",
  "/admin/organization": "Organization",
};

function titleCase(segment: string) {
  return segment
    .replace(/[-_]/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

type Crumb = { label: string; href?: string };

const ISSUE_LABELS: Record<string, string> = {
  outages: "Errors & Outages",
  breached: "Breached Metrics",
  warnings: "Warnings",
};

/**
 * Mostly a single label — the rail and panel already say where you are, and the project
 * filter says what is in scope. Setup is the one genuinely nested place, so it gets a
 * real trail naming the project it belongs to.
 */
function crumbsFor(pathname: string): Crumb[] {
  const seg = pathname.split("/").filter(Boolean);

  if (seg[0] === "apps" && seg[1] && seg[1] !== "new") {
    const trail: Crumb[] = [
      { label: "Projects", href: "/apps" },
      { label: seg[1], href: `/apps/${seg[1]}` },
    ];
    if (seg[2] === "settings") trail.push({ label: "Settings" });
    else if (seg[2] === "issues" && seg[3]) {
      trail.push({ label: ISSUE_LABELS[seg[3]] ?? titleCase(seg[3]) });
    }
    return trail;
  }

  if (LABELS[pathname]) return [{ label: LABELS[pathname] }];
  const match = Object.keys(LABELS)
    .filter((href) => pathname.startsWith(`${href}/`))
    .sort((a, b) => b.length - a.length)[0];
  if (match) return [{ label: LABELS[match] }];
  const last = seg.pop();
  return [{ label: last ? titleCase(last) : "Home" }];
}

export function ConsoleTopbar() {
  const { togglePanel } = useSidebarState();
  const pathname = usePathname();
  const crumbs: Crumb[] = crumbsFor(pathname);
  const crumb = crumbs[crumbs.length - 1].label;

  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border bg-sidebar px-3 lg:px-4">
      <div className="flex min-w-0 items-center gap-2">
        {/* Mobile-only menu button. Desktop sidebar is permanent. */}
        <button
          type="button"
          onClick={togglePanel}
          className="inline-flex shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground md:hidden"
          aria-label="Toggle sidebar"
        >
          <Menu className="h-4 w-4" />
        </button>
        <nav className="ml-2 hidden min-w-0 items-center gap-1.5 text-sm md:flex">
          {crumbs.map((c, i) => (
            <span key={i} className="flex min-w-0 items-center gap-1.5">
              {i > 0 && <span className="text-muted-foreground/60">/</span>}
              {c.href ? (
                <Link
                  href={c.href}
                  className="truncate text-muted-foreground hover:text-foreground"
                >
                  {c.label}
                </Link>
              ) : (
                <span className="truncate font-medium text-foreground">{c.label}</span>
              )}
            </span>
          ))}
        </nav>
        <span className="ml-1 truncate text-sm text-foreground md:hidden">
          {crumb}
        </span>
      </div>
    </header>
  );
}
