import { NextRequest, NextResponse } from "next/server";
import { AuthError } from "next-auth";
import { samlConfigured } from "@/lib/auth-saml";
import { signIn } from "@/lib/auth";

/**
 * Where the redirect lands. Built on AUTH_URL, not req.url: behind the edge req.url is
 * the pod's own bind address, and a redirect there sends the browser to
 * https://0.0.0.0:3000 (the sibling Label app's CS-2026-005).
 */
function appUrl(path: string, req: NextRequest): URL {
  return new URL(path, process.env.AUTH_URL ?? req.url);
}

/**
 * The ACS endpoint. It only carries the IdP's SAMLResponse to the "saml" provider;
 * the signature check happens in the provider's `authorize`, which is the one path
 * every SAML sign-in goes through (see samlProvider).
 */
export async function POST(req: NextRequest) {
  if (!samlConfigured()) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }
  const formData = await req.formData();
  const samlResponse = formData.get("SAMLResponse");
  if (typeof samlResponse !== "string" || !samlResponse) {
    return NextResponse.json({ error: "Missing SAMLResponse" }, { status: 400 });
  }

  try {
    await signIn("saml", { SAMLResponse: samlResponse, redirect: false });
  } catch (e) {
    if (e instanceof AuthError) {
      return NextResponse.redirect(appUrl("/login?error=CredentialsSignin", req), 303);
    }
    throw e;
  }
  return NextResponse.redirect(appUrl("/dashboard", req), 303);
}
