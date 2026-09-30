import Credentials from "next-auth/providers/credentials";
import { SAML, type Profile } from "@node-saml/node-saml";
import { prisma } from "@/lib/db";

/**
 * SAML provider implemented as an Auth.js Credentials provider that consumes
 * the IdP POST response. The /api/auth/saml/* routes handle the SP redirect
 * and ACS callback, then forward the raw assertion here, where it is validated.
 *
 * For multi-tenant SAML, prefer BoxyHQ Jackson and use it as a generic OIDC
 * provider instead.
 */
export function getSamlClient() {
  return new SAML({
    entryPoint: process.env.AUTH_SAML_ENTRY_POINT!,
    // Deliberately still the template default: an issuer is an identifier the IdP is
    // registered against, so renaming it silently breaks an existing registration.
    // Set AUTH_SAML_ISSUER when SAML is actually configured.
    issuer: process.env.AUTH_SAML_ISSUER ?? "enterprise-template",
    callbackUrl: `${process.env.AUTH_URL}/api/auth/saml/callback`,
    idpCert: process.env.AUTH_SAML_IDP_CERT!,
    wantAssertionsSigned: true,
    signatureAlgorithm: "sha256",
  });
}

/**
 * SAML is on only when both the IdP entry point and its signing certificate are set.
 * The /api/auth/saml/* routes answer 404 otherwise: without a certificate there is
 * nothing to validate an assertion against, and the half-configured client used to
 * throw a 500 at anyone who asked.
 */
export function samlConfigured(): boolean {
  return Boolean(process.env.AUTH_SAML_ENTRY_POINT && process.env.AUTH_SAML_IDP_CERT);
}

function attr(profile: Profile, ...names: string[]): string | undefined {
  for (const n of names) {
    const v = (profile as Record<string, unknown>)[n];
    if (typeof v === "string" && v) return v;
  }
  return undefined;
}

/**
 * The credential this provider accepts is the IdP's signed SAMLResponse itself, and
 * `authorize` validates it here, server-side, every time.
 *
 * It used to accept a `profile` JSON that the ACS route built after validating. But
 * the provider is also reachable directly — POST /api/auth/callback/saml with a CSRF
 * token from /api/auth/csrf — and there it took the JSON on trust: anyone could sign
 * in as any email, admins included, whenever SAML was enabled. Validating inside
 * `authorize` means there is no way in that skips the signature check.
 */
export function samlProvider() {
  return Credentials({
    id: "saml",
    name: "SAML SSO",
    credentials: {
      SAMLResponse: { label: "SAMLResponse", type: "text" },
    },
    async authorize(raw) {
      const samlResponse = raw?.SAMLResponse;
      if (typeof samlResponse !== "string" || !samlResponse) return null;

      let profile: Profile | null;
      try {
        ({ profile } = await getSamlClient().validatePostResponseAsync({
          SAMLResponse: samlResponse,
        }));
      } catch {
        return null; // bad signature, expired, wrong audience — all just "no"
      }
      if (!profile) return null;

      const email = attr(profile, "email", "mail") ?? profile.nameID;
      if (!email) return null;
      const name = attr(profile, "cn", "displayName", "givenName");

      const user = await prisma.user.upsert({
        where: { email },
        update: { name: name ?? undefined },
        create: { email, name: name ?? null },
      });
      return { id: user.id, email: user.email, name: user.name };
    },
  });
}
