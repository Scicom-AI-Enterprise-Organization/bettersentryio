"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";

import { Button } from "@/components/ui/button";
import { actArchive, actDelete, actPriority, actResolve, type ActResult } from "./actions";

export function IssueActions({
  id,
  slug,
  resolved,
  archived,
  priority,
}: {
  id: number;
  slug: string;
  resolved: boolean;
  archived: boolean;
  priority: string;
}) {
  const router = useRouter();
  const [notice, setNotice] = useState<ActResult | null>(null);
  const [pending, startTransition] = useTransition();

  const run = (fn: () => Promise<ActResult>) =>
    startTransition(async () => {
      setNotice(await fn());
      router.refresh();
    });

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" disabled={pending} onClick={() => run(() => actResolve(id, !resolved))}>
        {resolved ? "Unresolve" : "Resolve"}
      </Button>
      <select
        disabled={pending}
        value=""
        onChange={(e) => {
          const v = e.target.value;
          if (v === "forever") run(() => actArchive(id, "forever"));
          else if (v === "1d") run(() => actArchive(id, "for", 24));
          else if (v === "1w") run(() => actArchive(id, "for", 168));
          else if (v === "recur") run(() => actArchive(id, "recur"));
          else if (v === "off") run(() => actArchive(id, "off"));
        }}
        className="h-8 rounded-md border border-input bg-transparent px-2 text-sm"
      >
        <option value="">{archived ? "Archived…" : "Archive…"}</option>
        {!archived && <option value="forever">Forever</option>}
        {!archived && <option value="1d">For 1 day</option>}
        {!archived && <option value="1w">For 1 week</option>}
        {!archived && <option value="recur">Until it occurs again</option>}
        {archived && <option value="off">Unarchive</option>}
      </select>
      <select
        disabled={pending}
        value={priority}
        onChange={(e) => run(() => actPriority(id, e.target.value))}
        className="h-8 rounded-md border border-input bg-transparent px-2 text-sm"
      >
        <option value="">priority: none</option>
        <option value="high">priority: high</option>
        <option value="med">priority: med</option>
        <option value="low">priority: low</option>
      </select>
      <Button
        size="sm"
        variant="ghost"
        className="text-status-down hover:text-status-down"
        disabled={pending}
        onClick={() => {
          if (!window.confirm("Delete this issue and all its events? This cannot be undone.")) return;
          run(() => actDelete(id, slug));
        }}
      >
        Delete
      </Button>
      {pending && <span className="text-xs text-muted-foreground">working…</span>}
      {notice && (
        <span className={`text-sm ${notice.ok ? "text-status-active" : "text-status-down"}`}>
          {notice.message}
        </span>
      )}
    </div>
  );
}
