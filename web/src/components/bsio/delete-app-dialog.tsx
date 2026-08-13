"use client";

import { useActionState } from "react";
import { Loader2, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { removeApp, type DeleteAppState } from "@/app/(app)/apps/actions";

/**
 * Deleting an app takes its monitors, their beat history and their incidents with it.
 * The counts are named in the prompt so the confirmation is informed rather than
 * reflexive — there is no undo.
 */
export function DeleteAppDialog({
  slug,
  name,
  monitors,
}: {
  slug: string;
  name: string;
  monitors: number;
}) {
  const [state, formAction, pending] = useActionState<DeleteAppState, FormData>(removeApp, {});

  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="text-muted-foreground hover:text-status-down">
          <Trash2 className="h-4 w-4" />
          Delete
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <form action={formAction}>
          <input type="hidden" name="slug" value={slug} />
          <DialogHeader>
            <DialogTitle>Delete {name}?</DialogTitle>
            <DialogDescription>
              This also deletes{" "}
              {monitors === 0
                ? "its ingest key"
                : `its ${monitors} ${monitors === 1 ? "monitor" : "monitors"}, their beat history and incidents, and its ingest key`}
              . Any service still using that key will start getting{" "}
              <span className="font-mono">403</span>. There is no undo.
            </DialogDescription>
          </DialogHeader>

          {state.error && <p className="py-3 text-sm text-status-down">{state.error}</p>}

          <DialogFooter>
            <Button type="submit" variant="destructive" disabled={pending}>
              {pending && <Loader2 className="h-4 w-4 animate-spin" />}
              Delete {name}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
