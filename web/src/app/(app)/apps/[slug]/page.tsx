import { redirect } from "next/navigation";
import { requireUser } from "@/lib/rbac";
import { getApp } from "@/lib/bsio";

/**
 * A project has no page of its own — it lands you where the work is. Until it reports,
 * that is setup; after, it is the issue list.
 */
export default async function ProjectIndex({ params }: { params: Promise<{ slug: string }> }) {
  await requireUser();
  const { slug } = await params;
  const result = await getApp(slug);
  const connected = result.ok && result.data.app.connected;
  redirect(connected ? `/apps/${slug}/issues/outages` : `/apps/${slug}/settings`);
}
