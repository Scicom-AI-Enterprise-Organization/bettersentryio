"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";
import { HOME, SECTIONS, sectionFor } from "@/lib/nav";
import { RailMark } from "@/components/bsio/rail-mark";
import { ThemeToggle } from "@/components/theme-toggle";
import { UserMenu } from "./user-menu";

/**
 * Layer 1: the section rail.
 *
 * Icon + label per section, because an icon-only rail makes people hover to find
 * anything. Its only job is choosing which panel is open, so it holds no state of its
 * own — the current path decides what is active.
 */
export function ConsoleRail() {
  const pathname = usePathname();
  const active = sectionFor(pathname).id;

  return (
    <nav className="hidden w-[68px] shrink-0 flex-col items-center border-r border-border bg-sidebar py-2 md:flex">
      <Link
        href={HOME}
        aria-label="bettersentryio home"
        className="mb-2 flex h-11 w-11 items-center justify-center rounded-lg hover:bg-accent"
      >
        <RailMark className="h-7 w-7" />
      </Link>

      <ul className="flex flex-1 flex-col items-center gap-1">
        {SECTIONS.map((s) => {
          const Icon = s.icon;
          const on = s.id === active;
          return (
            <li key={s.id}>
              <Link
                href={s.href}
                aria-current={on ? "page" : undefined}
                className={cn(
                  "flex w-[60px] flex-col items-center gap-1 rounded-lg px-1 py-2 text-[10px] font-medium transition-colors",
                  on
                    ? "bg-accent text-accent-foreground"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
              >
                <Icon className="h-[18px] w-[18px]" />
                <span className="text-center leading-tight">{s.label}</span>
              </Link>
            </li>
          );
        })}
      </ul>

      <div className="flex flex-col items-center gap-1 pt-2">
        <ThemeToggle />
        <UserMenu />
      </div>
    </nav>
  );
}
