"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * A native select styled like the house Input: surfaced on the card background
 * with a shadow so it reads as a control, not text floating on the page —
 * `bg-transparent` selects were invisible against the body (measured by Ariff's
 * squint). `active` marks a filter that is narrowing something right now.
 */
export function SelectBox({
  active,
  className,
  ...props
}: React.ComponentProps<"select"> & { active?: boolean }) {
  return (
    <select
      data-slot="select"
      className={cn(
        "h-9 cursor-pointer rounded-md border bg-background py-1 pl-3 pr-2 text-sm shadow-xs transition-[color,box-shadow,border-color] outline-none",
        "focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]",
        "disabled:pointer-events-none disabled:opacity-50",
        active
          ? "border-primary/60 bg-primary/5 font-medium text-primary"
          : "border-input text-foreground hover:border-muted-foreground/40",
        className,
      )}
      {...props}
    />
  );
}
