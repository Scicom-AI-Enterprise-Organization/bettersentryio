"use client";

import { useActionState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { saveTeamsWebhook, type SaveState } from "./actions";

export function WebhookForm({ placeholder }: { placeholder: string }) {
  const [state, action, pending] = useActionState<SaveState, FormData>(saveTeamsWebhook, null);

  return (
    <form action={action} className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <Input
          name="url"
          type="url"
          placeholder={placeholder || "https://…webhook.office.com/webhookb2/…"}
          className="max-w-xl font-mono text-xs"
          autoComplete="off"
        />
        <Button type="submit" size="sm" disabled={pending}>
          {pending ? "Saving…" : "Save"}
        </Button>
      </div>
      {state && (
        <p className={state.ok ? "text-sm text-status-active" : "text-sm text-status-down"}>
          {state.message}
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        Leave the field empty and save to turn alerts off. The stored URL is kept, just disabled.
      </p>
    </form>
  );
}
