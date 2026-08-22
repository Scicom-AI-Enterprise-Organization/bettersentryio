import { Label } from "@/components/ui/label";

/**
 * One labelled control with its explanation attached.
 *
 * The hint belongs to the field rather than sitting in a paragraph above the form:
 * each answer is explained where it is given, which is the difference between a form
 * you read and a form you fill in twice.
 *
 * Omit `htmlFor` for a Radix control — its trigger is a button, and a label pointing
 * at something click-through cannot focus is worse than no label. Those carry
 * `aria-label` on the control instead.
 */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  className,
  children,
}: {
  label: string;
  htmlFor?: string;
  hint?: string;
  /** Replaces the hint while it is set, so the row does not change height. */
  error?: string | null;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={className}>
      <Label htmlFor={htmlFor} className="mb-1.5">
        {label}
      </Label>
      {children}
      {(error || hint) && (
        <p
          className={`mt-1.5 text-xs leading-snug ${error ? "text-status-down" : "text-muted-foreground"}`}
        >
          {error || hint}
        </p>
      )}
    </div>
  );
}
