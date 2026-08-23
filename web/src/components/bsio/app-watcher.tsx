"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { StatusPill } from "@/components/ui/status-pill";
// Statement-level `import type`, not `import { type … }`: the latter can still emit
// `import {} from "@/lib/bsio"` and keep a runtime edge to a server-only module.
import type { MonitorStatus } from "@/lib/bsio";
import { ago, monitorTone } from "@/lib/format";

type Discovered = {
  slug: string;
  environment: string;
  status: MonitorStatus;
  lastBeatAt: string | null;
  progress: number | null;
  beats24h: number;
};

type Check = {
  reachable: boolean;
  connected?: boolean;
  monitors?: Discovered[];
  error?: string;
};

/**
 * Watches an app until its service starts reporting, then lists what showed up.
 *
 * This is the reconciliation step: you paste the snippet, and the monitors it creates
 * appear here attached to the app, without anything being registered by hand. The
 * page refreshes once on the first arrival so the rest of it stops saying "waiting".
 */
export function AppWatcher({ app, initialConnected }: { app: string; initialConnected: boolean }) {
  const router = useRouter();
  const [check, setCheck] = useState<Check | null>(null);
  const announced = useRef(initialConnected);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const res = await fetch(`/api/bsio/verify?app=${encodeURIComponent(app)}`, {
          cache: "no-store",
        });
        const body = (await res.json()) as Check;
        if (cancelled) return;
        setCheck(body);
        if (body.connected && !announced.current) {
          announced.current = true;
          router.refresh();
        }
      } catch {
        if (!cancelled) setCheck({ reachable: false, error: "Could not reach this UI's API." });
      }
    }

    poll();
    const id = setInterval(poll, 3000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [app, router]);

  const monitors = check?.monitors ?? [];

  if (!check) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        Checking…
      </p>
    );
  }

  if (!check.reachable) {
    return <p className="text-sm text-status-down">{check.error ?? "The engine is unreachable."}</p>;
  }

  if (monitors.length === 0) {
    return (
      <div className="space-y-1">
        <p className="flex items-center gap-2 text-sm">
          <Loader2 className="h-3.5 w-3.5 animate-spin text-status-init" />
          Waiting for the first heartbeat from{" "}
          <span className="font-mono font-medium">{app}</span>
        </p>
        <p className="text-xs text-muted-foreground">
          Nothing has arrived yet. Monitors are created by their first beat, so this list
          fills itself in the moment your service sends one — there is nothing to register
          on this side. It rechecks every 3 seconds.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="flex flex-wrap items-center gap-2 text-sm">
        <StatusPill tone="active">connected</StatusPill>
        <span>
          {monitors.length} {monitors.length === 1 ? "monitor" : "monitors"} reporting under{" "}
          <span className="font-mono font-medium">{app}</span>.
        </span>
      </p>

      <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border">
        {monitors.map((m) => (
          <li
            key={`${m.slug}:${m.environment}`}
            className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5"
          >
            <div>
              <Link
                href={`/monitors/${encodeURIComponent(m.slug)}`}
                className="font-mono text-sm font-medium hover:underline"
              >
                {m.slug}
              </Link>
              <div className="text-xs text-muted-foreground">
                {m.environment} · {m.beats24h} {m.beats24h === 1 ? "beat" : "beats"} · last{" "}
                {ago(m.lastBeatAt)}
                {m.progress !== null && <> · progress {m.progress}</>}
              </div>
            </div>
            <StatusPill tone={monitorTone(m.status)}>{m.status}</StatusPill>
          </li>
        ))}
      </ul>

      <p className="text-xs text-muted-foreground">
        Worth proving once: stop the loop and watch it go MISSING, or freeze the progress
        counter while it keeps beating and watch it go STALLED. That second case is the one
        a <span className="font-mono">/health</span> check cannot see.
      </p>
    </div>
  );
}
