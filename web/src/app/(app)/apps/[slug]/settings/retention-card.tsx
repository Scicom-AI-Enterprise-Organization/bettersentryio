"use client";

/**
 * Data retention for one project. Off by default — 0 means keep forever — because
 * deleting error history is a decision, not a side effect of installing the platform.
 *
 * Turning it on (or shortening it) destroys data on the next hourly sweep, so that
 * direction gets the themed confirm; turning it off or lengthening it destroys
 * nothing and applies immediately.
 */

import { useState, useTransition } from "react";
import { History } from "lucide-react";

import { ConfirmDialog } from "@/components/bsio/confirm-dialog";
import { SelectBox } from "@/components/bsio/select-box";
import { changeRetention, type RetentionState } from "./retention-actions";

const CHOICES = [
  { value: "0", label: "Keep forever (default)" },
  { value: "30", label: "30 days" },
  { value: "60", label: "60 days" },
  { value: "90", label: "90 days" },
  { value: "180", label: "180 days" },
  { value: "365", label: "1 year" },
] as const;

export function RetentionCard({ slug, days }: { slug: string; days: number }) {
  const [notice, setNotice] = useState<RetentionState>(null);
  // The proposed value waiting behind the confirm dialog; null when nothing pends.
  const [proposed, setProposed] = useState<number | null>(null);
  const [pending, start] = useTransition();

  const apply = (next: number) => {
    start(async () => {
      setNotice(await changeRetention(slug, next));
      setProposed(null);
    });
  };

  const label = (d: number) =>
    CHOICES.find((c) => Number(c.value) === d)?.label ?? `${d} days`;

  return (
    // No card chrome: every other section on the setup page is a bare <section> sitting
    // on the page background, so a bordered white panel read as pasted in from another
    // screen. Heading matched to the numbered steps above it.
    <section>
      <div className="flex items-center gap-2.5">
        {/* Same 6x6 badge anatomy as the numbered steps above, tinted rather than
            solid: it is a sibling section, not a step to complete. */}
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/15 text-primary">
          <History className="h-3.5 w-3.5" />
        </span>
        <h2 className="text-lg font-semibold tracking-tight">Data retention</h2>
      </div>
      <p className="mt-1 pl-[34px] text-sm text-muted-foreground">
        How long this project keeps error events, stacktraces and attachments. An hourly
        sweep removes anything older; issue counts survive the events they summarise.
        Heartbeats and incident history are not affected.
      </p>
      <div className="mt-4 flex flex-wrap items-center gap-3 pl-[34px]">
        <SelectBox
          value={String(days)}
          active={days !== 0}
          disabled={pending}
          aria-label="Retention"
          onValueChange={(v) => {
            const next = Number(v);
            if (next === days) return;
            // Shortening the window (or turning retention on) deletes data within the
            // hour; lengthening or disabling deletes nothing and needs no ceremony.
            const destructive = next !== 0 && (days === 0 || next < days);
            if (destructive) setProposed(next);
            else apply(next);
          }}
        >
          {CHOICES.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label}
            </option>
          ))}
        </SelectBox>
        {pending && <span className="text-xs text-muted-foreground">applying…</span>}
        {notice && (
          <span className={`text-sm ${notice.ok ? "text-muted-foreground" : "text-status-down"}`}>
            {notice.message}
          </span>
        )}
      </div>

      <ConfirmDialog
        open={proposed !== null}
        onOpenChange={(open) => !open && setProposed(null)}
        title={proposed !== null ? `Keep only ${label(proposed)}?` : ""}
        description={
          <>
            Events older than that are deleted on the next hourly sweep — stacktraces and
            attachments included — and deletion is not recoverable from the platform. The
            change itself is recorded in the audit log.
          </>
        }
        confirmLabel="Apply retention"
        destructive
        pending={pending}
        onConfirm={() => proposed !== null && apply(proposed)}
      />
    </section>
  );
}
