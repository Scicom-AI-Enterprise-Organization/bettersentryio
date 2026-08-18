"use server";

import { revalidatePath } from "next/cache";
import { requireUser } from "@/lib/rbac";
import { createChannel, deleteChannel, updateChannel } from "@/lib/bsio";

export type ActionState = { ok: boolean; message: string } | null;

export async function addChannel(_prev: ActionState, formData: FormData): Promise<ActionState> {
  await requireUser();
  const name = String(formData.get("name") ?? "").trim();
  const type = String(formData.get("type") ?? "teams");
  const url = String(formData.get("url") ?? "").trim();
  const result = await createChannel(name, type, url);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/alerts");
  return { ok: true, message: `Added "${name}" — alerts flow to it from the next issue.` };
}

export async function editChannel(_prev: ActionState, formData: FormData): Promise<ActionState> {
  await requireUser();
  const id = Number(formData.get("id"));
  const name = String(formData.get("name") ?? "").trim();
  const url = String(formData.get("url") ?? "").trim();
  const patch: { name?: string; url?: string } = {};
  if (name) patch.name = name;
  if (url) patch.url = url; // leave blank to keep the stored URL
  const result = await updateChannel(id, patch);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/alerts");
  return { ok: true, message: "Saved." };
}

export async function toggleChannel(id: number, enabled: boolean): Promise<ActionState> {
  await requireUser();
  const result = await updateChannel(id, { enabled });
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/alerts");
  return { ok: true, message: enabled ? "Enabled." : "Disabled." };
}

export async function removeChannel(id: number): Promise<ActionState> {
  await requireUser();
  const result = await deleteChannel(id);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/alerts");
  return { ok: true, message: "Deleted." };
}
