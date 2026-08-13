import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { requireUser } from "@/lib/rbac";
import { StatCard } from "@/components/stat-card";
import { StatusPill } from "@/components/ui/status-pill";
import {
  Table,
  TableBody,
  TableCard,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ActivityBars } from "@/components/bsio/activity-bars";
import { getOverview, monitorTone, shortDuration, uptimeLabel } from "@/lib/bsio";
import { Ago, ClockAt, Since } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function MonitorsPage() {
  await requireUser();
  const result = await getOverview();

  if (!result.ok) {
    return (
      <div className="space-y-6">
        <PageTitle />
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>
            {result.error} Detection and alerting run in the Go engine, not in this UI —
            if it is down, nothing is being monitored right now.
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  const { summary, monitors } = result.data;

  return (
    <div className="space-y-6">
      <PageTitle />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Need attention"
          value={summary.unhealthy}
          sub={`${summary.missing} missing · ${summary.stalled} stalled`}
          tone={summary.unhealthy > 0 ? "negative" : "default"}
        />
        <StatCard
          label="Healthy"
          value={summary.ok}
          sub={`${summary.late} late · ${summary.waiting} waiting`}
          tone={summary.ok > 0 ? "positive" : "muted"}
        />
        <StatCard label="Monitors" value={summary.total} sub="created by their first beat" />
        <StatCard
          label="Open incidents"
          value={summary.open_incidents}
          sub="see the incident log"
          tone={summary.open_incidents > 0 ? "warning" : "muted"}
        />
      </div>

      <section>
        <div className="mb-3 flex items-center justify-between border-b border-border pb-2">
          <div className="flex items-baseline gap-3">
            <h2 className="text-base font-medium">Monitors</h2>
            <span className="text-xs text-muted-foreground">
              {monitors.length} {monitors.length === 1 ? "monitor" : "monitors"}
            </span>
          </div>
        </div>

        {monitors.length === 0 ? (
          <div className="rounded-xl border border-dashed border-border p-8">
            <p className="text-sm font-medium">No monitors yet.</p>
            <p className="mt-1 text-sm text-muted-foreground">
              A monitor is created by its first heartbeat — there is nothing to configure first.
              Add an app to get an ingest key and the code to paste in.
            </p>
            <Link
              href="/apps"
              className="mt-4 inline-flex items-center gap-1 text-sm text-primary hover:underline"
            >
              Go to Apps
              <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </div>
        ) : (
          <TableCard>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Monitor</TableHead>
                  <TableHead>App</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Last beat</TableHead>
                  <TableHead className="text-right">Progress</TableHead>
                  <TableHead>Activity · 1h</TableHead>
                  <TableHead className="text-right">Uptime</TableHead>
                  <TableHead className="text-right">Beats · 24h</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {monitors.map((m) => (
                  <TableRow key={`${m.slug}:${m.environment}`}>
                    <TableCell>
                      <Link
                        href={`/monitors/${encodeURIComponent(m.slug)}`}
                        className="font-mono text-sm font-medium hover:underline"
                      >
                        {m.slug}
                      </Link>
                      <div className="text-xs text-muted-foreground">
                        {m.environment} · every {shortDuration(m.every_secs)}
                        {m.muted && " · muted"}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Link
                        href={`/apps/${encodeURIComponent(m.app)}`}
                        className="text-sm text-muted-foreground hover:text-foreground hover:underline"
                      >
                        {m.app_name}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <StatusPill tone={monitorTone(m.status)}>{m.status}</StatusPill>
                      {m.open_incident_since && (
                        <div className="mt-1 text-xs text-muted-foreground">
                          for <Since secs={m.open_incident_secs ?? 0} />
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      <ClockAt iso={m.last_beat_at} />
                      <div className="text-xs text-muted-foreground">
                        <Ago iso={m.last_beat_at} />
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums">
                      {m.last_progress ?? "—"}
                    </TableCell>
                    <TableCell>
                      <ActivityBars buckets={m.activity} className="h-7 w-36" />
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums">
                      {uptimeLabel(m.uptime_pct)}
                      <div className="text-xs text-muted-foreground">
                        of {shortDuration(m.uptime_observed_secs)}
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-muted-foreground">
                      {m.beats_24h}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableCard>
        )}
      </section>
    </div>
  );
}

function PageTitle() {
  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight">Monitors</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Background loops and scheduled jobs, watched from the inside. A monitor goes MISSING
        when heartbeats stop and STALLED when they keep arriving without progress.
      </p>
    </div>
  );
}
