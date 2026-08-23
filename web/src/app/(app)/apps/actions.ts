"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { requireUser } from "@/lib/rbac";
import { createApp, deleteApp } from "@/lib/bsio";

export type AddAppState = {
  error?: string;
  createdSlug?: string;
};

/**
 * Creates an app in the engine. Returns the slug rather than redirecting so the
 * dialog can hand the user straight to that app's setup instructions.
 */
export async function addApp(_prev: AddAppState, formData: FormData): Promise<AddAppState> {
  await requireUser();

  const name = String(formData.get("name") ?? "").trim();
  if (!name) return { error: "Give the app a name." };
  if (name.length > 128) return { error: "Name must be 128 characters or fewer." };

  const platform = String(formData.get("platform") ?? "").trim();
  const progress = formData.get("progress") ? "1" : "0";

  const result = await createApp(name, platform);
  if (!result.ok) return { error: result.error };

  revalidatePath("/apps");
  // redirect() throws, so it must be outside the try/catch shape above; the query
  // carries the setup page's initial state rather than persisting a UI preference.
  redirect(`/apps/${result.data.slug}/settings?created=1&progress=${progress}`);
}

export type DeleteAppState = { error?: string };

/**
 * Deletes an app and everything under it, then returns to the list.
 *
 * Destructive and not undoable: monitors, their beat history and their incidents go
 * with the app. The dialog states what will be lost before this runs.
 */
export async function removeApp(_prev: DeleteAppState, formData: FormData): Promise<DeleteAppState> {
  await requireUser();

  const slug = String(formData.get("slug") ?? "").trim();
  if (!slug) return { error: "Missing app." };

  const result = await deleteApp(slug);
  if (!result.ok) return { error: result.error };

  revalidatePath("/apps");
  revalidatePath("/monitors");
  redirect("/apps");
}
