"use client";

import { useState, useTransition } from "react";
import { BellOff, Timer } from "lucide-react";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@/components/ui/select";
import { changePatience, type ActionState } from "./actions";
import { patienceLabel } from "./patience";

/**
 * Alert patience — Sentry's action interval by another name.
 *
 * The first alert of a quiet spell is always immediate: on-call learns something
 * broke within seconds. Everything that follows inside the window is collected and
 * arrives as one digest card when it closes. A service taking 50 requests a second
 * that starts throwing on all of them produces one alert and then one summary per
 * window, not one card per new issue.
 */
export function PatienceCard({ slug, seconds, choices }: {
  slug: string;
  seconds: number;
  choices: number[];
}) {
  const [value, setValue] = useState(seconds);
  const [notice, setNotice] = useState<ActionState>(null);
  const [pending, startTransition] = useTransition();

  return (
    // A bare section, like every other block on this page. `bg-card` made it the one
    // white panel on a page whose sections sit directly on the background — and in light
    // mode --card is near-white against a grey --background, so it read as a stray sheet.
    <section>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-[15px] font-semibold tracking-tight">
            {value === 0 ? (
              <BellOff className="h-4 w-4 text-status-down" />
            ) : (
              <Timer className="h-4 w-4 text-muted-foreground" />
            )}
            Alert patience
          </h2>
          <p className="mt-1.5 text-[13px] text-muted-foreground">
            The first alert in a quiet window goes out immediately. Anything else that
            happens before the window closes arrives as one digest card instead of its own
            — so an app throwing on every request wakes you once, not fifty times a second.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            value={String(value)}
            disabled={pending}
            onValueChange={(next) => {
              const secs = Number(next);
              setValue(secs);
              startTransition(async () => setNotice(await changePatience(slug, secs)));
            }}
          >
            {/* The label is rendered directly rather than through SelectValue, whose
                text is only resolvable once the portalled items hydrate — otherwise
                the trigger ships blank and fills in a moment later. */}
            <SelectTrigger aria-label="Alert patience" className="w-52 bg-card">
              <span className="truncate">{patienceLabel(value)}</span>
            </SelectTrigger>
            {/* Explicit, not inherited: item-aligned positions the menu so the selected
                option covers the trigger, which puts the rest off the top of a page that
                cannot scroll — present in the DOM and unclickable. This card sits high on
                the page, so it is exactly the case that breaks. */}
            <SelectContent position="popper">
              {choices.map((c) => (
                <SelectItem key={c} value={String(c)}>
                  {patienceLabel(c)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
      {value === 0 && (
        <p className="mt-3 text-[13px] text-status-down">
          Patience is off: every new issue and every monitor incident sends its own card.
        </p>
      )}
      {notice && (
        <p className={`mt-3 text-[13px] ${notice.ok ? "text-status-active" : "text-status-down"}`}>
          {notice.message}
        </p>
      )}
    </section>
  );
}
