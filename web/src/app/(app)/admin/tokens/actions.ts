"use server";

import { revalidatePath } from "next/cache";

import { requireUser } from "@/lib/rbac";
import { createApiToken, revokeApiToken } from "@/lib/bsio";

/**
 * `secret` is present exactly once, on the response that minted it. It travels through
 * the action's return value to be displayed and is never stored anywhere the UI can
 * read again — the engine keeps only a hash, so nothing here can show it twice.
 */
export type TokenActionState =
  | { ok: true; message: string; secret?: string }
  | { ok: false; message: string }
  | null;

export async function mintToken(
  _prev: TokenActionState,
  formData: FormData,
): Promise<TokenActionState> {
  await requireUser();
  const name = String(formData.get("name") ?? "").trim();
  if (!name) return { ok: false, message: "Give the token a name — you will be revoking it by that name later." };

  const result = await createApiToken(name);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/tokens");
  return {
    ok: true,
    message: `Created "${name}". Copy it now — it is not shown again.`,
    secret: result.data.secret,
  };
}

export async function revokeToken(id: number): Promise<TokenActionState> {
  await requireUser();
  const result = await revokeApiToken(id);
  if (!result.ok) return { ok: false, message: result.error };
  revalidatePath("/admin/tokens");
  return { ok: true, message: "Revoked. Anything using it starts getting 401s immediately." };
}
