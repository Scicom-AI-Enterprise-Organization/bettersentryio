"use client";

import { useActionState, useState } from "react";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { slugify } from "@/lib/format";
import { PLATFORMS, PLATFORM_GROUPS, type PlatformId } from "@/lib/platforms";
import { addApp, type AddAppState } from "@/app/(app)/apps/actions";

function StepNumber({ n }: { n: number }) {
  return (
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
      {n}
    </span>
  );
}

function StepHeading({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <div className="mb-4 flex items-center gap-2.5">
      <StepNumber n={n} />
      <h2 className="text-lg font-semibold tracking-tight">{children}</h2>
    </div>
  );
}

export function NewAppForm() {
  const [platform, setPlatform] = useState<PlatformId | null>(null);
  const [group, setGroup] = useState<(typeof PLATFORM_GROUPS)[number] | "All">("All");
  const [progress, setProgress] = useState(true);
  const [name, setName] = useState("");
  const [state, formAction, pending] = useActionState<AddAppState, FormData>(addApp, {});

  const visible = PLATFORMS.filter((p) => group === "All" || p.group === group);
  const slug = slugify(name);
  const chosen = PLATFORMS.find((p) => p.id === platform);

  return (
    <form action={formAction} className="space-y-10">
      <input type="hidden" name="platform" value={platform ?? ""} />
      <input type="hidden" name="progress" value={progress ? "1" : ""} />

      {/* ---- step 1: platform ------------------------------------------- */}
      <section>
        <StepHeading n={1}>Choose your platform</StepHeading>

        <div className="mb-4 flex flex-wrap gap-1 border-b border-border">
          {(["All", ...PLATFORM_GROUPS] as const).map((g) => (
            <button
              key={g}
              type="button"
              onClick={() => setGroup(g)}
              className={cn(
                "-mb-px border-b-2 px-3 py-2 text-sm transition-colors",
                group === g
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {g}
            </button>
          ))}
        </div>

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
          {visible.map((p) => (
            <button
              key={p.id}
              type="button"
              onClick={() => setPlatform(p.id)}
              aria-pressed={platform === p.id}
              title={p.blurb}
              className={cn(
                "flex flex-col items-center gap-2 rounded-lg border p-4 transition-colors",
                platform === p.id
                  ? "border-primary bg-primary/5 ring-1 ring-primary"
                  : "border-border hover:border-muted-foreground/40 hover:bg-accent/40",
              )}
            >
              {p.logo}
              <span className="text-center text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {p.name}
              </span>
            </button>
          ))}
        </div>

        {chosen && <p className="mt-3 text-sm text-muted-foreground">{chosen.blurb}</p>}
      </section>

      {/* ---- step 2: what to detect ------------------------------------- */}
      <section>
        <StepHeading n={2}>Choose what to detect</StepHeading>

        <div className="space-y-3">
          <div className="flex items-start gap-3 rounded-lg border border-border bg-muted/30 p-4 opacity-90">
            <div className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-sm bg-primary text-[10px] font-bold text-primary-foreground">
              ✓
            </div>
            <div>
              <p className="text-sm font-medium">
                Liveness <span className="font-normal text-muted-foreground">— always on</span>
              </p>
              <p className="mt-0.5 text-sm text-muted-foreground">
                Heartbeats stop arriving → the monitor goes <span className="font-mono">LATE</span>,
                then <span className="font-mono">MISSING</span>, and an incident opens.
              </p>
            </div>
          </div>

          <label
            className={cn(
              "flex cursor-pointer items-start gap-3 rounded-lg border p-4 transition-colors",
              progress ? "border-primary bg-primary/5" : "border-border hover:bg-accent/40",
            )}
          >
            <input
              type="checkbox"
              checked={progress}
              onChange={(e) => setProgress(e.target.checked)}
              className="mt-0.5 h-4 w-4 accent-[var(--primary)]"
            />
            <div>
              <p className="text-sm font-medium">
                Stall detection{" "}
                <span className="font-normal text-muted-foreground">— recommended</span>
              </p>
              <p className="mt-0.5 text-sm text-muted-foreground">
                Adds a <span className="font-mono">progress</span> counter to the snippet.
                Heartbeats that keep arriving while progress sits still →{" "}
                <span className="font-mono">STALLED</span>. This is the case a{" "}
                <span className="font-mono">/health</span> check cannot see.
              </p>
            </div>
          </label>
        </div>
      </section>

      {/* ---- step 3: name ----------------------------------------------- */}
      <section>
        <StepHeading n={3}>Name your app</StepHeading>

        <div className="flex flex-wrap items-end gap-4">
          <div className="grid gap-2">
            <Label htmlFor="app-name">App name</Label>
            <Input
              id="app-name"
              name="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="TTS API"
              autoComplete="off"
              maxLength={128}
              className="w-72"
              required
            />
            <p className="text-xs text-muted-foreground">
              {slug ? (
                <>
                  Identifier <span className="font-mono text-foreground">{slug}</span> — used in
                  URLs and monitor names.
                </>
              ) : (
                "One app per service."
              )}
            </p>
          </div>

          <Button type="submit" disabled={pending || !slug || !platform} className="mb-6">
            {pending && <Loader2 className="h-4 w-4 animate-spin" />}
            Create app
          </Button>
        </div>

        {!platform && name.length > 0 && (
          <p className="text-sm text-muted-foreground">Pick a platform in step 1 to continue.</p>
        )}
        {state.error && <p className="mt-2 text-sm text-status-down">{state.error}</p>}
      </section>
    </form>
  );
}
