"use server";

import { revalidatePath } from "next/cache";

import { requireUser } from "@/lib/rbac";
import {
  createProjectChannel,
  deleteProjectChannel,
  importChannels,
  setAlertPatience,
  testChannel,
  unimportChannel,
  updateProjectChannel,
} from "@/lib/bsio";

export type ActionState = { ok: boolean; message: string } | null;

/** Every mutation here changes what the engine will send, so the page is always
 *  re-read from the engine rather than trusting a local guess. */
function done(slug: string) {
  revalidatePath(`/apps/${slug}/alerts`);
}

export async function addProjectChannel(
  _prev: ActionState,
  formData: FormData,
): Promise<ActionState> {
  await requireUser();
  const slug = String(formData.get("slug") ?? "");
  const name = String(formData.get("name") ?? "").trim();
  const type = String(formData.get("type") ?? "teams");
  const url = String(formData.get("url") ?? "").trim();
  const result = await createProjectChannel(slug, name, type, url);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: `Added "${name}" — it alerts on this app only.` };
}

export async function editProjectChannel(
  _prev: ActionState,
  formData: FormData,
): Promise<ActionState> {
  await requireUser();
  const slug = String(formData.get("slug") ?? "");
  const id = Number(formData.get("id"));
  const name = String(formData.get("name") ?? "").trim();
  const url = String(formData.get("url") ?? "").trim();
  const patch: { name?: string; url?: string } = {};
  if (name) patch.name = name;
  if (url) patch.url = url; // leave blank to keep the stored URL
  const result = await updateProjectChannel(slug, id, patch);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: "Saved." };
}

export async function toggleProjectChannel(
  slug: string,
  id: number,
  enabled: boolean,
): Promise<ActionState> {
  await requireUser();
  const result = await updateProjectChannel(slug, id, { enabled });
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: enabled ? "Enabled." : "Disabled." };
}

export async function removeProjectChannel(slug: string, id: number): Promise<ActionState> {
  await requireUser();
  const result = await deleteProjectChannel(slug, id);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: "Deleted." };
}

/** Importing routes this app's alerts to a shared definition; un-importing stops
 *  that without touching the definition, which other apps may still use. */
export async function setImported(
  slug: string,
  id: number,
  imported: boolean,
): Promise<ActionState> {
  await requireUser();
  const result = imported
    ? await importChannels(slug, [id])
    : await unimportChannel(slug, id);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return {
    ok: true,
    message: imported ? "Imported — this app now alerts to it." : "Removed from this app.",
  };
}

export async function importAll(slug: string, ids: number[]): Promise<ActionState> {
  await requireUser();
  if (ids.length === 0) return { ok: false, message: "Nothing left to import." };
  const result = await importChannels(slug, ids);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: `Imported ${ids.length} channel${ids.length === 1 ? "" : "s"}.` };
}

export async function changePatience(slug: string, seconds: number): Promise<ActionState> {
  await requireUser();
  const result = await setAlertPatience(slug, seconds);
  if (!result.ok) return { ok: false, message: result.error };
  done(slug);
  return { ok: true, message: "Patience updated." };
}

/** Same probe as the global form: a card has to arrive before a channel can be saved. */
export async function testWebhook(type: string, url: string): Promise<ActionState> {
  await requireUser();
  const result = await testChannel(type, url);
  if (!result.ok) return { ok: false, message: result.error };
  return { ok: true, message: "Test card delivered — check the channel, then add it." };
}
