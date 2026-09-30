import { externalHttpUrl } from "@/lib/safe-url";

/** Sentry's key/value treatment: sans muted key column, mono value. */
export function KVRows({ rows }: { rows: [string, string][] }) {
  return (
    <div className="mt-2 space-y-1.5">
      {rows.map(([k, v]) => {
        // Event data is attacker-supplied: link only what parses as http(s), and never
        // hand the opened page a reference back to this one.
        const href = externalHttpUrl(v);
        return (
          <div key={k} className="flex gap-4 text-[13px] leading-6">
            <span className="w-44 shrink-0 text-muted-foreground">{k}</span>
            {href ? (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="min-w-0 break-all font-mono text-primary hover:underline"
              >
                {v}
              </a>
            ) : (
              <span className="min-w-0 break-all font-mono">{v}</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
