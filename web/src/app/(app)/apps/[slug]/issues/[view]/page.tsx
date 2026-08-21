import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowRight } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { clock, getApp, getIncidents, getIssues, shortDuration } from "@/lib/bsio";
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
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { ErrorIssuesFiltered, MonitorsFiltered } from "./filtered-tables";

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

  const [appResult, incidentResult, issueResult] = await Promise.all([
    getApp(slug),
    getIncidents(),
    // Error issues live on the "Errors & Outages" view; the other views are
    // monitor-state lists and never show them.
    view.id === "outages" ? getIssues(slug, { resolved: true, archived: true }) : Promise.resolve(null),
  ]);

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

      {view.id === "outages" && app.connected && issueResult?.ok && (
        <ErrorIssuesFiltered slug={app.slug} issues={issueResult.data.issues} />
      )}

      {!app.connected ? (
        <div className="rounded-xl border border-border bg-card p-8 shadow-xs">
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
        <MonitorsFiltered monitors={monitors} viewId={view.id} />
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
