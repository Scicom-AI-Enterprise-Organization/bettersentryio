"use client";

import { useActionState, useEffect, useState, useTransition } from "react";
import { Pencil, Plus, Trash2, X } from "lucide-react";

import type { Channel } from "@/lib/bsio";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectBox } from "@/components/bsio/select-box";
import { StatusPill } from "@/components/ui/status-pill";
import {
  Table,
  TableBody,
  TableCard,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { addChannel, editChannel, removeChannel, toggleChannel, type ActionState } from "./actions";

const TYPE_LABEL: Record<string, string> = {
  teams: "Teams",
  slack: "Slack",
  webhook: "Generic webhook",
};

export function ChannelsTable({ channels }: { channels: Channel[] }) {
  const [editing, setEditing] = useState<number | null>(null);
  const [notice, setNotice] = useState<ActionState>(null);
  const [pending, startTransition] = useTransition();

  return (
    <div className="space-y-6">
      {channels.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No webhooks yet — add one below and alerts start flowing on the next new issue.
        </p>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Webhook URL</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {channels.map((c) =>
                editing === c.id ? (
                  <EditRow key={c.id} channel={c} done={() => setEditing(null)} />
                ) : (
                  <TableRow key={c.id}>
                    <TableCell className="text-sm font-medium">{c.name}</TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {TYPE_LABEL[c.type] ?? c.type}
                    </TableCell>
                    <TableCell className="max-w-md">
                      <span className="block truncate font-mono text-xs text-muted-foreground">
                        {c.url_masked}
                      </span>
                    </TableCell>
                    <TableCell>
                      <button
                        type="button"
                        title={c.enabled ? "Click to disable" : "Click to enable"}
                        disabled={pending}
                        onClick={() =>
                          startTransition(async () => setNotice(await toggleChannel(c.id, !c.enabled)))
                        }
                      >
                        {c.enabled ? (
                          <StatusPill tone="active">on</StatusPill>
                        ) : (
                          <StatusPill tone="muted">off</StatusPill>
                        )}
                      </button>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button variant="ghost" size="sm" onClick={() => setEditing(c.id)}>
                          <Pencil className="h-3.5 w-3.5" />
                          Edit
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-status-down hover:text-status-down"
                          disabled={pending}
                          onClick={() => {
                            if (!window.confirm(`Delete webhook "${c.name}"? Alerts stop immediately.`)) return;
                            startTransition(async () => setNotice(await removeChannel(c.id)));
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          Delete
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ),
              )}
            </TableBody>
          </Table>
        </TableCard>
      )}

      {notice && (
        <p className={notice.ok ? "text-sm text-status-active" : "text-sm text-status-down"}>
          {notice.message}
        </p>
      )}

      <AddForm />
    </div>
  );
}

function EditRow({ channel, done }: { channel: Channel; done: () => void }) {
  const [state, action, pending] = useActionState<ActionState, FormData>(editChannel, null);
  useEffect(() => {
    if (state?.ok) done();
  }, [state, done]);
  return (
    <TableRow>
      <TableCell colSpan={5}>
        <form action={action} className="flex flex-wrap items-center gap-2">
          <input type="hidden" name="id" value={channel.id} />
          <Input name="name" defaultValue={channel.name} className="w-44 text-sm" required />
          <Input
            name="url"
            type="url"
            placeholder={`${channel.url_masked} (blank = keep current URL)`}
            className="min-w-72 flex-1 font-mono text-xs"
            autoComplete="off"
          />
          <Button type="submit" size="sm" disabled={pending}>
            {pending ? "Saving…" : "Save"}
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={done}>
            <X className="h-3.5 w-3.5" />
            Cancel
          </Button>
          {state && !state.ok && <span className="text-sm text-status-down">{state.message}</span>}
        </form>
      </TableCell>
    </TableRow>
  );
}

function AddForm() {
  const [state, action, pending] = useActionState<ActionState, FormData>(addChannel, null);
  return (
    <div className="rounded-xl border border-dashed border-border p-4">
      <p className="mb-3 text-sm font-medium">Add a webhook</p>
      <form action={action} className="flex flex-wrap items-center gap-2">
        <Input name="name" placeholder="name, e.g. sre-team-chat" className="w-52 text-sm" required />
        <SelectBox name="type" defaultValue="teams">
          <option value="teams">Teams</option>
          <option value="slack">Slack</option>
          <option value="webhook">Generic webhook</option>
        </SelectBox>
        <Input
          name="url"
          type="url"
          placeholder="https://….powerplatform.com/…/triggers/manual/paths/invoke?…"
          className="min-w-72 flex-1 font-mono text-xs"
          autoComplete="off"
          required
        />
        <Button type="submit" size="sm" disabled={pending}>
          <Plus className="h-3.5 w-3.5" />
          {pending ? "Adding…" : "Add"}
        </Button>
      </form>
      {state && (
        <p className={`mt-2 text-sm ${state.ok ? "text-status-active" : "text-status-down"}`}>
          {state.message}
        </p>
      )}
    </div>
  );
}
