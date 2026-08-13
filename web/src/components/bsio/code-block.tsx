import { highlight, TOKEN_CLASS } from "@/lib/highlight";
import { cn } from "@/lib/utils";
import { CopyButton } from "./copy-button";

export type CodeVariant = {
  /** Tab label, e.g. "pip" / "curl" / "docker compose". */
  label: string;
  language: string;
  code: string;
};

/**
 * A dark code surface with an optional tab strip, matching how every SDK setup page
 * presents snippets. Highlighting happens on the server, so the markup arrives
 * coloured and no highlighting runs in the browser.
 *
 * Multiple variants render as a tab strip; only the first is shown, and the rest are
 * exposed via a details/summary fallback rather than client-side tabs — one snippet is
 * the answer for most people, and this keeps the whole block server-rendered.
 */
export function CodeBlock({
  code,
  language = "plain",
  filename,
  className,
}: {
  code: string;
  language?: string;
  filename?: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950 text-[13px]",
        className,
      )}
    >
      <div className="flex items-center justify-between gap-2 border-b border-zinc-800 bg-zinc-900/60 pl-3 pr-1.5 py-1.5">
        <span className="truncate font-mono text-[11px] text-zinc-400">
          {filename ?? language}
        </span>
        <CopyButton value={code} />
      </div>
      <pre className="scrollbar-thin overflow-x-auto px-4 py-3 font-mono leading-relaxed">
        <code>
          {highlight(code, language).map((t, i) => (
            <span key={i} className={TOKEN_CLASS[t.kind]}>
              {t.text}
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}

/** A single-line value (a key, a URL) presented as a copyable field, not a snippet. */
export function CopyField({
  label,
  value,
  mono = true,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="flex items-center gap-1 rounded-md border border-border bg-muted/40 pl-3 pr-1 py-1">
        <span className={cn("min-w-0 flex-1 truncate text-sm", mono && "font-mono text-xs")}>
          {value}
        </span>
        <CopyButton value={value} />
      </div>
    </div>
  );
}
