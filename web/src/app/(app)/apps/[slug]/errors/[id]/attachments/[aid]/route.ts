import { NextResponse } from "next/server";
import { requireUser } from "@/lib/rbac";
import { fetchAttachment } from "@/lib/bsio";

/**
 * Download proxy for event attachments. The engine is not browser-reachable
 * (only its envelope path is public), so the bytes stream through here — with
 * the engine's own Content-Disposition kept, which forces save-not-render on
 * SDK-supplied content.
 */
export async function GET(
  _req: Request,
  { params }: { params: Promise<{ slug: string; id: string; aid: string }> },
) {
  await requireUser();
  const { aid } = await params;
  const n = Number(aid);
  if (!Number.isInteger(n) || n <= 0) {
    return NextResponse.json({ error: "no such attachment" }, { status: 404 });
  }

  const upstream = await fetchAttachment(n);
  if (!upstream.ok) {
    return NextResponse.json({ error: `engine returned ${upstream.status}` }, { status: 502 });
  }
  const headers = new Headers();
  for (const h of ["content-type", "content-disposition", "content-length"]) {
    const v = upstream.headers.get(h);
    if (v) headers.set(h, v);
  }
  // The content type is whatever the SDK claimed, so this route must never let the
  // browser render it on the console's origin — even if the engine's own
  // disposition went missing on the way.
  if (!headers.get("content-disposition")?.toLowerCase().startsWith("attachment")) {
    headers.set("content-disposition", "attachment");
  }
  headers.set("x-content-type-options", "nosniff");
  return new NextResponse(upstream.body, { status: 200, headers });
}
