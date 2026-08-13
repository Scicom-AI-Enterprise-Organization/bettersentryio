import { cn } from "@/lib/utils";
import type { Bucket } from "@/lib/bsio";

/**
 * Beat activity as bars, one per 5-minute bucket.
 *
 * Empty buckets are drawn as a faint stub rather than omitted: an absent bar and a
 * zero bar look identical, but only one of them tells you the loop stopped beating.
 */
export function ActivityBars({
  buckets,
  className,
  barClassName,
}: {
  buckets: Bucket[];
  className?: string;
  barClassName?: string;
}) {
  if (buckets.length === 0) {
    return <span className="text-xs text-muted-foreground">no beats recorded</span>;
  }
  const peak = Math.max(1, ...buckets.map((b) => b.beats));

  return (
    <div className={cn("flex items-end gap-0.5", className)}>
      {buckets.map((b) => {
        const empty = b.beats <= 0;
        // Floor non-empty bars so a single beat is still visible next to a busy bucket.
        const height = empty ? 2 : Math.max(12, Math.round((b.beats / peak) * 100));
        return (
          <span
            key={b.at}
            title={`${new Date(b.at).toLocaleTimeString([], { hour12: false })} — ${b.beats} beats, progress +${b.progress_delta}`}
            className={cn(
              "flex-1 rounded-sm",
              empty ? "bg-border" : "bg-status-init",
              barClassName,
            )}
            style={{ height: `${height}%` }}
          />
        );
      })}
    </div>
  );
}
