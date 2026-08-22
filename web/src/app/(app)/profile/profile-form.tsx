"use client";

import { useState, useTransition } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/bsio/field";
import { updateProfile } from "./actions";

export function ProfileForm({
  initialName,
  email,
}: {
  initialName: string;
  email: string;
}) {
  const [name, setName] = useState(initialName);
  const [pending, start] = useTransition();

  const dirty = name.trim() !== initialName.trim();

  function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const formData = new FormData(e.currentTarget);
    start(async () => {
      const res = await updateProfile(formData);
      if (res.error) toast.error(res.error);
      else toast.success("Profile updated");
    });
  }

  return (
    <form className="space-y-5" onSubmit={onSubmit}>
      {/* Two columns rather than a stack: a full-width page would otherwise give a
          name field the width of the window. */}
      <div className="grid gap-x-4 gap-y-5 sm:max-w-3xl sm:grid-cols-2">
        <Field
          label="Email"
          htmlFor="email"
          hint="Set by your sign-in method — change it there, not here."
        >
          <Input id="email" value={email} disabled className="font-mono text-sm" />
        </Field>
        <Field label="Name" htmlFor="name" hint="How you appear elsewhere in the console.">
          <Input
            id="name"
            name="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="your name"
            maxLength={100}
            autoComplete="name"
          />
        </Field>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button type="submit" disabled={pending || !dirty}>
          {pending ? "Saving…" : "Save"}
        </Button>
        {!dirty && <p className="text-xs text-muted-foreground">Nothing to save yet.</p>}
      </div>
    </form>
  );
}
