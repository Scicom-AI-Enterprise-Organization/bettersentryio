"use server";

import { revalidatePath } from "next/cache";
import { requireUser } from "@/lib/rbac";
import { archiveIssue, deleteIssue, resolveIssue, setIssuePriority } from "@/lib/bsio";

export type BulkResult = { ok: boolean; message: string };

async function forEachIssue(
  ids: number[],
  fn: (id: number) => Promise<{ ok: boolean; error?: string }>,
  did: string,
): Promise<BulkResult> {
  await requireUser();
  let failed = 0;
  for (const id of ids) {
    const r = await fn(id);
    if (!r.ok) failed++;
  }
  revalidatePath("/apps/[slug]/issues/[view]", "page");
  if (failed > 0) return { ok: false, message: `${did} ${ids.length - failed}, failed ${failed}.` };
  return { ok: true, message: `${did} ${ids.length} issue${ids.length === 1 ? "" : "s"}.` };
}

export async function bulkResolve(ids: number[], resolved: boolean): Promise<BulkResult> {
  return forEachIssue(ids, (id) => resolveIssue(id, resolved), resolved ? "Resolved" : "Unresolved");
}

export async function bulkArchive(
  ids: number[],
  mode: "forever" | "for" | "recur" | "off",
  hours?: number,
): Promise<BulkResult> {
  return forEachIssue(ids, (id) => archiveIssue(id, mode, hours), mode === "off" ? "Unarchived" : "Archived");
}

export async function bulkPriority(ids: number[], priority: string): Promise<BulkResult> {
  return forEachIssue(ids, (id) => setIssuePriority(id, priority), "Prioritized");
}

export async function bulkDelete(ids: number[]): Promise<BulkResult> {
  return forEachIssue(ids, (id) => deleteIssue(id), "Deleted");
}
