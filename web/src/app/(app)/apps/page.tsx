import { requireUser } from "@/lib/rbac";
import { getApps } from "@/lib/bsio";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ProjectGrid } from "@/components/bsio/project-grid";

export const dynamic = "force-dynamic";
export const revalidate = 0;
export const metadata = { title: "All projects" };

export default async function ProjectsPage() {
  await requireUser();
  const result = await getApps();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">All projects</h1>
        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
          One project per service. Each has its own ingest key, and its monitors register
          themselves on the first heartbeat.
        </p>
      </div>

      {!result.ok ? (
        <Alert variant="destructive">
          <AlertTitle>The engine is unreachable</AlertTitle>
          <AlertDescription>
            {result.error} Detection and alerting run in the Go engine, not in this UI — if it
            is down, nothing is being monitored right now.
          </AlertDescription>
        </Alert>
      ) : (
        <ProjectGrid apps={result.data.apps} />
      )}
    </div>
  );
}
