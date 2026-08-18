import { requireUser } from "@/lib/rbac";
import { getTeamsAlert } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { WebhookForm } from "./webhook-form";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export default async function AlertsPage() {
  await requireUser();
  const result = await getTeamsAlert();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Alerts</h1>
        <p className="mt-2 text-muted-foreground">
          One Microsoft Teams incoming webhook. Every <b>new</b> error issue and every monitor
          incident posts a card the moment it happens — recurrences of a known issue update its
          count instead of re-alerting, so a crash loop is one card, not a flood.
        </p>
      </div>

      {!result.ok ? (
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      ) : (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-3">
              <CardTitle>Microsoft Teams</CardTitle>
              {result.data.configured ? (
                <StatusPill tone="active">on</StatusPill>
              ) : (
                <StatusPill tone="muted">off</StatusPill>
              )}
            </div>
            <CardDescription>
              In Teams: open the chat or channel → ⋯ → Workflows → &quot;Post to a chat when a
              webhook request is received&quot; (or the channel variant) → copy the request URL
              here. The classic Connectors incoming webhook was retired by Microsoft in May 2026;
              cards are sent in the Adaptive Card format the Workflows trigger expects.
              {result.data.configured && result.data.url_masked && (
                <> Currently: <span className="font-mono">{result.data.url_masked}</span></>
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <WebhookForm placeholder={result.data.url_masked} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
