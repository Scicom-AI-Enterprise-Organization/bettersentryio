import { notFound } from "next/navigation";
import Link from "next/link";

import { requireUser } from "@/lib/rbac";
import { getApp, getProjectAlerts } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ProjectHeader } from "@/components/bsio/project-tabs";
import { ImportedTable } from "./imported-table";
import { OwnTable } from "./own-table";
import { PatienceCard } from "./patience-card";
import { patienceLabel } from "./patience";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return { title: `Alerts · ${slug}` };
}

/**
 * Where this app's alerts go, and how noisy they are allowed to be.
 *
 * Routing is the sum of two lists: shared definitions the app has imported from the
 * global catalogue, and webhooks the app owns outright. Nothing else receives its
 * alerts — a global webhook nobody imported is a definition, not a subscription.
 */
export default async function ProjectAlertsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  await requireUser();
  const { slug } = await params;

  const [appResult, alertsResult] = await Promise.all([getApp(slug), getProjectAlerts(slug)]);
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

  if (!alertsResult.ok) {
    return (
      <div className="space-y-6">
        <ProjectHeader app={app} title="Alerts" />
        <Alert variant="destructive">
          <AlertTitle>Could not load this app&apos;s alerting</AlertTitle>
          <AlertDescription>{alertsResult.error}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const { channels, globals, patience_seconds, patience_choices } = alertsResult.data;
  const routes = globals.filter((c) => c.imported && c.enabled).length +
    channels.filter((c) => c.enabled).length;

  return (
    <div className="space-y-6">
      <ProjectHeader
        app={app}
        title="Alerts"
        subtitle="Where this app's alerts go, and how patient it is about bursts. New issues, regressions and monitor incidents all ride the same routing."
        link={{ href: "/admin/alerts", label: "global catalogue" }}
      />

      {routes === 0 ? (
        <Alert variant="destructive">
          <AlertTitle>This app alerts nobody</AlertTitle>
          <AlertDescription>
            No channel is routed to {app.name}. Import one from the catalogue below, or add a
            webhook of its own — until then its new issues and incidents are recorded but not
            announced.
          </AlertDescription>
        </Alert>
      ) : (
        <p className="text-[13px] text-muted-foreground">
          {routes} channel{routes === 1 ? "" : "s"} receive this app&apos;s alerts
          {patience_seconds > 0
            ? `, at most one card per ${patienceLabel(patience_seconds).toLowerCase()} each after the first.`
            : ", with no patience window — every alert sends its own card."}
        </p>
      )}

      <PatienceCard
        slug={slug}
        seconds={patience_seconds}
        choices={patience_choices}
      />

      <ImportedTable slug={slug} globals={globals} />

      <OwnTable slug={slug} channels={channels} />

      <p className="text-[13px] text-muted-foreground">
        Recurrences of a known issue update its count instead of re-alerting, so a crash loop
        is one card either way. Patience is what protects you from the other flood: many
        <em> different</em> new issues at once.{" "}
        <Link href="/admin/alerts" className="underline underline-offset-2 hover:text-foreground">
          Manage shared webhooks
        </Link>
        .
      </p>
    </div>
  );
}
