"use client";

import { useState, useTransition } from "react";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/bsio/confirm-dialog";
import { setUserRoles, deleteUser } from "./actions";

interface User {
  id: string;
  email: string;
  name: string | null;
  roles: string[];
}

export function UsersTable({ users, allRoles }: { users: User[]; allRoles: string[] }) {
  const [pending, start] = useTransition();
  // One dialog for the table, pointed at whichever row is pending — window.confirm drew
  // a white box outside the theme and could say nothing about what deleting a user costs.
  const [confirming, setConfirming] = useState<User | null>(null);

  function toggleRole(user: User, role: string, checked: boolean) {
    const next = checked ? [...user.roles, role] : user.roles.filter((r) => r !== role);
    start(async () => {
      try {
        await setUserRoles(user.id, next);
        toast.success("Roles updated");
      } catch {
        toast.error("Failed to update roles");
      }
    });
  }

  function onDelete(user: User) {
    setConfirming(null);
    start(async () => {
      try {
        await deleteUser(user.id);
        toast.success("User deleted");
      } catch {
        toast.error("Failed to delete user");
      }
    });
  }

  return (
    <>
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-muted-foreground">
            <th className="py-2 pr-4 font-medium">User</th>
            {allRoles.map((r) => (
              <th key={r} className="px-2 py-2 font-medium">{r}</th>
            ))}
            <th className="py-2 pl-4" />
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id} className="border-b border-border/50">
              <td className="py-3 pr-4">
                <div className="font-medium">{u.name ?? "—"}</div>
                <div className="text-xs text-muted-foreground">{u.email}</div>
              </td>
              {allRoles.map((r) => (
                <td key={r} className="px-2 py-3">
                  <Checkbox
                    checked={u.roles.includes(r)}
                    disabled={pending}
                    onCheckedChange={(v) => toggleRole(u, r, v === true)}
                  />
                </td>
              ))}
              <td className="py-3 pl-4 text-right">
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={pending}
                  onClick={() => setConfirming(u)}
                  aria-label="Delete user"
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>

    <ConfirmDialog
      open={confirming !== null}
      onOpenChange={(open) => !open && setConfirming(null)}
      title={`Delete ${confirming?.email ?? "this user"}?`}
      description={
        <>
          Their account and role assignments are removed. Anything they created stays, and
          they can be invited again — but they lose access immediately.
        </>
      }
      confirmLabel="Delete user"
      destructive
      pending={pending}
      onConfirm={() => confirming && onDelete(confirming)}
    />
    </>
  );
}
