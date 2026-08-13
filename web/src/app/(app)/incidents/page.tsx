import Link from "next/link";
import { requireUser } from "@/lib/rbac";
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
import { getIncidents, incidentTone, shortDuration, stamp } from "@/lib/bsio";
import { StampAt } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function IncidentsPage() {
  await requireUser();
  const result = await getIncidents();

  if (!result.ok) {
    return (
      <div className="space-y-6">
        <Title />
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const { incidents } = result.data;
  const open = incidents.filter((i) => !i.resolved_at).length;

  return (
    <div className="space-y-6">
      <Title />

      <section>
        <div className="mb-3 flex items-baseline gap-3 border-b border-border pb-2">
          <h2 className="text-base font-medium">All incidents</h2>
          <span className="text-xs text-muted-foreground">
            {open} open · {incidents.length} total
          </span>
        </div>

        {incidents.length === 0 ? (
          <div className="rounded-xl border border-dashed border-border p-8">
            <p className="text-sm font-medium">No incidents.</p>
            <p className="mt-1 text-sm text-muted-foreground">
              Nothing has gone missing or stalled since this instance started.
            </p>
          </div>
        ) : (
          <TableCard>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Monitor</TableHead>
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
                      <Link
                        href={`/monitors/${encodeURIComponent(i.monitor)}`}
                        className="font-mono text-sm font-medium hover:underline"
                      >
                        {i.monitor}
                      </Link>
                      <div className="text-xs text-muted-foreground">{i.environment}</div>
                    </TableCell>
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
    </div>
  );
}

function Title() {
  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight">Incidents</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Open incidents first, then most recent. &quot;Alerts delivered&quot; counts confirmed
        deliveries — a zero on an open incident means the alert has not reached a channel yet and
        is still being retried.
      </p>
    </div>
  );
}
