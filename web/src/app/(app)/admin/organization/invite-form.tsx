"use client";

import { useState, useTransition } from "react";
import { toast } from "sonner";
import { Check, Copy, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectBox } from "@/components/bsio/select-box";
import { Field } from "@/components/bsio/field";
import { createInvitation } from "./actions";

const NO_ROLE = "__none__";
const NEVER = "never";

/** Presets rather than a spinner: nobody wants to arrow up to 90. */
const EXPIRY_CHOICES: { value: string; label: string }[] = [
  { value: "1", label: "1 day" },
  { value: "7", label: "7 days" },
  { value: "14", label: "14 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: NEVER, label: "Never expires" },
];

export function InviteForm({ roles, baseUrl }: { roles: string[]; baseUrl: string }) {
  const [email, setEmail] = useState("");
  const [roleName, setRoleName] = useState<string>(NO_ROLE);
  const [expiry, setExpiry] = useState("7");
  const [lastLink, setLastLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [pending, start] = useTransition();

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    start(async () => {
      const res = await createInvitation({
        email: email || undefined,
        roleName: roleName === NO_ROLE ? undefined : roleName,
        // Omitted means no expiry at all, which is what the action stores as null.
        expiresInDays: expiry === NEVER ? undefined : Number(expiry),
      });
      if ("error" in res && res.error) {
        toast.error(res.error);
        return;
      }
      if (res.ok && res.token) {
        const link = `${baseUrl}/invite/${res.token}`;
        setLastLink(link);
        const ok = await navigator.clipboard.writeText(link).then(
          () => true,
          () => false,
        );
        setCopied(ok);
        toast.success(ok ? "Invite created — link copied" : "Invite created — copy the link below");
        setEmail("");
      }
    });
  }

  function copy() {
    if (!lastLink) return;
    navigator.clipboard.writeText(lastLink).then(
      () => {
        setCopied(true);
        toast.success("Copied");
      },
      () => toast.error("Could not reach the clipboard — select the link and copy it"),
    );
  }

  return (
    <form onSubmit={onSubmit} className="space-y-5">
      {/* Email carries the most text, so it gets half the row; the two menus split the
          rest. Equal thirds gave a number field the width of an address. */}
      <div className="grid gap-x-4 gap-y-5 sm:grid-cols-4">
        <Field
          className="sm:col-span-2"
          label="Email"
          htmlFor="invite-email"
          hint="Optional. Blank means anyone holding the link can sign up with it."
        >
          <Input
            id="invite-email"
            type="email"
            autoComplete="off"
            placeholder="person@company.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>

        <Field label="Role" hint="Granted the moment they accept.">
          <SelectBox
            aria-label="Role"
            className="w-full"
            value={roleName}
            onValueChange={setRoleName}
          >
            <option value={NO_ROLE}>No role</option>
            {roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </SelectBox>
        </Field>

        <Field label="Expires" hint="Counted from now, not from first use.">
          <SelectBox
            aria-label="Expires in"
            className="w-full"
            value={expiry}
            onValueChange={setExpiry}
          >
            {EXPIRY_CHOICES.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </SelectBox>
        </Field>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button type="submit" disabled={pending}>
          <Plus className="h-4 w-4" />
          {pending ? "Creating…" : "Create invite link"}
        </Button>
        <p className="text-xs text-muted-foreground">
          The link goes to your clipboard as soon as it exists — nothing is emailed.
        </p>
      </div>

      {lastLink && (
        <div className="rounded-lg border border-status-active/30 bg-status-active/[0.04] p-3">
          <p className="flex items-center gap-1.5 text-xs font-medium">
            {copied && <Check className="h-3.5 w-3.5 text-status-active" />}
            Invite link{copied ? " — copied to your clipboard" : ""}
          </p>
          <div className="mt-2 flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1.5 font-mono text-xs">
              {lastLink}
            </code>
            <Button type="button" variant="outline" size="sm" onClick={copy}>
              <Copy className="h-3.5 w-3.5" />
              Copy
            </Button>
          </div>
        </div>
      )}
    </form>
  );
}
