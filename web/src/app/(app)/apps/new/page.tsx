import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { NewAppForm } from "@/components/bsio/new-app-form";

export const metadata = { title: "Create an app" };

export default async function NewAppPage() {
  await requireUser();

  return (
    <div className="max-w-5xl space-y-6">
      <Link
        href="/apps"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-3 w-3" />
        All apps
      </Link>

      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Create a new app in 3 steps</h1>
        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
          One app per service — the TTS API, vLLM serving, a nightly export. Each gets its own
          ingest key, so a leak or a rotation affects one service instead of the estate. Its
          monitors register themselves on the first heartbeat.
        </p>
      </div>

      <NewAppForm />
    </div>
  );
}
