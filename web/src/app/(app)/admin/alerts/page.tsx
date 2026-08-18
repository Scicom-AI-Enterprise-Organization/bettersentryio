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
        <p className="mt-2 max-w-3xl text-muted-foreground">
          Webhooks that receive a card for every <b>new</b> error issue and every monitor
          incident, the moment it happens. Recurrences of a known issue update its count instead
          of re-alerting, so a crash loop is one card, not a flood. Every enabled row gets every
          alert.
        </p>
        <p className="mt-2 max-w-3xl text-sm text-muted-foreground">
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
