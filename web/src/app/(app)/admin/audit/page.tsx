import { requireUser } from "@/lib/rbac";
import { getAuditLog } from "@/lib/bsio";
import { DEFAULT_RANGE, RANGES, customReady, resolveWindow } from "@/lib/ranges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { StampAt } from "@/components/bsio/time";
import { AuditFilters } from "./filters";
import { Pager } from "./pager";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export const metadata = { title: "Audit log" };

/**
 * Who did what: every control-plane mutation the engine has answered — app created,
 * issue deleted, token revoked, retention changed — with the actor, how they were
 * authenticated, and the status the engine returned. Denied attempts are rows too;
 * an audit log that only shows successes is half a log.
 *
 * Server-rendered, and every filter — window, actor, action, page cursor — is a URL
 * param, so any view of the log is a link somebody can paste into a ticket. The filter
 * row is a client component only because the custom range has to convert the browser's
 * local wall-clock into an absolute instant before it reaches the URL; a server doing
 * that would resolve "13:00" in the server's zone.
 *
 * The window defaults to 30 days rather than to everything: an unbounded log is a table
 * that gets slower forever, and "what happened lately" is the question people arrive
 * with.
 */
export default async function AuditPage({
  searchParams,
}: {
  searchParams: Promise<{
    actor?: string;
    action?: string;
    limit?: string;
    range?: string;
    start?: string;
    end?: string;
    before?: string;
    after?: string;
  }>;
}) {
  await requireUser();
  const sp = await searchParams;
  const limit = Math.min(Math.max(Number(sp.limit) || 100, 1), 1000);
  const window = resolveWindow(sp);
  // Cursors are opaque here: the engine validates the shape and rejects a mangled one
  // with a 400, which the page surfaces as an error rather than silently showing the
  // top of the log as though that were what the link asked for.
  const before = sp.before || undefined;
  const after = before ? undefined : sp.after || undefined;

  const result = await getAuditLog({
    actor: sp.actor,
    action: sp.action,
    limit,
    window,
    before,
    after,
  });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Audit log</h1>
        <p className="mt-2 text-muted-foreground">
          Every administrative action against the engine, recorded by the engine itself —
          the UI cannot skip it and neither can anyone talking to the API directly. Reads
          and the data plane (heartbeats, error events) are not logged; reporting an error
          is not an administrative act.
        </p>
      </div>

      <AuditFilters window={window} actor={sp.actor ?? ""} action={sp.action ?? ""} />

      {!result.ok ? (
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>
            {result.error} The audit log lives in the engine&apos;s database.
          </AlertDescription>
        </Alert>
      ) : result.data.entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          Nothing recorded in {windowLabel(window)}
          {sp.actor || sp.action ? " for this filter" : ""}. Widen the range to look further
          back — the log is never trimmed.
        </p>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Via</TableHead>
                <TableHead>Action</TableHead>
                <TableHead className="text-right">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {result.data.entries.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="whitespace-nowrap font-mono text-xs tabular-nums text-muted-foreground">
                    <StampAt iso={e.at} />
                  </TableCell>
                  <TableCell className="font-mono text-[13px]">{e.actor}</TableCell>
                  <TableCell>
                    <StatusPill tone={e.via === "session" || e.via === "operator" ? "init" : "muted"}>
                      {e.via}
                    </StatusPill>
                  </TableCell>
                  <TableCell className="font-mono text-[13px]">{e.action}</TableCell>
                  <TableCell className="text-right">
                    {/* 2xx is the quiet case; anything else is the row worth reading —
                        a 403 here is somebody's credential trying to do what it cannot. */}
                    <span
                      className={`font-mono text-[13px] tabular-nums ${
                        e.status < 400 ? "text-muted-foreground" : "text-status-down"
                      }`}
                    >
                      {e.status}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}

      {result.ok && result.data.entries.length > 0 && (
        <Pager
          params={{
            actor: sp.actor,
            action: sp.action,
            range: sp.range,
            start: sp.start,
            end: sp.end,
            limit: sp.limit,
          }}
          newest={cursorOf(result.data.entries[0])}
          oldest={cursorOf(result.data.entries[result.data.entries.length - 1])}
          hasNewer={result.data.has_newer}
          hasOlder={result.data.has_older}
          showing={result.data.entries.length}
        />
      )}

      <p className="text-[13px] text-muted-foreground">
        Showing {windowLabel(window)}, newest first. Paging walks by row id rather than by
        page number, so a new entry arriving while you read cannot shift a row onto a page
        you already passed.
      </p>
    </div>
  );
}

/** The page cursor for a row: the whole sort key, matching what the engine parses. */
function cursorOf(e: { at: string; id: number } | undefined): string | undefined {
  return e ? `${e.at},${e.id}` : undefined;
}

/** Names the window in the same words the picker offers, so the two cannot disagree. */
function windowLabel(w: { range: string; start?: string; end?: string }): string {
  if (customReady(w)) return "a custom range";
  const preset = RANGES.find((r) => r.value === w.range && r.value !== "custom");
  return (preset?.label ?? RANGES.find((r) => r.value === DEFAULT_RANGE)!.label).toLowerCase();
}
