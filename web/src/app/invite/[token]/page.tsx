import Link from "next/link";
import { auth } from "@/lib/auth";
import { prisma } from "@/lib/db";
import { Button } from "@/components/ui/button";
import { InviteSignupForm } from "./signup-form";

export default async function InvitePage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = await params;
  const session = await auth();

  const invite = await prisma.invitation.findUnique({
    where: { token },
    include: { role: true },
  });

  if (!invite) return <InviteState title="Invalid invitation" body="This link is not recognized." />;
  if (invite.revokedAt) return <InviteState title="Invitation revoked" body="The owner revoked this invite." />;
  if (invite.acceptedAt) return <InviteState title="Already accepted" body="This invitation has already been used." />;
  if (invite.expiresAt && invite.expiresAt < new Date()) {
    return <InviteState title="Invitation expired" body="Ask the sender to issue a new link." />;
  }

  if (!session?.user?.id) {
    // No session is the NORMAL case: the invitee does not have an account yet
    // — this page is where it gets created. The old redirect-to-login was a
    // dead end under credentials auth (nothing to sign in AS).
    return (
      <div className="flex min-h-screen flex-col items-center justify-center px-4 text-center">
        <h1 className="text-3xl font-bold tracking-tight">You&apos;re invited</h1>
        <p className="mt-3 max-w-md text-muted-foreground">
          Create your bettersentryio account to accept this invitation
          {invite.role ? ` (role: ${invite.role.name})` : ""}.
        </p>
        <InviteSignupForm token={token} email={invite.email} roleName={invite.role?.name ?? null} />
      </div>
    );
  }

  if (invite.email && invite.email.toLowerCase() !== session.user.email?.toLowerCase()) {
    return (
      <InviteState
        title="This invite is for a different email"
        body={`Sign in as ${invite.email} to accept it.`}
      />
    );
  }

  await prisma.$transaction(async (tx) => {
    if (invite.roleId) {
      await tx.userRole.upsert({
        where: { userId_roleId: { userId: session.user.id, roleId: invite.roleId } },
        update: {},
        create: { userId: session.user.id, roleId: invite.roleId },
      });
    }
    await tx.invitation.update({
      where: { id: invite.id },
      data: { acceptedAt: new Date(), acceptedById: session.user.id },
    });
  });

  return (
    <InviteState
      title="Welcome aboard"
      body={
        invite.role
          ? `You've been added with the "${invite.role.name}" role.`
          : "Your invitation has been accepted."
      }
      cta={
        <Button asChild>
          <Link href="/dashboard">Go to dashboard</Link>
        </Button>
      }
    />
  );
}

function InviteState({
  title,
  body,
  cta,
}: {
  title: string;
  body: string;
  cta?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-4 text-center">
      <h1 className="text-3xl font-bold tracking-tight">{title}</h1>
      <p className="mt-3 max-w-md text-muted-foreground">{body}</p>
      <div className="mt-6">
        {cta ?? (
          <Button asChild variant="outline">
            <Link href="/">Back home</Link>
          </Button>
        )}
      </div>
    </div>
  );
}
