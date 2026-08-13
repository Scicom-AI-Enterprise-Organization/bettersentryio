import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowRight } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { clock, getApp, getIncidents, monitorTone, shortDuration } from "@/lib/bsio";
import { incidentsFor, issueView, monitorsFor } from "@/lib/issues";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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
import { ActivityBars } from "@/components/bsio/activity-bars";
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { Ago, ClockAt, Since } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string; view: string }>;
}) {
  const { slug, view } = await params;
  return { title: `${issueView(view)?.label ?? "Issues"} · ${slug}` };
}

export default async function ProjectIssuesPage({
  params,
}: {
  params: Promise<{ slug: string; view: string }>;
}) {
  await requireUser();
  const { slug, view: viewId } = await params;
  const view = issueView(viewId);
  if (!view) notFound();

  const [appResult, incidentResult] = await Promise.all([getApp(slug), getIncidents()]);

  if (!appResult.ok) {
    if (appResult.error.includes("404")) notFound();
    return (
      <Alert variant="destructive">
        <AlertTitle>The engine is unreachable</AlertTitle>
        <AlertDescription>
          {appResult.error} Detection runs in the Go engine, not in this UI — if it is down,
          nothing is being watched right now.
        </AlertDescription>
      </Alert>
    );
  }

  const { app, monitors: all } = appResult.data;
  const monitors = monitorsFor(view, all);
  const mine = new Set(all.map((m) => m.slug));
  const incidents = incidentsFor(
    view,
    (incidentResult.ok ? incidentResult.data.incidents : []).filter((i) => mine.has(i.monitor)),
  );
  const resolved = incidents.filter((i) => i.resolved_at);

  return (
    <div className="space-y-6">
      <ProjectHeader
        app={app}
        title={view.label}
        subtitle={view.description}
        link={{ href: `/learn#${view.id}`, label: "How to instrument this" }}
      />

      {!app.connected ? (
        <div className="rounded-xl border border-border p-8">
          <h2 className="text-lg font-semibold tracking-tight">{app.name} has never reported</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            The project exists and has a key, but no heartbeat has arrived yet — so nothing is
            being watched, and this list would be empty either way. Finish the setup and it fills
            itself in.
          </p>
          <Button asChild size="sm" className="mt-4">
            <Link href={`/apps/${app.slug}/setup`}>
              Finish setup
              <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </Button>
        </div>
      ) : monitors.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border p-8">
          <div className="flex flex-wrap items-center gap-2">
            <StatusPill tone="active">all clear</StatusPill>
            <p className="text-sm font-medium">{view.empty}</p>
          </div>
          <p className="mt-2 text-sm text-muted-foreground">
            Watching {all.length} {all.length === 1 ? "monitor" : "monitors"} in {app.name}.
          </p>
        </div>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Monitor</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Last beat</TableHead>
                <TableHead className="text-right">Progress</TableHead>
                <TableHead>Activity · 1h</TableHead>
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
                      {m.environment} · expects a beat every {shortDuration(m.every_secs)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <StatusPill tone={monitorTone(m.status)}>{m.status}</StatusPill>
                    {m.open_incident_secs !== null && (
                      <div className="mt-1 text-xs text-muted-foreground">
                        for <Since secs={m.open_incident_secs} />
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
                    {view.id === "breached" && (
                      <div className="text-xs text-status-idle">frozen</div>
                    )}
                  </TableCell>
                  <TableCell>
                    <ActivityBars buckets={m.activity} className="h-7 w-36" />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}

      {resolved.length > 0 && (
        <section>
          <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
            <h2 className="text-base font-medium">Resolved</h2>
            <span className="text-xs text-muted-foreground">{resolved.length} of this kind</span>
          </div>
          <TableCard>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Monitor</TableHead>
                  <TableHead>Opened</TableHead>
                  <TableHead>Resolved</TableHead>
                  <TableHead className="text-right">Lasted</TableHead>
                  <TableHead className="text-right">Alerts sent</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {resolved.slice(0, 20).map((i) => (
                  <TableRow key={i.id}>
                    <TableCell className="font-mono text-sm">{i.monitor}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                      {clock(i.opened_at)}
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                      {clock(i.resolved_at)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums">
                      {shortDuration(i.duration_secs)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-muted-foreground">
                      {i.alerts_delivered}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableCard>
        </section>
      )}
    </div>
  );
}
