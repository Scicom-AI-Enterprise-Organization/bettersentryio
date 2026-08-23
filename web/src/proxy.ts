import NextAuth from "next-auth";
import { NextResponse } from "next/server";
import { authConfig } from "@/lib/auth.config";

const { auth } = NextAuth(authConfig);

// /invite must be public: an invitee has no session yet by definition —
// without it every invite link bounced to /login, where the only possible
// outcome was CredentialsSignin (measured: 1 invitation, 0 accepted).
const PUBLIC_PATHS = ["/", "/login", "/forbidden", "/showcase", "/invite"];

// Icons are requested by the browser chrome, not by a user, and there is nothing in
// them to protect. Left behind the gate they 307 to /login, the browser rejects the
// HTML as an image, and the tab falls back to whatever it can get — so the login page
// in particular ends up without the icon it just asked for.
const PUBLIC_ASSETS = ["/favicon.ico", "/icon.svg", "/apple-icon.png"];

export default auth((req) => {
  const { pathname } = req.nextUrl;

  if (
    pathname.startsWith("/_next") ||
    pathname.startsWith("/api/auth") ||
    pathname.startsWith("/images") ||
    PUBLIC_ASSETS.includes(pathname)
  ) {
    return NextResponse.next();
  }

  const isPublic = PUBLIC_PATHS.some(
    (p) => pathname === p || pathname.startsWith(`${p}/`),
  );
  if (isPublic) return NextResponse.next();

  if (!req.auth) {
    const url = req.nextUrl.clone();
    url.pathname = "/login";
    url.searchParams.set("callbackUrl", pathname);
    return NextResponse.redirect(url);
  }

  return NextResponse.next();
});

export const config = {
  // Keep in step with PUBLIC_ASSETS above: this skips the middleware entirely, that
  // one waves the request through if it ever reaches here.
  matcher: ["/((?!_next/static|_next/image|favicon\\.ico|icon\\.svg|apple-icon\\.png).*)"],
};
