import { notFound } from "next/navigation";

import { requireUser } from "@/lib/rbac";
import { getApp, getReleases } from "@/lib/bsio";
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
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { Ago } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return { title: `Releases · ${slug}` };
}

/**
 * Release health from SDK sessions. The Python SDK sends these by default
 * (auto_session_tracking), so this view fills itself in with zero setup the
 * moment a service reports through its DSN. Crash-free is Sentry's number:
 * sessions that did not end in a crash.
 */
export default async function ReleasesPage({ params }: { params: Promise<{ slug: string }> }) {
  await requireUser();
  const { slug } = await params;

  const [appResult, relResult] = await Promise.all([getApp(slug), getReleases(slug)]);
  if (!appResult.ok) {
    if (appResult.error.includes("404")) notFound();
    return (
      <Alert variant="destructive">
        <AlertTitle>The engine is unreachable</AlertTitle>
        <AlertDescription>{appResult.error}</AlertDescription>
      </Alert>
    );
  }
  const { app } = appResult.data;
  const releases = relResult.ok ? relResult.data.releases : [];

  return (
    <div className="space-y-6">
      <ProjectHeader
        app={app}
        title="Releases"
        subtitle="Session health per release and environment, from the SDK's own session tracking — crash-free means the session did not end in a crash."
      />

      {!relResult.ok ? (
        <Alert variant="destructive">
          <AlertTitle>Could not load release health</AlertTitle>
          <AlertDescription>{relResult.error}</AlertDescription>
        </Alert>
      ) : releases.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border p-8">
          <h2 className="text-lg font-semibold tracking-tight">No sessions yet</h2>
          <p className="mt-1 max-w-2xl text-[15px] text-muted-foreground">
            Sessions arrive automatically from any service reporting through its DSN — the
            Python SDK tracks them by default. Set <code className="font-mono">release=</code> in{" "}
            <code className="font-mono">sentry_sdk.init()</code> to see health split by version;
            without it everything lands under <span className="font-mono">(no release)</span>.
          </p>
        </div>
      ) : (
        <TableCard>
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[26%]">Release</TableHead>
                <TableHead className="w-[14%]">Environment</TableHead>
                <TableHead className="w-[14%] text-right">Crash-free</TableHead>
                <TableHead className="w-[12%] text-right">Sessions</TableHead>
                <TableHead className="w-[10%] text-right">Crashed</TableHead>
                <TableHead className="w-[10%] text-right">Errored</TableHead>
                <TableHead className="w-[14%] text-right">Last seen</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {releases.map((r) => (
                <TableRow key={`${r.release}|${r.environment}`}>
                  <TableCell className="truncate font-mono text-[13px]">
                    {r.release || "(no release)"}
                  </TableCell>
                  <TableCell className="truncate font-mono text-[13px] text-muted-foreground">
                    {r.environment}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums">
                    <span
                      className={
                        r.crash_free >= 99.5
                          ? "text-status-active"
                          : r.crash_free >= 95
                            ? "text-status-idle"
                            : "text-status-down"
                      }
                    >
                      {r.crash_free.toFixed(2)}%
                    </span>
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums">
                    {r.sessions}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums">
                    {r.crashed}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] tabular-nums text-muted-foreground">
                    {r.errored}
                  </TableCell>
                  <TableCell className="text-right font-mono text-[13px] text-muted-foreground">
                    <Ago iso={r.last_seen} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}

      <p className="text-[13px] text-muted-foreground">
        Last 30 days, bucketed hourly. Errored sessions saw at least one handled error; crashed
        sessions ended in an unhandled exception.
      </p>
    </div>
  );
}
