import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { getApp, getIssue } from "@/lib/bsio";
import type { EventFrame, EventPayload } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/ui/status-pill";
import type { StatusTone } from "@/components/ui/status-pill";
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { Ago, ClockAt } from "@/components/bsio/time";

export const dynamic = "force-dynamic";
export const revalidate = 0;

/**
 * One grouped error issue, rendered from the SDK's own stored payload. The
 * stock sentry_sdk sends locals, source context, breadcrumbs and custom
 * contexts (e.g. Grafana deep links) — this page's job is to show all of it,
 * because storing what we do not render was exactly the "mana takde pun" bug.
 */
export default async function ErrorIssuePage({
  params,
}: {
  params: Promise<{ slug: string; id: string }>;
}) {
  await requireUser();
  const { slug, id } = await params;

  const [appResult, issueResult] = await Promise.all([getApp(slug), getIssue(id)]);
  if (!issueResult.ok) {
    if (issueResult.error.includes("404")) notFound();
    return (
      <Alert variant="destructive">
        <AlertTitle>Could not load this issue</AlertTitle>
        <AlertDescription>{issueResult.error}</AlertDescription>
      </Alert>
    );
  }
  const { issue, latest_event: ev, recent } = issueResult.data;
  const app = appResult.ok ? appResult.data.app : null;

  const excs = ev?.exception?.values ?? [];
  const message = ev?.message || ev?.logentry?.formatted || ev?.logentry?.message || "";
  // The issue's tags are the client's merged with server-derived ones; older
  // issues predating derivation fall back to what the event payload carried.
  const tags = issue.tags && Object.keys(issue.tags).length > 0 ? issue.tags : (ev?.tags ?? null);
  const contexts = ev?.contexts ?? null;
  const crumbs = normalizeCrumbs(ev);

  return (
    <div className="max-w-5xl space-y-6">
      {app && (
        <ProjectHeader
          app={app}
          title={issue.title}
          subtitle={issue.culprit || issue.kind}
          actions={
            <Button asChild variant="outline" size="sm">
              <Link href={`/apps/${slug}/issues/outages`}>
                <ArrowLeft className="h-3.5 w-3.5" />
                All errors
              </Link>
            </Button>
          }
        />
      )}

      {/* ---- issue facts ---------------------------------------------------- */}
      <section className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
        <StatusPill tone={levelTone(issue.level)}>{issue.level}</StatusPill>
        <span className="font-mono tabular-nums">{issue.times_seen}× seen</span>
        <span className="text-muted-foreground">
          first <Ago iso={issue.first_seen} /> · last <Ago iso={issue.last_seen} />
        </span>
        <span className="font-mono text-xs text-muted-foreground">{issue.environment}</span>
        {ev?.release && (
          <span className="font-mono text-xs text-muted-foreground">release {ev.release}</span>
        )}
        {ev?.server_name && (
          <span className="font-mono text-xs text-muted-foreground">{ev.server_name}</span>
        )}
        {issue.resolved_at && <StatusPill tone="muted">resolved</StatusPill>}
      </section>

      {/* ---- tags ----------------------------------------------------------- */}
      {tags && Object.keys(tags).length > 0 && (
        <section className="flex flex-wrap gap-1.5">
          {Object.entries(tags).map(([k, v]) => (
            <span
              key={k}
              className="rounded-md border border-border bg-muted/40 px-2 py-0.5 font-mono text-xs"
            >
              <span className="text-muted-foreground">{k}:</span> {v}
            </span>
          ))}
        </section>
      )}

      {/* ---- message-only events --------------------------------------------- */}
      {excs.length === 0 && message && (
        <SectionCard title="Message">
          <p className="whitespace-pre-wrap font-mono text-sm">{message}</p>
          {ev?.logger && (
            <p className="mt-2 text-xs text-muted-foreground">logger {ev.logger}</p>
          )}
        </SectionCard>
      )}

      {/* ---- exception chain, raised exception first ------------------------- */}
      {[...excs].reverse().map((ex, i) => (
        <SectionCard
          key={i}
          title={`${ex.type ?? "Error"}${excs.length > 1 ? (i === 0 ? " — raised" : " — caused by") : ""}`}
        >
          {ex.value && <p className="mb-3 font-mono text-sm">{ex.value}</p>}
          {ex.mechanism?.type && (
            <p className="mb-3 text-xs text-muted-foreground">
              mechanism {ex.mechanism.type} ·{" "}
              {ex.mechanism.handled === false ? "unhandled" : "handled"}
            </p>
          )}
          <Frames frames={ex.stacktrace?.frames ?? []} />
        </SectionCard>
      ))}

      {/* ---- request ---------------------------------------------------------- */}
      {ev?.request?.url && (
        <SectionCard title="Request">
          <p className="font-mono text-sm">
            {ev.request.method} {ev.request.url}
            {ev.request.query_string ? `?${ev.request.query_string}` : ""}
          </p>
          {ev.request.headers && (
            <KVRows
              rows={Object.entries(ev.request.headers).map(([k, v]) => [k, String(v)])}
            />
          )}
        </SectionCard>
      )}

      {/* ---- contexts: URL values become links (the Grafana pattern) ---------- */}
      {contexts &&
        Object.entries(contexts)
          .filter(([k]) => k !== "trace")
          .map(([name, ctx]) => (
            <SectionCard key={name} title={`Context — ${name}`}>
              <KVRows
                rows={Object.entries(ctx)
                  .filter(([k]) => k !== "type")
                  .map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)])}
              />
            </SectionCard>
          ))}

      {/* ---- breadcrumbs -------------------------------------------------------- */}
      {crumbs.length > 0 && (
        <SectionCard title={`Breadcrumbs — last ${crumbs.length} before the event`}>
          <div className="space-y-1">
            {crumbs.map((c, i) => (
              <div key={i} className="flex gap-3 font-mono text-xs">
                <span className="w-24 shrink-0 text-muted-foreground">{c.category ?? "log"}</span>
                <span className="w-14 shrink-0 text-muted-foreground">{c.level ?? ""}</span>
                <span className="min-w-0 break-all">{c.message ?? ""}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      )}

      {/* ---- recent events ------------------------------------------------------ */}
      {recent.length > 1 && (
        <SectionCard title="Recent events in this issue">
          <div className="space-y-1">
            {recent.map((r) => (
              <div key={r.id} className="flex gap-3 font-mono text-xs">
                <span className="w-40 shrink-0 tabular-nums text-muted-foreground">
                  <ClockAt iso={r.received_at} />
                </span>
                <span className="min-w-0 break-all">{r.message}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      )}

      {ev?.sdk?.name && (
        <p className="text-xs text-muted-foreground">
          reported by {ev.sdk.name} {ev.sdk.version}
        </p>
      )}
    </div>
  );
}

function levelTone(level: string): StatusTone {
  switch (level) {
    case "fatal":
    case "error":
      return "down";
    case "warning":
      return "idle";
    default:
      return "init";
  }
}

function SectionCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-border p-5">
      <h2 className="mb-3 text-sm font-semibold tracking-tight">{title}</h2>
      {children}
    </section>
  );
}

function KVRows({ rows }: { rows: [string, string][] }) {
  return (
    <div className="mt-2 space-y-1">
      {rows.map(([k, v]) => (
        <div key={k} className="flex gap-3 font-mono text-xs">
          <span className="w-40 shrink-0 text-muted-foreground">{k}</span>
          {/^https?:\/\//.test(v) ? (
            <a
              href={v}
              target="_blank"
              rel="noreferrer"
              className="min-w-0 break-all text-primary hover:underline"
            >
              {v}
            </a>
          ) : (
            <span className="min-w-0 break-all">{v}</span>
          )}
        </div>
      ))}
    </div>
  );
}

function contextLine(f: EventFrame): string {
  if (typeof f.context_line === "string") return f.context_line;
  if (Array.isArray(f.context_line)) return f.context_line.join("\n");
  return "";
}

/**
 * Stack frames, crash frame first (callee-last on the wire). In-app frames
 * render expanded with their source window and locals; library frames collapse
 * to one line — present, but out of the way.
 */
function Frames({ frames }: { frames: EventFrame[] }) {
  if (frames.length === 0) return <p className="text-xs text-muted-foreground">no stacktrace</p>;
  const ordered = [...frames].reverse();
  return (
    <div className="divide-y divide-border overflow-hidden rounded-lg border border-border">
      {ordered.map((f, i) => {
        const line = contextLine(f);
        const hasBody =
          !!line || (f.pre_context?.length ?? 0) > 0 || !!f.vars;
        const label = (
          <span className="min-w-0 break-all font-mono text-xs">
            <span className={f.in_app ? "font-semibold" : "text-muted-foreground"}>
              {f.filename ?? f.module ?? "?"}
            </span>
            <span className="text-muted-foreground">
              :{f.lineno ?? "?"} in {f.function ?? "?"}
            </span>
            {f.in_app && (
              <span className="ml-2 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                in app
              </span>
            )}
          </span>
        );
        if (!hasBody) return <div key={i} className="px-3 py-1.5">{label}</div>;
        return (
          <details key={i} open={f.in_app}>
            <summary className="cursor-pointer list-none px-3 py-1.5 hover:bg-muted/40">
              {label}
            </summary>
            <div className="space-y-2 border-t border-border bg-muted/30 px-3 py-2">
              {(f.pre_context?.length || line) && (
                <pre className="overflow-x-auto font-mono text-xs leading-5">
                  {(f.pre_context ?? []).map((l, n) => (
                    <div key={`pre${n}`} className="text-muted-foreground">
                      {(f.lineno ?? 0) - (f.pre_context?.length ?? 0) + n}  {l}
                    </div>
                  ))}
                  {line && (
                    <div className="bg-status-down/10 font-semibold">
                      {f.lineno ?? "?"}  {line}
                    </div>
                  )}
                  {(f.post_context ?? []).map((l, n) => (
                    <div key={`post${n}`} className="text-muted-foreground">
                      {(f.lineno ?? 0) + 1 + n}  {l}
                    </div>
                  ))}
                </pre>
              )}
              {f.vars && Object.keys(f.vars).length > 0 && (
                <div>
                  <p className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                    Local variables
                  </p>
                  <KVRows
                    rows={Object.entries(f.vars).map(([k, v]) => [
                      k,
                      typeof v === "string" ? v : JSON.stringify(v),
                    ])}
                  />
                </div>
              )}
            </div>
          </details>
        );
      })}
    </div>
  );
}

type Crumb = { category?: string; level?: string; message?: string };

function normalizeCrumbs(ev: EventPayload | null | undefined): Crumb[] {
  const b = ev?.breadcrumbs;
  if (!b) return [];
  const list = Array.isArray(b) ? b : (b.values ?? []);
  return list.slice(-30);
}
