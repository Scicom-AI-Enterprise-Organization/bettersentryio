import { NextResponse } from "next/server";
import { getSamlClient, samlConfigured } from "@/lib/auth-saml";

export async function GET() {
  if (!samlConfigured()) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }
  const saml = getSamlClient();
  const url = await saml.getAuthorizeUrlAsync("", undefined, {});
  return NextResponse.redirect(url);
}
