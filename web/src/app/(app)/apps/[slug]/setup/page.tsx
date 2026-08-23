import { permanentRedirect } from "next/navigation";

/**
 * The page lives at /settings now — it stopped being onboarding the day it grew
 * configuration (data retention). This stub keeps every old link working: snippets,
 * bookmarks, and the setup URLs printed by engines older than the rename.
 */
export default async function SetupRedirect({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  permanentRedirect(`/apps/${slug}/settings`);
}
