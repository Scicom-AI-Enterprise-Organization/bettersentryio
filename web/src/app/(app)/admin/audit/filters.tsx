"use client";

/**
 * The audit log's filter row: a time window, an actor, an action prefix.
 *
 * Not `WindowControls` from the shared set, for two reasons. It renders a bucket-interval
 * picker, which means nothing on a table with no chart. And more importantly, changing
 * any filter here has to drop the pagination cursor — a `before` id from the previous
 * result set points into a list that no longer exists, so keeping it would show a page
 * from the middle of nowhere. A generic control cannot know that.
 *
 * The window presets and the local↔absolute conversion are still the shared ones from
 * `@/lib/ranges`, so a 30-day window means the same thing here as on the issue list.
 */

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Search, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectBox } from "@/components/bsio/select-box";
import {
  DEFAULT_RANGE,
  RANGES,
  fromLocalInput,
  toLocalInput,
  type TimeWindow,
} from "@/lib/ranges";

export function AuditFilters({
  window: w,
  actor,
  action,
}: {
  window: TimeWindow;
  actor: string;
  action: string;
}) {
  const router = useRouter();
  const params = useSearchParams();
  const [actorDraft, setActorDraft] = useState(actor);
  const [actionDraft, setActionDraft] = useState(action);

  const push = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params.toString());
    for (const [k, v] of Object.entries(patch)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, v);
    }
    // Any change to what is being asked for invalidates where we were in the answer.
    next.delete("before");
    next.delete("after");
    router.replace(next.size ? `?${next}` : "?", { scroll: false });
  };

  const filtered = !!actor || !!action || w.range !== DEFAULT_RANGE;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <SelectBox
        value={w.range}
        active={w.range !== DEFAULT_RANGE}
        aria-label="Time range"
        className="w-44"
        onValueChange={(v) =>
          // Leaving custom mode drops the stamps: a stale start/end in the URL of a
          // "last 24 hours" view is a trap for whoever opens that link next.
          push(v === "custom" ? { range: v } : { range: v, start: null, end: null })
        }
      >
        {RANGES.map((r) => (
          <option key={r.value} value={r.value}>
            {r.label}
          </option>
        ))}
      </SelectBox>

      {w.range === "custom" && (
        <CustomRange
          key={`${w.start ?? ""}|${w.end ?? ""}`}
          window={w}
          onApply={(start, end) => push({ start, end })}
        />
      )}

      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          push({ actor: actorDraft.trim(), action: actionDraft.trim() });
        }}
      >
        <Input
          value={actorDraft}
          onChange={(e) => setActorDraft(e.target.value)}
          placeholder="actor — email, operator, token:…"
          aria-label="Actor"
          className="h-9 w-64 bg-card text-sm"
        />
        <Input
          value={actionDraft}
          onChange={(e) => setActionDraft(e.target.value)}
          placeholder="action prefix — DELETE, POST /api/0/tokens…"
          aria-label="Action prefix"
          className="h-9 w-72 bg-card font-mono text-[13px]"
        />
        <Button type="submit" size="sm" variant="outline">
          <Search className="h-3.5 w-3.5" />
          Filter
        </Button>
      </form>

      {filtered && (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setActorDraft("");
            setActionDraft("");
            push({ actor: null, action: null, range: null, start: null, end: null });
          }}
        >
          <X className="h-3.5 w-3.5" />
          Clear
        </Button>
      )}
    </div>
  );
}

/**
 * Two timestamps and an Apply. Applied explicitly rather than on change: a half-typed
 * date is a window that cannot contain anything.
 *
 * The inputs hold local wall-clock and the URL holds the absolute instant, and this is
 * where that conversion happens — it is the only part of this page running in the
 * browser. A server resolving "13:00" would resolve it in the server's zone and move a
 * +08 reader's afternoon by eight hours.
 */
function CustomRange({
  window: w,
  onApply,
}: {
  window: TimeWindow;
  onApply: (start: string, end: string) => void;
}) {
  const [start, setStart] = useState(toLocalInput(w.start));
  const [end, setEnd] = useState(toLocalInput(w.end));

  const applicable =
    !!start &&
    !!end &&
    Date.parse(start) < Date.parse(end) &&
    (fromLocalInput(start) !== w.start || fromLocalInput(end) !== w.end);

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input
        type="datetime-local"
        value={start}
        onChange={(e) => setStart(e.target.value)}
        aria-label="From"
        className="h-9 w-[205px] bg-card text-sm"
      />
      <Input
        type="datetime-local"
        value={end}
        onChange={(e) => setEnd(e.target.value)}
        aria-label="To"
        className="h-9 w-[205px] bg-card text-sm"
      />
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={!applicable}
        onClick={() => onApply(fromLocalInput(start), fromLocalInput(end))}
      >
        Apply
      </Button>
    </div>
  );
}
