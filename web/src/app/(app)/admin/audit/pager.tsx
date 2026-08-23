import Link from "next/link";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";

/**
 * Keyset pagination, as two links.
 *
 * No page numbers and no total, deliberately. Numbering needs OFFSET, which is wrong for
 * a table being appended to while somebody reads it — a row arriving at the top shifts
 * every later page down, so page 2 repeats a row you already read. And a total means
 * `count(*)` over the filter on every render, which is the one query here that gets
 * slower forever. "Older" and "Newer" are the two things a reader actually does.
 */
export function Pager({
  params,
  newest,
  oldest,
  hasNewer,
  hasOlder,
  showing,
}: {
  /** The current filters, so paging preserves them. */
  params: Record<string, string | undefined>;
  /** Cursors for the page edges: "<at>,<id>", the whole sort key. */
  newest?: string;
  oldest?: string;
  hasNewer: boolean;
  hasOlder: boolean;
  showing: number;
}) {
  const href = (cursor: { before?: string; after?: string }) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v) q.set(k, v);
    }
    q.delete("before");
    q.delete("after");
    if (cursor.before) q.set("before", cursor.before);
    if (cursor.after) q.set("after", cursor.after);
    return `?${q}`;
  };

  if (!hasNewer && !hasOlder) {
    return (
      <p className="text-[13px] text-muted-foreground">
        {showing} {showing === 1 ? "entry" : "entries"} — all of them for this filter.
      </p>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button asChild={hasNewer} variant="outline" size="sm" disabled={!hasNewer}>
        {hasNewer && newest ? (
          <Link href={href({ after: newest })} scroll={false}>
            <ChevronLeft className="h-3.5 w-3.5" />
            Newer
          </Link>
        ) : (
          <span>
            <ChevronLeft className="h-3.5 w-3.5" />
            Newer
          </span>
        )}
      </Button>
      <Button asChild={hasOlder} variant="outline" size="sm" disabled={!hasOlder}>
        {hasOlder && oldest ? (
          <Link href={href({ before: oldest })} scroll={false}>
            Older
            <ChevronRight className="h-3.5 w-3.5" />
          </Link>
        ) : (
          <span>
            Older
            <ChevronRight className="h-3.5 w-3.5" />
          </span>
        )}
      </Button>
      <p className="text-[13px] text-muted-foreground">
        {showing} {showing === 1 ? "entry" : "entries"} on this page
      </p>
    </div>
  );
}
