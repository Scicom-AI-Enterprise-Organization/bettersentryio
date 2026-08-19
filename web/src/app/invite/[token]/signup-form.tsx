"use client";

import { useActionState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { acceptInviteSignup, type SignupState } from "./actions";

export function InviteSignupForm({
  token,
  email,
  roleName,
}: {
  token: string;
  email: string | null;
  roleName: string | null;
}) {
  const [state, action, pending] = useActionState<SignupState, FormData>(acceptInviteSignup, null);

  return (
    <form action={action} className="mt-6 w-full max-w-sm space-y-3 text-left">
      <input type="hidden" name="token" value={token} />
      <Input name="name" placeholder="Your name" required autoComplete="name" />
      <Input
        name="email"
        type="email"
        placeholder="you@scicom.com.my"
        defaultValue={email ?? ""}
        readOnly={!!email}
        className={email ? "bg-muted/50 text-muted-foreground" : ""}
        required
        autoComplete="email"
      />
      <Input
        name="password"
        type="password"
        placeholder="Password (min 8 characters)"
        required
        minLength={8}
        autoComplete="new-password"
      />
      <Button type="submit" className="w-full" disabled={pending}>
        {pending ? "Creating account…" : `Create account${roleName ? ` · ${roleName}` : ""}`}
      </Button>
      {state && !state.ok && <p className="text-sm text-status-down">{state.message}</p>}
      {state?.ok && <p className="text-sm text-status-active">{state.message}</p>}
    </form>
  );
}
