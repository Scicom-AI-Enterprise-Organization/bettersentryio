"use client";

import { useActionState, useState, useTransition } from "react";
import { Check, Copy, KeyRound, Plus, Trash2 } from "lucide-react";

import type { ApiToken } from "@/lib/bsio";
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
import { Ago, StampAt } from "@/components/bsio/time";
import { ConfirmDialog } from "@/components/bsio/confirm-dialog";
import { mintToken, revokeToken, type TokenActionState } from "./actions";

export function TokensTable({ tokens }: { tokens: ApiToken[] }) {
  // The mint action's state lives here, not in the form, so the freshly minted token
  // renders above the table instead of inside a form that is about to be typed in
  // again — and so nothing has to lift state during a render to get it here.
  const [mint, mintAction, minting] = useActionState<TokenActionState, FormData>(mintToken, null);
  const [revoked, setRevoked] = useState<TokenActionState>(null);
  // Which token the confirm dialog is asking about. One dialog for the table, pointed
  // at a row, rather than one mounted per row.
  const [confirming, setConfirming] = useState<ApiToken | null>(null);
  const [pending, startTransition] = useTransition();

  return (
    <div className="space-y-6">
      <form
        action={mintAction}
        onSubmit={() => setRevoked(null)}
        className="flex flex-wrap items-center gap-2"
      >
        <Input
          name="name"
          placeholder="What will hold it — grafana, oncall-dashboard, …"
          className="min-w-72 flex-1"
          maxLength={128}
          autoComplete="off"
          required
        />
        <Button type="submit" size="sm" disabled={minting}>
          <Plus className="h-3.5 w-3.5" />
          {minting ? "Creating…" : "Create token"}
        </Button>
      </form>

      {mint && !mint.ok && <p className="text-sm text-status-down">{mint.message}</p>}
      {mint?.ok && mint.secret && <Secret value={mint.secret} message={mint.message} />}
      {revoked && (
        <p className={revoked.ok ? "text-sm text-muted-foreground" : "text-sm text-status-down"}>
          {revoked.message}
        </p>
      )}

      {tokens.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border p-8">
          <div className="flex flex-wrap items-center gap-2">
            <KeyRound className="h-4 w-4 text-muted-foreground" />
            <p className="text-sm font-medium">No tokens yet.</p>
          </div>
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
            Until one exists, the only way to read the API is the operator token from the
            engine&apos;s environment — which can also delete apps, and cannot be revoked
            without a redeploy.
          </p>
        </div>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Token</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((t) => (
                <TableRow key={t.id} className={t.revoked_at ? "opacity-60" : undefined}>
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {t.name}
                      {t.revoked_at && <StatusPill tone="muted">revoked</StatusPill>}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {t.prefix}…
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                    <StampAt iso={t.created_at} />
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                    {/* Never used is worth saying plainly: it is the difference between
                        "safe to revoke" and "something out there breaks". */}
                    {t.last_used_at ? <Ago iso={t.last_used_at} /> : "never"}
                  </TableCell>
                  <TableCell className="text-right">
                    {!t.revoked_at && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-status-down hover:text-status-down"
                        disabled={pending}
                        onClick={() => setConfirming(t)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}

      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(open) => !open && setConfirming(null)}
        title={confirming ? `Revoke “${confirming.name}”?` : ""}
        description={
          <>
            Anything still using{" "}
            <code className="font-mono text-xs">{confirming?.prefix}…</code> starts getting
            401s immediately, and the token cannot be reinstated — you would create a new one.
            {confirming && !confirming.last_used_at && (
              <> This one has never been used, so nothing is relying on it.</>
            )}
          </>
        }
        confirmLabel="Revoke"
        destructive
        pending={pending}
        onConfirm={() => {
          const target = confirming;
          if (!target) return;
          startTransition(async () => {
            setRevoked(await revokeToken(target.id));
            setConfirming(null);
          });
        }}
      />
    </div>
  );
}

/** The one and only sighting of a token, with the copy button that makes that workable. */
function Secret({ value, message }: { value: string; message: string }) {
  const [copied, setCopied] = useState(false);

  return (
    <div className="rounded-xl border border-primary/40 bg-primary/5 p-4">
      <p className="text-sm font-medium">{message}</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded-md border border-border bg-card px-3 py-2 font-mono text-[13px]">
          {value}
        </code>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
              setCopied(true);
              setTimeout(() => setCopied(false), 2000);
            } catch {
              // Clipboard access can be refused (insecure origin, permissions). The
              // token is on screen and selectable, so this is a convenience failing,
              // not the operation.
              setCopied(false);
            }
          }}
        >
          {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        The engine stores only a hash of this, so it cannot be shown again — if it is lost,
        revoke it and create another.
      </p>
    </div>
  );
}
