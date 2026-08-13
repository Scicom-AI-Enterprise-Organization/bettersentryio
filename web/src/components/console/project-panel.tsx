"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronsLeft } from "lucide-react";

import { cn } from "@/lib/utils";
import { projectInPath, projectNav, viewIcon } from "@/lib/nav";
import { IconLink, IconRail, type PanelProject } from "./nav-panel";

/**
 * Layer 3: the selected project's views.
 *
 * A column of its own rather than a replacement for the project list, so you can read
 * one project's issues and switch to another in a single click. It only exists while a
 * project is selected — an empty third column would be furniture.
 */
export function ProjectPanel({ projects = [] }: { projects?: PanelProject[] }) {
  const pathname = usePathname();
  const slug = projectInPath(pathname);
  const [collapsed, setCollapsed] = useState(false);

  if (!slug) return null;

  const project = projects.find((p) => p.slug === slug);
  const isActive = (href: string) => pathname === href || pathname.startsWith(`${href}/`);

  if (collapsed) {
    return (
      <IconRail
        label={`Expand ${project?.name ?? slug}`}
        onExpand={() => setCollapsed(false)}
      >
        {projectNav(slug).map((item) => {
          const Icon = viewIcon(item.href);
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
        })}
      </IconRail>
    );
  }

  return (
    <div className="hidden w-52 shrink-0 flex-col border-r border-border bg-sidebar md:flex">
      <div className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border px-4">
        <div className="min-w-0">
          <p className="truncate text-[10px] font-medium uppercase tracking-[0.12em] text-muted-foreground">
            Project
          </p>
          <span className="block truncate text-sm font-semibold tracking-tight">
            {project?.name ?? slug}
          </span>
        </div>
        <button
          type="button"
          onClick={() => setCollapsed(true)}
          aria-label="Collapse project"
          className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ChevronsLeft className="h-4 w-4" />
        </button>
      </div>

      <nav className="scrollbar-thin flex-1 overflow-y-auto p-2">
        <ul className="space-y-0.5">
          {projectNav(slug).map((item) => {
            const Icon = viewIcon(item.href);
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
      </nav>
    </div>
  );
}
