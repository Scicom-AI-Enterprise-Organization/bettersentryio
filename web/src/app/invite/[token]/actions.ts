"use server";

import bcrypt from "bcryptjs";
import { isRedirectError } from "next/dist/client/components/redirect-error";
import { signIn } from "@/lib/auth";
import { prisma } from "@/lib/db";

export type SignupState = { ok: boolean; message: string } | null;

/**
 * The half the template never had for credentials auth: an invitee has no
 * account, so the invite page must CREATE one — the old flow bounced them to
 * a login they could only fail. Validates the token again server-side; the
 * invite's email binding is enforced, the role granted, the invite consumed,
 * and the new user signed straight in.
 */
export async function acceptInviteSignup(
  _prev: SignupState,
  formData: FormData,
): Promise<SignupState> {
  const token = String(formData.get("token") ?? "");
  const name = String(formData.get("name") ?? "").trim();
  const email = String(formData.get("email") ?? "").trim().toLowerCase();
  const password = String(formData.get("password") ?? "");

  if (!name) return { ok: false, message: "Name is required." };
  if (!/.+@.+\..+/.test(email)) return { ok: false, message: "A valid email is required." };
  if (password.length < 8) return { ok: false, message: "Password must be at least 8 characters." };

  const invite = await prisma.invitation.findUnique({ where: { token }, include: { role: true } });
  if (!invite) return { ok: false, message: "This invitation is not recognized." };
  if (invite.revokedAt) return { ok: false, message: "This invitation was revoked." };
  if (invite.acceptedAt) return { ok: false, message: "This invitation has already been used." };
  if (invite.expiresAt && invite.expiresAt < new Date()) {
    return { ok: false, message: "This invitation has expired — ask for a new link." };
  }
  if (invite.email && invite.email.toLowerCase() !== email) {
    return { ok: false, message: `This invite is bound to ${invite.email}.` };
  }

  const existing = await prisma.user.findUnique({ where: { email } });
  if (existing) {
    return { ok: false, message: "An account with this email already exists — sign in instead." };
  }

  const passwordHash = await bcrypt.hash(password, 10);
  await prisma.$transaction(async (tx) => {
    const user = await tx.user.create({
      data: { email, name, passwordHash, emailVerified: new Date() },
    });
    if (invite.roleId) {
      await tx.userRole.create({ data: { userId: user.id, roleId: invite.roleId } });
    }
    await tx.invitation.update({
      where: { id: invite.id },
      data: { acceptedAt: new Date(), acceptedById: user.id },
    });
  });

  try {
    await signIn("credentials", { email, password, redirectTo: "/dashboard" });
  } catch (err) {
    if (isRedirectError(err)) throw err; // the success path: signIn redirects
    return { ok: true, message: "Account created — sign in with your new password." };
  }
  return { ok: true, message: "Account created." };
}
