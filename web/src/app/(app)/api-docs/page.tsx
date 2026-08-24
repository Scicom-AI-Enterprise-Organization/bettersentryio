import { requireUser } from "@/lib/rbac";
import { ingestBase } from "@/lib/snippets";
import { ApiDocs } from "./api-docs";

export const metadata = { title: "API docs" };

/**
 * The engine's API reference. The base URL is resolved server-side because it is the
 * address a *service* should call (BSIO_PUBLIC_URL), which is not necessarily the
 * address this UI reaches the engine on.
 */
export default async function ApiDocsPage() {
  await requireUser();
  return <ApiDocs base={ingestBase()} />;
}
