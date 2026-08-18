"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { requireUser } from "@/lib/rbac";
import { archiveIssue, deleteIssue, resolveIssue, setIssuePriority } from "@/lib/bsio";

export type ActResult = { ok: boolean; message: string };

export async function actResolve(id: number, resolved: boolean): Promise<ActResult> {
  await requireUser();
  const r = await resolveIssue(id, resolved);
  revalidatePath(`/apps/[slug]/errors/[id]`, "page");
  return r.ok
    ? { ok: true, message: resolved ? "Resolved." : "Unresolved." }
    : { ok: false, message: r.error };
}

export async function actArchive(
  id: number,
  mode: "forever" | "for" | "recur" | "off",
  hours?: number,
): Promise<ActResult> {
  await requireUser();
  const r = await archiveIssue(id, mode, hours);
  revalidatePath(`/apps/[slug]/errors/[id]`, "page");
  return r.ok
    ? { ok: true, message: mode === "off" ? "Unarchived." : "Archived." }
    : { ok: false, message: r.error };
}

export async function actPriority(id: number, priority: string): Promise<ActResult> {
  await requireUser();
  const r = await setIssuePriority(id, priority);
  revalidatePath(`/apps/[slug]/errors/[id]`, "page");
  return r.ok ? { ok: true, message: "Priority set." } : { ok: false, message: r.error };
}

export async function actDelete(id: number, slug: string): Promise<ActResult> {
  await requireUser();
  const r = await deleteIssue(id);
  if (!r.ok) return { ok: false, message: r.error };
  redirect(`/apps/${slug}/issues/outages`);
}
