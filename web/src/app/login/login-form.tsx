"use client";

import { useState, useTransition } from "react";
import Link from "next/link";
import Image from "next/image";
import { signIn } from "next-auth/react";
import { toast } from "sonner";
import { Building2, KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { GoogleIcon } from "@/components/auth/provider-icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface LoginFormProps {
  callbackUrl: string;
  error?: string;
  providers: {
    azure: boolean;
    google: boolean;
    keycloak: boolean;
    saml: boolean;
  };
}

export function LoginForm({ callbackUrl, error, providers }: LoginFormProps) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [pending, startTransition] = useTransition();

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    startTransition(async () => {
      const res = await signIn("credentials", {
        email,
        password,
        redirect: false,
        callbackUrl,
      });
      if (res?.error) {
        toast.error("Invalid email or password");
      } else if (res?.url) {
        window.location.href = res.url;
      }
    });
  }

  function notConfigured(name: string) {
    return () =>
      toast.error(`${name} is not configured`, {
        description: `Set the ${name} env vars in .env to enable.`,
      });
  }

  const ssoButtons = [
    {
      key: "google",
      label: "Google",
      icon: <GoogleIcon className="h-4 w-4" />,
      onClick: providers.google
        ? () => signIn("google", { callbackUrl })
        : notConfigured("Google"),
      disabled: !providers.google,
    },
    {
      key: "azure",
      label: "Azure AD",
      icon: <KeyRound className="h-4 w-4 text-[#0078D4]" />,
      onClick: providers.azure
        ? () => signIn("microsoft-entra-id", { callbackUrl })
        : notConfigured("Azure AD"),
      disabled: !providers.azure,
    },
    {
      key: "keycloak",
      label: "Keycloak",
      icon: <Building2 className="h-4 w-4" />,
      onClick: providers.keycloak
        ? () => signIn("keycloak", { callbackUrl })
        : notConfigured("Keycloak"),
      disabled: !providers.keycloak,
    },
    {
      key: "saml",
      label: "SAML SSO",
      icon: <ShieldCheck className="h-4 w-4" />,
      onClick: providers.saml
        ? () => (window.location.href = "/api/auth/saml/login")
        : notConfigured("SAML"),
      disabled: !providers.saml,
    },
  ];

  return (
    <div className="grid min-h-screen w-full lg:grid-cols-2">
      {/* Left — Brand hero. Hidden on mobile to free up space for the form. */}
      <div className="relative hidden overflow-hidden bg-gradient-to-br from-primary/15 via-background to-primary/5 lg:block">
        {/* Faint dot grid for visual texture */}
        <div
          aria-hidden
          className="absolute inset-0 opacity-[0.4] [background-image:radial-gradient(circle,_var(--muted-foreground)_1px,_transparent_1px)] [background-size:18px_18px]"
        />
        <div className="relative z-10 flex h-full flex-col justify-between p-10">
          <Link href="/" className="inline-flex items-center gap-3 self-start">
            <Image
              src="/images/scicom-logo.png"
              alt="Scicom"
              width={140}
              height={36}
              priority
              className="h-9 w-auto select-none"
            />
            <span className="text-sm font-medium uppercase tracking-wide text-muted-foreground">
              Enterprise Template
            </span>
          </Link>
          <div className="max-w-md space-y-3">
            <h2 className="text-3xl font-semibold tracking-tight text-foreground">
              Everything your enterprise apps need, built in.
            </h2>
            <p className="text-sm text-muted-foreground">
              Role-based access control, single sign-on, and audit-ready user
              management — ready out of the box.
            </p>
          </div>
        </div>
      </div>

      {/* Right — Form. Carries its own logo on small screens since the hero is hidden. */}
      <div className="flex flex-col items-center justify-center bg-background px-6 py-10 sm:px-10">
        <div className="w-full max-w-sm">
          <Link href="/" className="mb-6 inline-flex items-center gap-2 lg:hidden">
            <Image
              src="/images/scicom-logo.png"
              alt="Scicom"
              width={120}
              height={32}
              priority
              className="h-8 w-auto select-none"
            />
            <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Enterprise Template
            </span>
          </Link>

          <div className="space-y-6">
            <div className="space-y-1">
              <h1 className="text-2xl font-semibold tracking-tight">Sign in</h1>
              <p className="text-sm text-muted-foreground">
                Welcome back to Enterprise Template.
              </p>
            </div>

            {error && (
              <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {error === "OAuthAccountNotLinked"
                  ? "This email is already linked to another sign-in method."
                  : "Sign-in failed. Please try again."}
              </div>
            )}

            <div className="grid grid-cols-2 gap-2">
              {ssoButtons.map((b) => (
                <Button
                  key={b.key}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={b.onClick}
                  title={b.disabled ? `${b.label} (not configured)` : b.label}
                  className={b.disabled ? "text-muted-foreground" : undefined}
                >
                  {b.icon}
                  {b.label}
                </Button>
              ))}
            </div>

            <div className="relative">
              <div className="absolute inset-0 flex items-center">
                <span className="w-full border-t border-border" />
              </div>
              <div className="relative flex justify-center text-[11px] uppercase tracking-wide">
                <span className="bg-background px-2 text-muted-foreground">
                  or with email
                </span>
              </div>
            </div>

            <form onSubmit={onSubmit} className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  required
                  autoFocus
                  autoComplete="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="name@company.com"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  required
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
                />
              </div>
              <Button type="submit" disabled={pending} className="w-full">
                {pending && <Loader2 className="h-4 w-4 animate-spin" />}
                Sign in
              </Button>
            </form>

            <p className="text-center text-xs text-muted-foreground">
              By continuing, you agree to the Terms and Privacy Policy.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
