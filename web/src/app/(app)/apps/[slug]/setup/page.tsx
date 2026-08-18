import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, Check } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { getApp } from "@/lib/bsio";
import { platform as findPlatform } from "@/lib/platforms";
import { dsnFor, errorTracking, ingestBase, integration, type Block } from "@/lib/snippets";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { AppWatcher } from "@/components/bsio/app-watcher";
import { CodeBlock, CopyField } from "@/components/bsio/code-block";
import { CopyButton } from "@/components/bsio/copy-button";
import { DeleteAppDialog } from "@/components/bsio/delete-app-dialog";
import { ProjectHeader } from "@/components/bsio/project-tabs";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function AppPage({
  params,
  searchParams,
}: {
  params: Promise<{ slug: string }>;
  searchParams: Promise<{ created?: string; progress?: string }>;
}) {
  await requireUser();
  const { slug } = await params;
  const { created, progress } = await searchParams;
  const result = await getApp(slug);

  if (!result.ok) {
    if (result.error.includes("404")) notFound();
    return (
      <div className="space-y-6">
        <Alert variant="destructive">
          <AlertTitle>Could not load this app</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const { app } = result.data;
  const plat = findPlatform(app.platform);
  const guide = integration(app, plat?.id ?? "python", { progress: progress !== "0" });
  const errors = errorTracking(app);

  // Each half ticks off from its own evidence: an error event proves the DSN
  // works; a monitor proves a beat arrived. `connected` is either.
  const errorsDone = app.last_event_at !== null;
  const beatsDone = app.monitors > 0;

  return (
    <div className="max-w-5xl space-y-8">
      <ProjectHeader
        app={app}
        title={plat ? `Configure the ${plat.name} integration` : "Configure this app"}
        subtitle="Three steps, already filled in with this project's key. Steps tick themselves off from real state, not from clicks."
        actions={
          <div className="flex items-center gap-1">
            <Button asChild variant="outline" size="sm">
              <Link href="/apps/new">
                <ArrowLeft className="h-3.5 w-3.5" />
                Platform selection
              </Link>
            </Button>
            <DeleteAppDialog slug={app.slug} name={app.name} monitors={app.monitors} />
          </div>
        }
      />

      {created === "1" && !app.connected && (
        <Alert>
          <Check className="h-4 w-4 text-status-active" />
          <AlertTitle>App created</AlertTitle>
          <AlertDescription>
            Point sentry_sdk at the DSN below for errors, and paste the heartbeat snippet into
            {" "}{app.name}&apos;s loop. Everything registers itself on first contact — there is
            nothing else to configure here.
          </AlertDescription>
        </Alert>
      )}

      {/* ---- credentials -------------------------------------------------- */}
      <section className="grid gap-4 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <CopyField label="DSN (errors — paste into sentry_sdk.init)" value={dsnFor(app)} />
        </div>
        <CopyField label="Engine URL (heartbeats)" value={ingestBase()} />
        <CopyField label="Ingest key" value={app.ingest_key} />
      </section>
      <p className="-mt-4 text-xs text-muted-foreground">
        One key, two protocols: inside the DSN it lets the stock sentry_sdk report errors;
        as <span className="font-mono">BSIO_KEY</span> it reports heartbeats. Treat it as a
        credential — a secret or an env var, not your repo. It cannot create or delete apps.
      </p>

      {/* ---- the steps ------------------------------------------------------ */}
      <div className="space-y-8">
        <Step
          n={1}
          title={errors.title}
          done={errorsDone}
          body={errors.body}
          action={
            <CopyButton
              label="Copy instructions"
              value={errors.blocks.map((b) => b.code).join("\n\n")}
            />
          }
        >
          <Blocks blocks={errors.blocks} />
        </Step>

        {guide.install && (
          <Step n={2} title={`${guide.install.title} the heartbeat client`} done={beatsDone} body={guide.install.body}>
            <Blocks blocks={guide.install.blocks} />
          </Step>
        )}

        <Step
          n={guide.install ? 3 : 2}
          title="Heartbeats — the loop that must not stop"
          done={beatsDone}
          body={guide.configure.body}
          action={
            <CopyButton
              label="Copy instructions"
              value={guide.configure.blocks.map((b) => b.code).join("\n\n")}
            />
          }
        >
          <Blocks blocks={guide.configure.blocks} />
          <p className="text-xs text-muted-foreground">
            The monitor name <span className="font-mono text-foreground">{guide.monitor}</span> is
            a suggestion — use one name per loop. Each distinct name becomes its own monitor the
            first time it beats. For what the numbers mean and how to pick them, see{" "}
            <Link href="/learn" className="text-primary hover:underline">
              How it works
            </Link>
            .
          </p>
        </Step>

        <Step
          n={guide.install ? 4 : 3}
          title="Verify"
          done={app.connected}
          body="Live from the engine, rechecked every 3 seconds. Nothing here needs a refresh."
        >
          <AppWatcher app={app.slug} initialConnected={app.connected} />
        </Step>
      </div>

      {app.connected && (
        <div className="flex flex-wrap gap-2 border-t border-border pt-6">
          <Button asChild size="sm">
            <Link href={`/apps/${app.slug}/issues/outages`}>Take me to Issues</Link>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link href="/monitors">View monitors</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/learn">How it works</Link>
          </Button>
        </div>
      )}
    </div>
  );
}

function Blocks({ blocks }: { blocks: Block[] }) {
  return (
    <div className="space-y-3">
      {blocks.map((b) => (
        <CodeBlock key={b.filename} code={b.code} language={b.language} filename={b.filename} />
      ))}
    </div>
  );
}

/**
 * A numbered step that marks itself done from real state rather than from clicks — the
 * checkmark means "we have seen a heartbeat", which is the only evidence that counts.
 */
function Step({
  n,
  title,
  body,
  done,
  action,
  children,
}: {
  n: number;
  title: string;
  body?: string;
  done?: boolean;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <span
            className={
              done
                ? "flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-status-active/15 text-status-active"
                : "flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground"
            }
          >
            {done ? <Check className="h-3.5 w-3.5" /> : n}
          </span>
          <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
        </div>
        {action}
      </div>
      <div className="space-y-3 pl-[34px]">
        {body && <p className="max-w-3xl text-sm text-muted-foreground">{body}</p>}
        {children}
      </div>
    </section>
  );
}

