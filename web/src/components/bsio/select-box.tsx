"use client";

import * as React from "react";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

/**
 * The house dropdown: the themed Radix select, wearing the API of a native one.
 *
 * It was a real `<select>` until a native menu turned out to be the one control on
 * these pages the OS drew itself — wrong font, wrong radius, and in dark mode a white
 * popup. Keeping the `<option>` children API means every existing call site got the
 * themed menu without being touched.
 *
 * Two compatibility details worth knowing:
 *
 *  - Radix reserves the empty string for "nothing selected", but `<option value="">`
 *    is how these filters spell "all". Empty is mapped to a sentinel on the way in and
 *    back out, so callers keep using "".
 *  - `onChange` is offered alongside `onValueChange` and receives `{target:{value}}`,
 *    because that is what the call sites already read. It is deliberately not a real
 *    ChangeEvent — there is no DOM event behind it, and pretending otherwise would
 *    invite someone to call preventDefault on nothing.
 *
 * `active` marks a filter that is narrowing something right now. `bg-card` rather than
 * the trigger's default transparent: on the body background a transparent control is
 * invisible, which is the whole reason this wrapper exists.
 */

// Radix rejects "" as an item value, so the "all" option travels under a stand-in.
const EMPTY = "__all__";

type Option = { value: string; label: React.ReactNode; disabled?: boolean };

export function SelectBox({
  active,
  className,
  children,
  value,
  defaultValue,
  onChange,
  onValueChange,
  name,
  disabled,
  size = "default",
  "aria-label": ariaLabel,
}: {
  active?: boolean;
  className?: string;
  children?: React.ReactNode;
  value?: string;
  defaultValue?: string;
  onChange?: (e: { target: { value: string } }) => void;
  onValueChange?: (value: string) => void;
  /** Present for a form: a Radix select submits nothing, so a hidden input carries it. */
  name?: string;
  disabled?: boolean;
  size?: "sm" | "default";
  "aria-label"?: string;
}) {
  const options = React.useMemo(() => collectOptions(children), [children]);
  const [internal, setInternal] = React.useState(defaultValue ?? options[0]?.value ?? "");
  const controlled = value !== undefined;
  const current = controlled ? value : internal;

  // Rendered in the trigger rather than by SelectValue, which takes its text from the
  // portalled items and therefore renders nothing on the server: the control would
  // arrive blank and fill in on hydration.
  const label = options.find((o) => o.value === current)?.label ?? current;

  const handle = (next: string) => {
    const real = next === EMPTY ? "" : next;
    if (!controlled) setInternal(real);
    onValueChange?.(real);
    onChange?.({ target: { value: real } });
  };

  return (
    <>
      <Select value={current === "" ? EMPTY : current} onValueChange={handle} disabled={disabled}>
        <SelectTrigger
          size={size}
          aria-label={ariaLabel}
          className={cn(
            "bg-card",
            active
              ? "border-primary/60 bg-primary/5 font-medium text-primary"
              : "border-input hover:border-muted-foreground/40",
            className,
          )}
        >
          <span className="truncate">{label}</span>
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value === "" ? EMPTY : o.value} disabled={o.disabled}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {name && <input type="hidden" name={name} value={current} />}
    </>
  );
}

/**
 * Reads `<option>` children into data. Anything else is skipped rather than rendered:
 * a Radix menu can only hold items, and silently dropping a stray node beats crashing
 * the page it sits on.
 */
function collectOptions(children: React.ReactNode): Option[] {
  const out: Option[] = [];
  React.Children.forEach(children, (child) => {
    if (!React.isValidElement(child)) return;
    if (child.type === "option") {
      const props = child.props as React.OptionHTMLAttributes<HTMLOptionElement> & {
        children?: React.ReactNode;
      };
      out.push({
        value: String(props.value ?? ""),
        label: props.children ?? String(props.value ?? ""),
        disabled: props.disabled,
      });
      return;
    }
    // <optgroup> and fragments: take their options, drop the grouping. No call site
    // groups today, and a half-rendered group would read as missing choices.
    const nested = (child.props as { children?: React.ReactNode })?.children;
    if (nested) out.push(...collectOptions(nested));
  });
  return out;
}
