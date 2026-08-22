import { requireUser } from "@/lib/rbac";
import { listChannels } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ChannelsTable } from "./channels-table";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function AlertsPage() {
  await requireUser();
  const result = await listChannels();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Alerts</h1>
        <p className="mt-2 text-muted-foreground">
          The shared catalogue: webhooks defined once here, then imported by the projects that
          should alert to them. A new project imports all of these by default, so adding one
          below starts alerting everywhere — narrow it per project on that project&apos;s{" "}
          <b>Alerts</b> page, which is also where a project adds webhooks of its own.
        </p>
        <p className="mt-2 text-muted-foreground">
          Each row receives a card for every <b>new</b> error issue and every monitor incident
          in the projects that imported it. Recurrences of a known issue update its count
          instead of re-alerting, so a crash loop is one card, not a flood; a burst of many
          different issues collapses into one digest per project, controlled by that
          project&apos;s alert patience. Editing a URL here rotates it for every importer at
          once.
        </p>
        <p className="mt-2 text-sm text-muted-foreground">
          For Teams: open the chat or channel → ⋯ → Workflows → &quot;Post to a chat when a
          webhook request is received&quot; (or the channel variant) → copy the request URL.
          Cards are sent as Adaptive Cards, which is what the Workflows trigger renders — the
          classic Connectors webhook was retired by Microsoft in May 2026.
        </p>
      </div>

      {!result.ok ? (
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      ) : (
        <ChannelsTable channels={result.data.channels} />
      )}
    </div>
  );
}
