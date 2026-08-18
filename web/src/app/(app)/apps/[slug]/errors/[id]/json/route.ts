import { NextResponse } from "next/server";
import { requireUser } from "@/lib/rbac";
import { getIssue, getIssueEvent } from "@/lib/bsio";

/** The raw stored event — the SDK's own bytes — for "view JSON" on the event page. */
export async function GET(
  req: Request,
  { params }: { params: Promise<{ slug: string; id: string }> },
) {
  await requireUser();
  const { id } = await params;
  const eventID = new URL(req.url).searchParams.get("event");

  if (eventID) {
    const one = await getIssueEvent(id, eventID);
    if (one.ok) return NextResponse.json(one.data.payload);
    return NextResponse.json({ error: one.error }, { status: 502 });
  }
  const detail = await getIssue(id);
  if (detail.ok) return NextResponse.json(detail.data.latest_event);
  return NextResponse.json({ error: detail.error }, { status: 502 });
}
