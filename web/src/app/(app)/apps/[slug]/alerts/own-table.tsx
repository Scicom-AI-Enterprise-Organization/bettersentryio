"use client";

import { useActionState, useEffect, useState, useTransition } from "react";
import { Pencil, Trash2, X } from "lucide-react";

import type { Channel } from "@/lib/bsio";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import {
  addProjectChannel,
  editProjectChannel,
  removeProjectChannel,
  testWebhook,
  toggleProjectChannel,
  type ActionState,
} from "./actions";
import { CHANNEL_TYPE_LABEL as TYPE_LABEL, ChannelAddForm } from "@/components/bsio/channel-add-form";
import { ConfirmDialog } from "@/components/bsio/confirm-dialog";

/**
 * Channels this project owns outright — the team chat that only wants to hear about
 * this service. They need no import row: a project's own channel routes to that
 * project by construction, and deleting the project takes them with it.
 */
export function OwnTable({ slug, channels }: { slug: string; channels: Channel[] }) {
  const [editing, setEditing] = useState<number | null>(null);
  const [notice, setNotice] = useState<ActionState>(null);
  const [pending, startTransition] = useTransition();
  const [deleting, setDeleting] = useState<Channel | null>(null);

  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-[15px] font-semibold tracking-tight">This app&apos;s own channels</h2>
        <p className="mt-1.5 text-[13px] text-muted-foreground">
          Webhooks that exist only for this app. Nothing else routes to them and no other app
          can see them.
        </p>
      </div>

      {channels.length === 0 ? (
        <p className="rounded-xl border border-dashed border-border p-4 text-[13px] text-muted-foreground">
          None yet. Add one below, or import a shared webhook from the catalogue above.
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
                  <EditRow key={c.id} slug={slug} channel={c} done={() => setEditing(null)} />
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
                          startTransition(async () =>
                            setNotice(await toggleProjectChannel(slug, c.id, !c.enabled)),
                          )
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
                          onClick={() => setDeleting(c)}
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
        <p className={`text-[13px] ${notice.ok ? "text-status-active" : "text-status-down"}`}>
          {notice.message}
        </p>
      )}

      <ChannelAddForm
        title="Add a webhook for this app"
        addAction={addProjectChannel}
        testAction={testWebhook}
        slug={slug}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting ? `Delete "${deleting.name}"?` : "Delete webhook?"}
        description="This app stops alerting to it immediately. Nothing else routes to it, so nobody else is affected."
        confirmLabel="Delete webhook"
        destructive
        pending={pending}
        onConfirm={() => {
          const target = deleting;
          if (!target) return;
          startTransition(async () => {
            setNotice(await removeProjectChannel(slug, target.id));
            setDeleting(null);
          });
        }}
      />
    </section>
  );
}

function EditRow({
  slug,
  channel,
  done,
}: {
  slug: string;
  channel: Channel;
  done: () => void;
}) {
  const [state, action, pending] = useActionState<ActionState, FormData>(
    editProjectChannel,
    null,
  );
  useEffect(() => {
    if (state?.ok) done();
  }, [state, done]);
  return (
    <TableRow>
      <TableCell colSpan={5}>
        <form action={action} className="flex flex-wrap items-center gap-2">
          <input type="hidden" name="slug" value={slug} />
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
