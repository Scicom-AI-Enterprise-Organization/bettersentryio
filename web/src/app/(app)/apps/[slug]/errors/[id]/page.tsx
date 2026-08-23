import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { getApp, getEventAttachments, getIssue, getIssueEvent, getIssueSeries } from "@/lib/bsio";
import { issueStatus } from "@/lib/format";
import type { EventFrame, EventPayload } from "@/lib/bsio";
import { IssueActions } from "./issue-actions";
import { OccurrenceChart } from "@/components/bsio/occurrence-chart";
import { resolveWindow } from "@/lib/ranges";
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
  searchParams,
}: {
  params: Promise<{ slug: string; id: string }>;
  searchParams: Promise<{
    event?: string;
    range?: string;
    interval?: string;
    start?: string;
    end?: string;
  }>;
}) {
  await requireUser();
  const { slug, id } = await params;
  const sp = await searchParams;
  const eventParam = sp.event;
  const w = resolveWindow(sp);

  const [appResult, issueResult, seriesResult] = await Promise.all([
    getApp(slug),
    getIssue(id),
    getIssueSeries(id, w),
  ]);
  if (!issueResult.ok) {
    if (issueResult.error.includes("404")) notFound();
    return (
      <Alert variant="destructive">
        <AlertTitle>Could not load this issue</AlertTitle>
        <AlertDescription>{issueResult.error}</AlertDescription>
      </Alert>
    );
  }
  const { issue, latest_event: latest, recent } = issueResult.data;
  const app = appResult.ok ? appResult.data.app : null;

  // Event browsing: ?event=<id> renders that stored occurrence instead of the
  // newest one; prev/next step through the recent list (newest first).
  let ev: EventPayload | null | undefined = latest;
  let currentIdx = 0;
  if (eventParam && recent.length > 0) {
    const idx = recent.findIndex((r) => String(r.id) === eventParam);
    if (idx >= 0) {
      currentIdx = idx;
      const one = await getIssueEvent(id, eventParam);
      if (one.ok) ev = one.data.payload;
    }
  }
  const newer = currentIdx > 0 ? recent[currentIdx - 1] : null;
  const older = currentIdx < recent.length - 1 ? recent[currentIdx + 1] : null;
  const status = issueStatus(issue);

  const excs = ev?.exception?.values ?? [];
  const message = ev?.message || ev?.logentry?.formatted || ev?.logentry?.message || "";
  // The issue's tags are the client's merged with server-derived ones; older
  // issues predating derivation fall back to what the event payload carried.
  const tags = issue.tags && Object.keys(issue.tags).length > 0 ? issue.tags : (ev?.tags ?? null);
  const contexts = ev?.contexts ?? null;
  const crumbs = normalizeCrumbs(ev);
  const threads = (ev?.threads?.values ?? []).filter((t) => t.stacktrace?.frames?.length);
  const attachmentsResult = ev?.event_id
    ? await getEventAttachments(slug, String(ev.event_id))
    : null;
  const attachments = attachmentsResult?.ok ? attachmentsResult.data.attachments : [];

  return (
    <div className="space-y-6">
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
      <section className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[15px]">
        <StatusPill tone={levelTone(issue.level)}>{issue.level}</StatusPill>
        <span className="font-mono tabular-nums">{issue.times_seen}× seen</span>
        <span className="text-muted-foreground">
          first <Ago iso={issue.first_seen} /> · last <Ago iso={issue.last_seen} />
        </span>
        <span className="font-mono text-[13px] text-muted-foreground">{issue.environment}</span>
        {ev?.release && (
          <span className="font-mono text-[13px] text-muted-foreground">release {ev.release}</span>
        )}
        {ev?.server_name && (
          <span className="font-mono text-[13px] text-muted-foreground">{ev.server_name}</span>
        )}
        {status === "resolved" && <StatusPill tone="active">resolved</StatusPill>}
        {status === "archived" && (
          <StatusPill tone="muted">
            archived{issue.archive_recur ? " · until it recurs" : issue.archived_until ? " · temporary" : ""}
          </StatusPill>
        )}
      </section>

      {/* ---- actions ---------------------------------------------------------- */}
      <IssueActions
        id={issue.id}
        slug={slug}
        resolved={status === "resolved"}
        archived={status === "archived"}
        priority={issue.priority}
      />

      {/* ---- occurrence volume ------------------------------------------------- */}
      <OccurrenceChart
        title="Occurrences"
        rows={seriesResult.ok ? seriesResult.data.buckets.map((b) => ({ at: b.at, count: b.count })) : []}
        series={[{ key: "count", label: "events", color: "var(--status-down)" }]}
        total={seriesResult.ok ? seriesResult.data.total : 0}
        intervalSeconds={seriesResult.ok ? seriesResult.data.interval_s : 3600}
        range={w.range}
        interval={w.interval ?? "auto"}
        error={seriesResult.ok ? undefined : seriesResult.error}
      />

      {/* ---- event navigation -------------------------------------------------- */}
      {recent.length > 1 && (
        <section className="flex flex-wrap items-center gap-3 text-[15px]">
          {newer ? (
            <Link
              className="text-primary hover:underline"
              href={`/apps/${slug}/errors/${issue.id}?event=${newer.id}`}
            >
              ← newer
            </Link>
          ) : (
            <span className="text-muted-foreground">← newer</span>
          )}
          <span className="font-mono text-[13px] text-muted-foreground">
            event {currentIdx + 1} of {recent.length} recent
            {recent[currentIdx] && <> · <ClockAt iso={recent[currentIdx].received_at} /></>}
          </span>
          {older ? (
            <Link
              className="text-primary hover:underline"
              href={`/apps/${slug}/errors/${issue.id}?event=${older.id}`}
            >
              older →
            </Link>
          ) : (
            <span className="text-muted-foreground">older →</span>
          )}
        </section>
      )}

      {/* ---- tags: Sentry-style key/value pills ------------------------------- */}
      {tags && Object.keys(tags).length > 0 && (
        <SectionCard title="Tags">
          <div className="flex flex-wrap gap-2">
            {Object.entries(tags)
              .sort()
              .map(([k, v]) => (
                <span
                  key={k}
                  className="inline-flex max-w-full items-stretch overflow-hidden rounded-full border border-border bg-card font-mono text-[13px] leading-6"
                >
                  <span className="bg-background px-3 py-0.5 text-muted-foreground">{k}</span>
                  <span className="min-w-0 truncate border-l border-border px-3 py-0.5">{v}</span>
                </span>
              ))}
          </div>
        </SectionCard>
      )}

      {/* ---- event header: identity + quick chips + raw JSON ------------------- */}
      {ev && (
        <section className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border pb-3 font-mono text-[13px]">
          {ev.event_id && <span className="font-semibold">ID: {String(ev.event_id).slice(0, 8)}</span>}
          {recent[currentIdx] && (
            <span className="text-muted-foreground"><Ago iso={recent[currentIdx].received_at} /></span>
          )}
          <Link
            href={`/apps/${slug}/errors/${issue.id}/json${recent[currentIdx] ? `?event=${recent[currentIdx].id}` : ""}`}
            className="text-primary hover:underline"
            prefetch={false}
          >
            JSON
          </Link>
          <span className="text-muted-foreground">·</span>
          {typeof ev.user?.ip_address === "string" && (
            <span className="text-muted-foreground">{ev.user.ip_address}</span>
          )}
          {ev.contexts?.runtime && (
            <span className="text-muted-foreground">
              {String(ev.contexts.runtime.name ?? "")} {String(ev.contexts.runtime.version ?? "")}
            </span>
          )}
          {ev.release && <span className="text-muted-foreground">📦 {ev.release}</span>}
          {ev.dist && <span className="text-muted-foreground">dist {ev.dist}</span>}
          {ev.environment && <span className="text-muted-foreground">{ev.environment}</span>}
        </section>
      )}

      {/* ---- highlights: the fields you read first, curated like Sentry's ------ */}
      {tags && (
        <SectionCard title="Highlights">
          <div className="grid gap-x-8 gap-y-1 sm:grid-cols-2">
            <KVRows
              rows={(["handled", "level", "mechanism", "transaction"] as const)
                .filter((k) => tags[k])
                .map((k) => [k, tags[k]] as [string, string])}
            />
            <KVRows
              rows={[
                ...(tags.url ? ([["url", tags.url]] as [string, string][]) : []),
                ...(typeof ev?.contexts?.trace?.trace_id === "string"
                  ? ([["trace id", String(ev.contexts.trace.trace_id)]] as [string, string][])
                  : []),
              ]}
            />
          </div>
        </SectionCard>
      )}

      {/* ---- message-only events --------------------------------------------- */}
      {message && (
        <SectionCard title="Message">
          <p className="whitespace-pre-wrap font-mono text-[15px] leading-relaxed">{message}</p>
          {ev?.logger && (
            <p className="mt-2 text-[13px] text-muted-foreground">logger {ev.logger}</p>
          )}
        </SectionCard>
      )}

      {/* ---- exception chain, raised exception first ------------------------- */}
      {[...excs].reverse().map((ex, i) => (
        <SectionCard
          key={i}
          title={`${ex.type ?? "Error"}${excs.length > 1 ? (i === 0 ? " — raised" : " — caused by") : ""}`}
        >
          {ex.value && <p className="mb-3 font-mono text-[15px] leading-relaxed">{ex.value}</p>}
          {ex.mechanism?.type && (
            <p className="mb-3 text-[13px] text-muted-foreground">
              mechanism {ex.mechanism.type} ·{" "}
              {ex.mechanism.handled === false ? "unhandled" : "handled"}
            </p>
          )}
          <Frames frames={ex.stacktrace?.frames ?? []} />
        </SectionCard>
      ))}

      {/* ---- threads: attach_stacktrace and crash-time thread dumps ----------- */}
      {threads.length > 0 && (
        <SectionCard title={`Threads (${threads.length})`}>
          <div className="space-y-4">
            {threads.map((t, i) => (
              <div key={i}>
                <p className="mb-2 font-mono text-[13px]">
                  <span className="font-semibold">{t.name || `thread ${t.id ?? i}`}</span>
                  {t.crashed && (
                    <span className="ml-2 rounded bg-status-down/10 px-1.5 py-0.5 text-[11px] font-medium text-status-down">
                      crashed
                    </span>
                  )}
                  {t.current && !t.crashed && (
                    <span className="ml-2 rounded bg-primary/10 px-1.5 py-0.5 text-[11px] font-medium text-primary">
                      current
                    </span>
                  )}
                  {t.state && <span className="ml-2 text-muted-foreground">{t.state}</span>}
                </p>
                <Frames frames={t.stacktrace?.frames ?? []} />
              </div>
            ))}
          </div>
        </SectionCard>
      )}

      {/* ---- attachments -------------------------------------------------------- */}
      {attachments.length > 0 && (
        <SectionCard title={`Attachments (${attachments.length})`}>
          <div className="space-y-1.5">
            {attachments.map((a) => (
              <div key={a.id} className="flex flex-wrap items-baseline gap-x-4 font-mono text-[13px] leading-6">
                <a
                  href={`/apps/${slug}/errors/${issue.id}/attachments/${a.id}`}
                  className="min-w-0 break-all text-primary hover:underline"
                  download={a.filename}
                >
                  {a.filename}
                </a>
                <span className="text-muted-foreground">{a.content_type}</span>
                <span className="tabular-nums text-muted-foreground">{prettySize(a.size)}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      )}

      {/* ---- request ---------------------------------------------------------- */}
      {ev?.request?.url && (
        <SectionCard title="Request">
          <p className="font-mono text-[15px]">
            {ev.request.method} {ev.request.url}
            {ev.request.query_string ? `?${ev.request.query_string}` : ""}
          </p>
          {ev.request.headers && (
            <>
              <p className="mt-4 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Headers</p>
              <KVRows
                rows={Object.entries(ev.request.headers).map(([k, v]) => [k, String(v)])}
              />
            </>
          )}
          {(ev.request as { env?: Record<string, unknown> }).env && (
            <>
              <p className="mt-4 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Environment</p>
              <KVRows
                rows={Object.entries((ev.request as { env?: Record<string, unknown> }).env ?? {}).map(
                  ([k, v]) => [k, String(v)],
                )}
              />
            </>
          )}
        </SectionCard>
      )}

      {/* ---- contexts: URL values become links (the Grafana pattern) ---------- */}
      {ev?.user && Object.keys(ev.user).length > 0 && (
        <SectionCard title="Context — user">
          <KVRows
            rows={Object.entries(ev.user)
              .filter(([, v]) => v !== null && v !== undefined)
              .map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)])}
          />
        </SectionCard>
      )}
      {contexts &&
        Object.entries(contexts)
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
          <div className="space-y-1.5">
            {crumbs.map((c, i) => (
              <div key={i} className="flex gap-3 font-mono text-[13px] leading-6">
                <span className="w-28 shrink-0 text-muted-foreground">{c.category ?? "log"}</span>
                <span className="w-16 shrink-0 text-muted-foreground">{c.level ?? ""}</span>
                <span className="min-w-0 break-all">{c.message ?? ""}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      )}

      {/* ---- recent events ------------------------------------------------------ */}
      {recent.length > 1 && (
        <SectionCard title="Recent events in this issue">
          <div className="space-y-0.5">
            {recent.map((r) => (
              <Link
                key={r.id}
                href={`/apps/${slug}/errors/${issue.id}?event=${r.id}`}
                className="flex gap-3 rounded-md px-2 py-1 font-mono text-[13px] leading-6 hover:bg-muted/40"
              >
                <span className="w-44 shrink-0 tabular-nums text-muted-foreground">
                  <ClockAt iso={r.received_at} />
                </span>
                <span className="min-w-0 break-all">{r.message}</span>
              </Link>
            ))}
          </div>
        </SectionCard>
      )}

      {/* ---- additional data (extra) ------------------------------------------- */}
      {ev?.extra && Object.keys(ev.extra).length > 0 && (
        <SectionCard title="Additional data">
          <KVRows
            rows={Object.entries(ev.extra).map(([k, v]) => [
              k,
              typeof v === "string" ? v : JSON.stringify(v),
            ])}
          />
        </SectionCard>
      )}

      {/* ---- packages, collapsed: useful for "which venv was this" ------------- */}
      {ev?.modules && Object.keys(ev.modules).length > 0 && (
        <details className="rounded-xl border border-border bg-card p-5 shadow-xs">
          <summary className="cursor-pointer text-base font-semibold tracking-tight">
            Packages ({Object.keys(ev.modules).length})
          </summary>
          <div className="mt-4 grid gap-x-8 gap-y-1.5 sm:grid-cols-3">
            {Object.entries(ev.modules)
              .sort()
              .map(([k, v]) => (
                <div key={k} className="flex gap-3 font-mono text-[13px]">
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">{k}</span>
                  <span className="tabular-nums">{v}</span>
                </div>
              ))}
          </div>
        </details>
      )}

      {ev?.sdk?.name && (
        <p className="text-[13px] text-muted-foreground">
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
    <section className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <h2 className="mb-4 text-base font-semibold tracking-tight">{title}</h2>
      {children}
    </section>
  );
}

/** Sentry's key/value treatment: sans muted key column, mono value. */
function KVRows({ rows }: { rows: [string, string][] }) {
  return (
    <div className="mt-2 space-y-1.5">
      {rows.map(([k, v]) => (
        <div key={k} className="flex gap-4 text-[13px] leading-6">
          <span className="w-44 shrink-0 text-muted-foreground">{k}</span>
          {/^https?:\/\//.test(v) ? (
            <a
              href={v}
              target="_blank"
              rel="noreferrer"
              className="min-w-0 break-all font-mono text-primary hover:underline"
            >
              {v}
            </a>
          ) : (
            <span className="min-w-0 break-all font-mono">{v}</span>
          )}
        </div>
      ))}
    </div>
  );
}

function prettySize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
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
  if (frames.length === 0) return <p className="text-[13px] text-muted-foreground">no stacktrace</p>;
  const ordered = [...frames].reverse();
  return (
    <div className="divide-y divide-border overflow-hidden rounded-lg border border-border">
      {ordered.map((f, i) => {
        const line = contextLine(f);
        const hasBody =
          !!line || (f.pre_context?.length ?? 0) > 0 || !!f.vars;
        // Sentry's frame header: file path in accent, "in <function>" with the
        // function emphasized, line number trailing.
        const label = (
          <span className="min-w-0 break-all font-mono text-[13px] leading-6">
            <span className={f.in_app ? "font-medium text-primary" : "text-muted-foreground"}>
              {f.filename ?? f.module ?? "?"}
            </span>
            <span className="text-muted-foreground"> in </span>
            <span className={f.in_app ? "font-semibold" : "text-muted-foreground"}>
              {f.function ?? "?"}
            </span>
            <span className="text-muted-foreground"> at line {f.lineno ?? "?"}</span>
            {f.in_app && (
              <span className="ml-2 rounded bg-primary/10 px-1.5 py-0.5 text-[11px] font-medium text-primary">
                In App
              </span>
            )}
          </span>
        );
        if (!hasBody) return <div key={i} className="px-4 py-2">{label}</div>;
        return (
          <details key={i} open={f.in_app}>
            <summary className="cursor-pointer list-none px-4 py-2 hover:bg-muted/40">
              {label}
            </summary>
            <div className="space-y-3 border-t border-border bg-background/60 px-4 py-3">
              {(f.pre_context?.length || line) && (
                <pre className="overflow-x-auto font-mono text-[13px] leading-6">
                  {(f.pre_context ?? []).map((l, n) => (
                    <div key={`pre${n}`} className="text-muted-foreground">
                      <span className="mr-4 inline-block w-10 select-none text-right opacity-60">
                        {(f.lineno ?? 0) - (f.pre_context?.length ?? 0) + n}
                      </span>
                      {l}
                    </div>
                  ))}
                  {line && (
                    <div className="rounded-sm bg-status-down/10 font-semibold">
                      <span className="mr-4 inline-block w-10 select-none text-right">
                        {f.lineno ?? "?"}
                      </span>
                      {line}
                    </div>
                  )}
                  {(f.post_context ?? []).map((l, n) => (
                    <div key={`post${n}`} className="text-muted-foreground">
                      <span className="mr-4 inline-block w-10 select-none text-right opacity-60">
                        {(f.lineno ?? 0) + 1 + n}
                      </span>
                      {l}
                    </div>
                  ))}
                </pre>
              )}
              {f.vars && Object.keys(f.vars).length > 0 && (
                <div>
                  <p className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
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
