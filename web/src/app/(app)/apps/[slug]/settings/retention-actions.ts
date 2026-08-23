"use server";

import { revalidatePath } from "next/cache";

import { requireUser } from "@/lib/rbac";
import { setAppRetention } from "@/lib/bsio";

export type RetentionState = { ok: boolean; message: string } | null;

export async function changeRetention(slug: string, days: number): Promise<RetentionState> {
  await requireUser();
  if (!Number.isInteger(days) || days < 0 || days > 3650) {
    return { ok: false, message: "Retention must be between 0 (keep forever) and 3650 days." };
  }
  const result = await setAppRetention(slug, days);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath(`/apps/${slug}/settings`);
  return {
    ok: true,
    message:
      days === 0
        ? "Retention off — events are kept forever."
        : `Events older than ${days} days are now removed by the hourly sweep.`,
  };
}
