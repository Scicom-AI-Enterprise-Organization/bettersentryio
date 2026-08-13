import { redirect } from "next/navigation";
import { HOME } from "@/lib/nav";

/**
 * The bare domain goes straight to the app.
 *
 * The template shipped a marketing page here. This is an internal tool behind a login, so
 * that page would only ever be stale copy — and an unauthenticated visitor still lands on
 * /login, because HOME requires a session.
 */
export default function RootPage() {
  redirect(HOME);
}
