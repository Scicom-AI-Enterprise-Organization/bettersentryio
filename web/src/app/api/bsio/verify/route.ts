import { NextResponse } from "next/server";
import { auth } from "@/lib/auth";
import { getApp, getOverview } from "@/lib/bsio";

/**
 * Backs the "waiting for your first heartbeat" check on the setup page.
 *
 * It exists so the browser never needs the engine's API key: the poll comes here,
 * this route talks to the engine server-side, and only the state goes back.
 *
 * `?app=` watches every monitor an app has reported — that is what reconciles a
 * pasted snippet with the app you created. `?slug=` watches one named monitor.
 */
export async function GET(req: Request) {
  const session = await auth();
  if (!session?.user) {
    return NextResponse.json({ error: "authentication required" }, { status: 401 });
  }

  const params = new URL(req.url).searchParams;
  const app = params.get("app")?.trim();
  const slug = params.get("slug")?.trim();

  if (app) {
    const result = await getApp(app);
    if (!result.ok) {
      return NextResponse.json({ reachable: false, error: result.error });
    }
    return NextResponse.json({
      reachable: true,
      connected: result.data.app.connected,
      monitors: result.data.monitors.map((m) => ({
        slug: m.slug,
        environment: m.environment,
        status: m.status,
        lastBeatAt: m.last_beat_at,
        progress: m.last_progress,
        beats24h: m.beats_24h,
      })),
    });
  }

  if (!slug) {
    return NextResponse.json({ error: "app or slug is required" }, { status: 400 });
  }

  const result = await getOverview();
  if (!result.ok) {
    return NextResponse.json({ reachable: false, error: result.error });
  }

  const monitor = result.data.monitors.find((m) => m.slug === slug);
  return NextResponse.json({
    reachable: true,
    found: Boolean(monitor),
    status: monitor?.status ?? null,
    lastBeatAt: monitor?.last_beat_at ?? null,
    progress: monitor?.last_progress ?? null,
    beats24h: monitor?.beats_24h ?? 0,
  });
}
