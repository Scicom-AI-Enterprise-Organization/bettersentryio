"use client";

import { useState, useTransition } from "react";
import { Download, Globe } from "lucide-react";

import type { Channel } from "@/lib/bsio";
import { Button } from "@/components/ui/button";
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
import { importAll, setImported, type ActionState } from "./actions";

const TYPE_LABEL: Record<string, string> = {
  teams: "Teams",
  slack: "Slack",
  webhook: "Generic webhook",
};

/**
 * The global catalogue, seen from inside a project.
 *
 * Importing is a reference, not a copy: the webhook URL keeps living in Settings →
 * Alerts, so rotating it there rotates it everywhere at once. That is the whole
 * reason to import rather than paste the same URL into every project.
 */
export function ImportedTable({ slug, globals }: { slug: string; globals: Channel[] }) {
  const [notice, setNotice] = useState<ActionState>(null);
  const [pending, startTransition] = useTransition();

  const unimported = globals.filter((c) => !c.imported);
  const importedCount = globals.length - unimported.length;

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-[15px] font-semibold tracking-tight">
            <Globe className="h-4 w-4 text-muted-foreground" />
            Imported from the global catalogue
          </h2>
          <p className="mt-1.5 text-[13px] text-muted-foreground">
            Shared definitions from Settings → Alerts. Imported rows receive this app&apos;s
            alerts; the URL stays in the catalogue, so changing it there changes it for every
            app that imported it.
          </p>
        </div>
        {unimported.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            disabled={pending}
            onClick={() =>
              startTransition(async () =>
                setNotice(await importAll(slug, unimported.map((c) => c.id))),
              )
            }
          >
            <Download className="h-3.5 w-3.5" />
            Import all {unimported.length}
          </Button>
        )}
      </div>

      {globals.length === 0 ? (
        <p className="rounded-xl border border-dashed border-border p-4 text-[13px] text-muted-foreground">
          The global catalogue is empty. An operator adds shared webhooks under Settings →
          Alerts; until then this app can still have channels of its own below.
        </p>
      ) : (
        <TableCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Webhook URL</TableHead>
                <TableHead>Alerts this app</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {globals.map((c) => (
                <TableRow key={c.id} className={c.imported ? undefined : "opacity-60"}>
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
                    {!c.imported ? (
                      <StatusPill tone="muted">not imported</StatusPill>
                    ) : c.enabled ? (
                      <StatusPill tone="active">yes</StatusPill>
                    ) : (
                      // Imported, but switched off in the catalogue: nothing reaches it
                      // and the fix is not on this page.
                      <StatusPill tone="init">off globally</StatusPill>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={pending}
                      className={c.imported ? "text-status-down hover:text-status-down" : undefined}
                      onClick={() =>
                        startTransition(async () =>
                          setNotice(await setImported(slug, c.id, !c.imported)),
                        )
                      }
                    >
                      {c.imported ? "Remove" : "Import"}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableCard>
      )}

      {globals.length > 0 && (
        <p className="text-[13px] text-muted-foreground">
          {importedCount} of {globals.length} imported.
        </p>
      )}
      {notice && (
        <p className={`text-[13px] ${notice.ok ? "text-status-active" : "text-status-down"}`}>
          {notice.message}
        </p>
      )}
    </section>
  );
}
