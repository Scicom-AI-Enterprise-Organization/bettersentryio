import Link from "next/link";
import { notFound } from "next/navigation";
import { revalidatePath } from "next/cache";
import { ChevronRight } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { pageEnabled } from "@/lib/features";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCard,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ActivityBars } from "@/components/bsio/activity-bars";
import { getMonitor, setMuted } from "@/lib/bsio";
import { ago, clock, incidentTone, monitorTone, shortDuration, stamp, uptimeLabel } from "@/lib/format";
import { Ago, StampAt } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function MonitorDetailPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  await requireUser();
  if (!pageEnabled("monitors")) notFound();
  const { slug } = await params;
  const result = await getMonitor(slug);

  if (!result.ok) {
    if (result.error.includes("404")) notFound();
    return (
      <div className="space-y-6">
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const { monitor: m, config, incidents } = result.data;

  async function toggleMute() {
    "use server";
    await setMuted(slug, !m.muted);
    revalidatePath(`/monitors/${slug}`);
  }

  return (
    <div className="space-y-6">
      <nav className="flex items-center gap-1 text-xs text-muted-foreground">
        <Link href="/monitors" className="hover:text-foreground">
          Monitors
        </Link>
        <ChevronRight className="h-3.5 w-3.5" />
        <span className="font-mono text-foreground">{m.slug}</span>
      </nav>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="font-mono text-2xl font-semibold tracking-tight">{m.slug}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {m.environment} · {m.kind} monitor · created{" "}
            <Ago iso={m.created_at} />
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="text-right">
            <StatusPill tone={monitorTone(m.status)} className="px-3 py-1 text-sm">
              {m.status}
            </StatusPill>
            {m.open_incident_since && (
              <div className="mt-1 text-xs text-muted-foreground">
                since <StampAt iso={m.open_incident_since} />
              </div>
            )}
          </div>
          <form action={toggleMute}>
            <Button type="submit" variant="outline" size="sm">
              {m.muted ? "Unmute" : "Mute alerts"}
            </Button>
          </form>
        </div>
      </div>

      {m.status === "stalled" && (
        <Alert>
          <AlertTitle>Beating, but making no progress</AlertTitle>
          <AlertDescription>
            Heartbeats are arriving on schedule while the progress counter sits at{" "}
            <span className="font-mono">{m.last_progress ?? "—"}</span>. The loop is alive and
            doing nothing, so a liveness check alone would call this healthy.
          </AlertDescription>
        </Alert>
      )}
      {m.status === "missing" && (
        <Alert variant="destructive">
          <AlertTitle>No heartbeat</AlertTitle>
          <AlertDescription>
            Expected one every {shortDuration(m.every_secs)} with{" "}
            {shortDuration(m.grace_secs)} of grace; nothing since <StampAt iso={m.last_beat_at} />. If the
            service still answers its own health check, this is exactly the failure that check
            cannot see.
          </AlertDescription>
        </Alert>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
        <Fact label="Last beat" value={clock(m.last_beat_at)} sub={ago(m.last_beat_at)} />
        <Fact
          label="Next expected"
          value={clock(m.next_expected_at)}
          sub={`every ${shortDuration(m.every_secs)}`}
        />
        <Fact label="Grace" value={shortDuration(m.grace_secs)} sub="before missing" />
        <Fact
          label="Stall window"
          value={m.stall_window_secs > 0 ? shortDuration(m.stall_window_secs) : "off"}
          sub="frozen progress"
        />
        <Fact
          label="Progress"
          value={m.last_progress === null ? "—" : String(m.last_progress)}
          sub="monotonic counter"
        />
        <Fact
          label="Uptime"
          value={uptimeLabel(m.uptime_pct)}
          sub={`over ${shortDuration(m.uptime_observed_secs)} · ${m.beats_24h} beats`}
        />
      </div>

      <section>
        <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
          <h2 className="text-base font-medium">Beat activity</h2>
          <span className="text-xs text-muted-foreground">last 2 hours, 5-minute buckets</span>
        </div>
        <Card>
          <CardContent className="px-6">
            <ActivityBars buckets={m.activity} className="h-28 w-full gap-1" />
          </CardContent>
        </Card>
      </section>

      <section>
        <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
          <h2 className="text-base font-medium">Incidents</h2>
          <span className="text-xs text-muted-foreground">{incidents.length} recorded</span>
        </div>
        {incidents.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No incidents recorded. This monitor has never gone missing or stalled.
          </p>
        ) : (
          <TableCard>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kind</TableHead>
                  <TableHead>Opened</TableHead>
                  <TableHead>Resolved</TableHead>
                  <TableHead className="text-right">Duration</TableHead>
                  <TableHead className="text-right">Alerts delivered</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {incidents.map((i) => (
                  <TableRow key={i.id}>
                    <TableCell>
                      <StatusPill tone={incidentTone(i.kind)}>{i.kind}</StatusPill>
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      <StampAt iso={i.opened_at} />
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      {i.resolved_at ? (
                        stamp(i.resolved_at)
                      ) : (
                        <StatusPill tone="down">ongoing</StatusPill>
                      )}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums">
                      {shortDuration(i.duration_secs)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums">
                      {i.alerts_delivered === 0 ? (
                        <span className="text-muted-foreground">
                          0{!i.resolved_at && " · retrying"}
                        </span>
                      ) : (
                        i.alerts_delivered
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableCard>
        )}
      </section>

      <section>
        <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
          <h2 className="text-base font-medium">Configuration</h2>
          <span className="text-xs text-muted-foreground">
            set by the beat&apos;s query parameters
          </span>
        </div>
        <TableCard>
          <Table>
            <TableBody>
              {Object.entries(config).map(([k, v]) => (
                <TableRow key={k}>
                  <TableCell className="text-muted-foreground">{k}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{String(v)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      </section>
    </div>
  );
}

function Fact({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Card>
      <CardContent className="space-y-0.5 px-4">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="font-mono text-lg font-semibold tabular-nums">{value}</p>
        {sub && <p className="text-[11px] text-muted-foreground">{sub}</p>}
      </CardContent>
    </Card>
  );
}
