"use client";

import { useState } from "react";
import Link from "next/link";
import { Plus, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/status-pill";
import { CopyButton } from "@/components/bsio/copy-button";
import { platformMark, platform as findPlatform } from "@/lib/platforms";

import type { App } from "@/lib/bsio";
import { Ago } from "@/components/bsio/time";

/**
 * The landing screen: pick a project. Cards rather than a table because the useful
 * comparison here is state and volume at a glance, not a column you sort.
 */
export function ProjectGrid({ apps }: { apps: App[] }) {
  const [q, setQ] = useState("");
  const needle = q.trim().toLowerCase();
  const shown = needle
    ? apps.filter(
        (a) => a.name.toLowerCase().includes(needle) || a.slug.includes(needle),
      )
    : apps;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search projects"
            className="w-64 pl-8"
          />
        </div>
        <div className="flex-1" />
        <Button asChild size="sm">
          <Link href="/apps/new">
            <Plus className="h-4 w-4" />
            New project
          </Link>
        </Button>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {shown.map((a) => (
          <div
            key={a.slug}
            className="flex flex-col justify-between gap-4 rounded-xl border border-border bg-card p-4 transition-colors hover:border-muted-foreground/40"
          >
            <div className="flex items-start gap-3">
              <span className="shrink-0">{platformMark(a.platform)}</span>
              <div className="min-w-0 flex-1">
                <Link
                  href={`/apps/${a.slug}`}
                  className="text-sm font-medium hover:underline"
                >
                  {a.name}
                </Link>
                <p className="truncate font-mono text-xs text-muted-foreground">
                  {a.slug}
                  {findPlatform(a.platform) && ` · ${findPlatform(a.platform)!.name}`}
                </p>
              </div>
              {!a.connected ? (
                <StatusPill tone="init">not connected</StatusPill>
              ) : a.open_incident ? (
                <StatusPill tone="down">incident</StatusPill>
              ) : a.unhealthy > 0 ? (
                <StatusPill tone="idle">degraded</StatusPill>
              ) : (
                <StatusPill tone="active">healthy</StatusPill>
              )}
            </div>

            <div className="flex gap-6">
              <Stat n={a.monitors} label="Monitors" />
              <Stat
                n={a.unhealthy}
                label="Need attention"
                tone={a.unhealthy > 0 ? "bad" : undefined}
              />
              <div>
                <div className="font-mono text-sm tabular-nums">
                  <Ago iso={a.last_beat_at} />
                </div>
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  Last beat
                </div>
              </div>
            </div>

            <div className="flex items-center gap-2">
              <Button asChild variant="outline" size="sm">
                <Link href={`/apps/${a.slug}`}>
                  {a.connected ? "Go to project" : "Finish setup"}
                </Link>
              </Button>
              <div className="flex-1" />
              <CopyButton
                value={a.ingest_key}
                label={`${a.ingest_key.slice(0, 10)}…`}
                className="rounded border border-border font-mono"
              />
            </div>
          </div>
        ))}

        <Link
          href="/apps/new"
          className="flex min-h-[160px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border text-sm text-muted-foreground transition-colors hover:border-muted-foreground/50 hover:text-foreground"
        >
          <Plus className="h-5 w-5" />
          New project
        </Link>
      </div>

      {needle && shown.length === 0 && (
        <p className="text-sm text-muted-foreground">No project matches “{q}”.</p>
      )}
    </div>
  );
}

function Stat({ n, label, tone }: { n: number; label: string; tone?: "bad" }) {
  return (
    <div>
      <div
        className={
          tone === "bad"
            ? "font-mono text-sm tabular-nums text-status-down"
            : "font-mono text-sm tabular-nums"
        }
      >
        {n}
      </div>
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
    </div>
  );
}
