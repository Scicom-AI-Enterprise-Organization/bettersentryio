import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

type Tone = "default" | "muted" | "positive" | "warning" | "negative";

// `positive` uses --status-active rather than --chart-2 on purpose. The chart ramp
// follows the dual-mode accent, so --chart-2 is orange in dark mode — and an orange
// "Healthy" reads as a warning. The status tokens stay green/amber/red in both themes.
const TONE: Record<Tone, string> = {
  default: "text-foreground",
  muted: "text-muted-foreground",
  positive: "text-status-active",
  warning: "text-status-idle",
  negative: "text-status-down",
};

export function StatCard({
  label, value, sub, tone = "default",
}: {
  label: string;
  value: string | number;
  sub?: string;
  tone?: Tone;
}) {
  return (
    <Card>
      <CardContent className="pt-4 pb-3 space-y-0.5">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className={cn("text-2xl font-semibold tabular-nums", TONE[tone])}>{value}</p>
        {sub && <p className="text-[11px] text-muted-foreground">{sub}</p>}
      </CardContent>
    </Card>
  );
}
