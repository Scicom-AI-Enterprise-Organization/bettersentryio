"use client";

/**
 * The time window, as controls: a span preset, a bucket width, and — when the preset is
 * "Custom range…" — two absolute timestamps.
 *
 * These write to the URL with `router.replace`, which re-runs the server component and
 * refetches. That is the difference between these and the row's other filters: level and
 * environment narrow rows the browser already has, while changing the window changes
 * what the engine must go and read.
 */

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { CalendarRange } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectBox } from "@/components/bsio/select-box";
import {
  DEFAULT_INTERVAL,
  DEFAULT_RANGE,
  INTERVALS,
  RANGES,
  fromLocalInput,
  toLocalInput,
  type TimeWindow,
} from "@/lib/ranges";

export function WindowControls({ window: w }: { window: TimeWindow }) {
  const router = useRouter();
  const params = useSearchParams();
  // Wrapped, not a bare fragment: dropped into a `space-y-*` column an unwrapped set of
  // controls stacks vertically, which the next page should not have to discover.

  const push = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params.toString());
    for (const [k, v] of Object.entries(patch)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, v);
    }
    router.replace(`?${next.toString()}`, { scroll: false });
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <SelectBox
        value={w.range}
        active={w.range !== DEFAULT_RANGE}
        aria-label="Time range"
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
        // Keyed by the window in the URL so a new window mounts fresh inputs. Resetting
        // this state from an effect instead would be the classic props-into-state
        // anti-pattern, and it renders the stale value first.
        <CustomRange
          key={`${w.start ?? ""}|${w.end ?? ""}`}
          window={w}
          onApply={(start, end) => push({ start, end })}
        />
      )}

      <SelectBox
        value={w.interval ?? DEFAULT_INTERVAL}
        active={(w.interval ?? DEFAULT_INTERVAL) !== DEFAULT_INTERVAL}
        aria-label="Bucket interval"
        onValueChange={(v) => push({ interval: v === DEFAULT_INTERVAL ? null : v })}
      >
        {INTERVALS.map((i) => (
          <option key={i.value} value={i.value}>
            {i.label}
          </option>
        ))}
      </SelectBox>
    </div>
  );
}

/**
 * Two timestamps and an Apply.
 *
 * Applied explicitly rather than on change: a half-typed date is a window that cannot
 * contain anything, and refetching on every keystroke of a date field would ask the
 * engine about the year 0002 on the way to 2026.
 *
 * The inputs hold local wall-clock; the URL holds the absolute instant. This component
 * is where that conversion happens, because it is the only part of this that runs in the
 * browser — a server resolving "13:00" would resolve it in the server's zone.
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
    // One flex group so a wrapping row keeps the pair and its Apply together, rather
    // than leaving "To" and the button orphaned on the next line.
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
        <CalendarRange className="h-3.5 w-3.5" />
        Apply
      </Button>
    </div>
  );
}
