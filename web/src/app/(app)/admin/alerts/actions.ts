"use server";

import { revalidatePath } from "next/cache";
import { requireUser } from "@/lib/rbac";
import { setTeamsAlert } from "@/lib/bsio";

export type SaveState = { ok: boolean; message: string } | null;

/**
 * Saves (or, with an empty field, disables) the Teams incoming webhook. The
 * engine stores it as the named "teams" alert channel; every new error issue
 * and every monitor incident posts a card to it.
 */
export async function saveTeamsWebhook(_prev: SaveState, formData: FormData): Promise<SaveState> {
  await requireUser();
  const url = String(formData.get("url") ?? "").trim();
  const result = await setTeamsAlert(url);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/alerts");
  return {
    ok: true,
    message: result.data.configured ? "Saved — alerts flow to Teams." : "Teams alerts disabled.",
  };
}
