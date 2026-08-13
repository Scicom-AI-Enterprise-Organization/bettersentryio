/**
 * A compact brand mark for the 68px rail.
 *
 * The full Scicom wordmark is ~4:1, so it clips at this width. The three orange dots are
 * the distinctive part of that mark, so this keeps them and drops the lettering rather
 * than squeezing a wordmark into a square.
 */
export function RailMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} role="img" aria-label="bettersentryio">
      <rect x="0.5" y="0.5" width="31" height="31" rx="8" className="fill-foreground/[0.08]" />
      <g fill="#f36a10">
        <circle cx="10" cy="11" r="2.1" />
        <circle cx="16" cy="11" r="2.1" />
        <circle cx="22" cy="11" r="2.1" />
      </g>
      <path
        d="M8 19h16M8 23h11"
        className="stroke-foreground/70"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  );
}
