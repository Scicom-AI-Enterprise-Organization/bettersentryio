import { requireUser } from "@/lib/rbac";
import { getApiTokens } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { TokensTable } from "./tokens-table";

export const dynamic = "force-dynamic";
export const revalidate = 0;

export const metadata = { title: "API tokens" };

/**
 * The credential you hand a dashboard.
 *
 * It exists because the two credentials that came before it are both wrong for the
 * job: the operator token can delete apps and lives in the engine's environment, so it
 * can only be rotated by redeploying; an ingest key is minted for *writing* events and
 * gets embedded in client code. A token here is named, read-only, individually
 * revocable, and reports when it was last used.
 */
export default async function TokensPage() {
  await requireUser();
  const result = await getApiTokens();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">API tokens</h1>
        <p className="mt-2 text-muted-foreground">
          A token reads this install through the Sentry-compatible API — issues, events and
          their time series. It is what Grafana&apos;s Sentry datasource means by an{" "}
          <b>auth token</b>. Anything holding one can read every project, and can change
          nothing: creating apps, muting monitors and editing webhooks all still need the
          operator token or a signed-in session like this one.
        </p>
        <p className="mt-2 text-muted-foreground">
          Each token is shown once, at creation. The engine keeps only a SHA-256 of it, so a
          database dump is not a pile of working credentials — and a lost token is replaced
          rather than recovered. <b>Last used</b> is recorded at most once a minute, which is
          enough to tell a token that something depends on from one that is safe to revoke.
        </p>
        <p className="mt-2 text-sm text-muted-foreground">
          To point Grafana at us: <b>Connections → Data sources → Sentry</b>, URL{" "}
          <code className="font-mono text-xs">http://host.docker.internal:9090</code> (the
          engine, not this UI — inside a container <code className="font-mono text-xs">localhost</code>{" "}
          is the container), organization slug{" "}
          <code className="font-mono text-xs">bettersentryio</code>, and the token below as the
          auth token. <b>Save &amp; test</b> should answer{" "}
          <i>plugin health check successful</i>. The provisioned stack in{" "}
          <code className="font-mono text-xs">examples/grafana</code> does all of that for you.
        </p>
      </div>

      {!result.ok ? (
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>
            {result.error} Tokens are stored by the engine, so they cannot be listed or created
            while it is down.
          </AlertDescription>
        </Alert>
      ) : (
        <TokensTable tokens={result.data.tokens} />
      )}
    </div>
  );
}
