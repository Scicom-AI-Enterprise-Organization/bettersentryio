import * as React from "react"

import { cn } from "@/lib/utils"

/**
 * RunPod-style status pill shared with GPUPlatform. Renders a fully-rounded
 * pill with an optional leading dot that inherits the pill's text colour.
 * Colour comes from the `--status-*` design tokens (light + dark), so it reads
 * correctly in both themes — unlike the old `bg-*-100 text-*-800` badges.
 */
export type StatusTone = "active" | "idle" | "init" | "down" | "muted"

const TONE_CLASS: Record<StatusTone, string> = {
  active: "bg-status-active/15 text-status-active",
  idle: "bg-status-idle/15 text-status-idle",
  init: "bg-status-init/15 text-status-init",
  down: "bg-status-down/15 text-status-down",
  muted: "bg-muted text-muted-foreground",
}

function StatusPill({
  tone,
  showDot = true,
  className,
  children,
  ...props
}: React.ComponentProps<"span"> & { tone: StatusTone; showDot?: boolean }) {
  return (
    <span
      data-slot="status-pill"
      className={cn(
        "inline-flex w-fit items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium",
        TONE_CLASS[tone],
        className
      )}
      {...props}
    >
      {showDot && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-current" />}
      {children}
    </span>
  )
}

/* ---- Domain → tone mappers -------------------------------------------------
 * These preserve SlurmUI's existing colour semantics (yellow/blue/green/red/
 * gray), routed through the token system. The one deliberate tidy-up is
 * cluster PROVISIONING → init (blue) instead of amber, since it *is* the
 * "initializing" state and that frees amber for the DEGRADED warning. */

export function jobStatusTone(status: string | null | undefined): StatusTone {
  switch ((status ?? "").toUpperCase()) {
    case "PENDING":
      return "idle"
    case "RUNNING":
      return "init"
    case "COMPLETED":
      return "active"
    case "FAILED":
      return "down"
    case "CANCELLED":
      return "muted"
    default:
      return "muted"
  }
}

export function clusterStatusTone(status: string | null | undefined): StatusTone {
  switch ((status ?? "").toUpperCase()) {
    case "PROVISIONING":
      return "init"
    case "ACTIVE":
      return "active"
    case "DEGRADED":
      return "idle"
    case "OFFLINE":
      return "down"
    default:
      return "muted"
  }
}

const CLUSTER_STATUS_LABELS: Record<string, string> = {
  PROVISIONING: "Provisioning",
  ACTIVE: "Active",
  DEGRADED: "Degraded",
  OFFLINE: "Offline",
}

/** Title-cased label for a cluster status, falling back to the raw value. */
export function clusterStatusLabel(status: string | null | undefined): string {
  const s = (status ?? "").toUpperCase()
  return CLUSTER_STATUS_LABELS[s] ?? status ?? ""
}

export function nodeStateTone(state: string | null | undefined): StatusTone {
  const s = (state ?? "").toLowerCase()
  if (s.includes("down") || s.includes("drain")) return "down"
  if (s.includes("alloc") || s.includes("mix")) return "init"
  if (s.includes("idle")) return "active"
  if (s.includes("undeployed")) return "idle"
  return "muted"
}

export { StatusPill }
