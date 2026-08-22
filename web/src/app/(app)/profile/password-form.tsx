"use client";

import { useRef, useState, useTransition } from "react";
import { toast } from "sonner";
import { Eye, EyeOff } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/bsio/field";
import { updatePassword } from "./actions";

const MIN_LENGTH = 8;

export function PasswordForm({ hasPassword }: { hasPassword: boolean }) {
  const formRef = useRef<HTMLFormElement>(null);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [reveal, setReveal] = useState(false);
  const [pending, start] = useTransition();

  // Checked as you type. Learning that the two fields disagree from a toast, after a
  // round trip, is the thing that makes a password form feel hostile.
  const tooShort = next.length > 0 && next.length < MIN_LENGTH;
  const mismatch = confirm.length > 0 && confirm !== next;
  const ready =
    next.length >= MIN_LENGTH && confirm === next && (!hasPassword || current.length > 0);

  function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const formData = new FormData(e.currentTarget);
    start(async () => {
      const res = await updatePassword(formData);
      if (res.error) {
        toast.error(res.error);
        return;
      }
      toast.success(hasPassword ? "Password updated" : "Password set");
      formRef.current?.reset();
      setCurrent("");
      setNext("");
      setConfirm("");
      setReveal(false);
    });
  }

  return (
    <form ref={formRef} className="space-y-5" onSubmit={onSubmit}>
      <div className="grid gap-x-4 gap-y-5 sm:max-w-3xl sm:grid-cols-2">
        {hasPassword && (
          <Field
            className="sm:col-span-2 sm:max-w-sm"
            label="Current password"
            htmlFor="currentPassword"
            hint="Proves it is you before the password changes."
          >
            <Input
              id="currentPassword"
              name="currentPassword"
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              required
            />
          </Field>
        )}

        <Field
          label={hasPassword ? "New password" : "Password"}
          htmlFor="newPassword"
          hint={`At least ${MIN_LENGTH} characters.`}
          error={tooShort ? `${MIN_LENGTH - next.length} more character${next.length === MIN_LENGTH - 1 ? "" : "s"} needed.` : null}
        >
          <Input
            id="newPassword"
            name="newPassword"
            type={reveal ? "text" : "password"}
            autoComplete="new-password"
            minLength={MIN_LENGTH}
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </Field>

        <Field
          label="Confirm password"
          htmlFor="confirmPassword"
          hint="Type it again."
          error={mismatch ? "These do not match." : null}
        >
          <Input
            id="confirmPassword"
            name="confirmPassword"
            type={reveal ? "text" : "password"}
            autoComplete="new-password"
            minLength={MIN_LENGTH}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            aria-invalid={mismatch || undefined}
            required
          />
        </Field>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button type="submit" disabled={pending || !ready}>
          {pending ? "Saving…" : hasPassword ? "Change password" : "Set password"}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setReveal((r) => !r)}
          disabled={!next && !confirm}
        >
          {reveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
          {reveal ? "Hide" : "Show"}
        </Button>
        {!ready && (
          <p className="text-xs text-muted-foreground">
            {hasPassword && !current
              ? "Enter your current password to continue."
              : next.length < MIN_LENGTH
                ? `A new password of at least ${MIN_LENGTH} characters.`
                : "Confirm it to continue."}
          </p>
        )}
      </div>
    </form>
  );
}
