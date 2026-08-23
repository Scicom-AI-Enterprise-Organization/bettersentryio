"use client";

import { useState, useTransition } from "react";
import { CheckCircle2, Plus, Send } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SelectBox } from "@/components/bsio/select-box";

export type ChannelActionState = { ok: boolean; message: string } | null;

export const CHANNEL_TYPE_LABEL: Record<string, string> = {
  teams: "Teams",
  slack: "Slack",
  webhook: "Generic webhook",
};

/**
 * Add a webhook, but only one that has been proven to work.
 *
 * A webhook URL is a capability that either works or silently does not, and the usual
 * way to find out which is an outage nobody was told about. So the test is a
 * precondition, not a nicety: Add stays closed until a probe has been delivered
 * through the live notification path to this exact type and URL.
 *
 * The pass is keyed on (type, URL) rather than being a boolean, because a boolean lets
 * you test one URL and save a different one — which is worse than never testing, since
 * it comes with a green tick.
 */
export function ChannelAddForm({
  title,
  addAction,
  testAction,
  slug,
}: {
  title: string;
  addAction: (prev: ChannelActionState, formData: FormData) => Promise<ChannelActionState>;
  testAction: (type: string, url: string) => Promise<ChannelActionState>;
  /** Present on a project's form, where the action needs to know which app. */
  slug?: string;
}) {
  const [name, setName] = useState("");
  const [type, setType] = useState("teams");
  const [url, setUrl] = useState("");
  const [passed, setPassed] = useState<{ type: string; url: string } | null>(null);
  const [testResult, setTestResult] = useState<ChannelActionState>(null);
  const [state, setState] = useState<ChannelActionState>(null);
  const [busy, start] = useTransition();
  const [testing, startTest] = useTransition();

  const trimmed = url.trim();
  const testable = trimmed.startsWith("https://");
  const proven = passed !== null && passed.type === type && passed.url === trimmed;

  function runTest() {
    startTest(async () => {
      const result = await testAction(type, trimmed);
      setTestResult(result);
      setPassed(result?.ok ? { type, url: trimmed } : null);
    });
  }

  // Submitted by hand rather than through `action={}`: the reset then happens where
  // the result is known, instead of in an effect watching for it. The gate is
  // client-side anyway, so there is no no-JS path to preserve.
  function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const formData = new FormData(e.currentTarget);
    start(async () => {
      const result = await addAction(null, formData);
      setState(result);
      if (!result?.ok) return;
      // A successful add empties the form, proof included: the next webhook is a
      // different webhook and has to earn its own pass.
      setName("");
      setUrl("");
      setPassed(null);
      setTestResult(null);
    });
  }

  return (
    <div className="rounded-xl border border-dashed border-border p-4">
      <p className="mb-3 text-sm font-medium">{title}</p>
      <form onSubmit={onSubmit} className="space-y-3">
        {slug && <input type="hidden" name="slug" value={slug} />}
        <input type="hidden" name="type" value={type} />

        <div className="flex flex-wrap items-center gap-2">
          <Input
            name="name"
            placeholder="name, e.g. sre-team-chat"
            className="w-52 text-sm"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <SelectBox
            aria-label="Channel type"
            className="w-44"
            value={type}
            onValueChange={setType}
          >
            {Object.entries(CHANNEL_TYPE_LABEL).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </SelectBox>
          <Input
            name="url"
            type="url"
            placeholder="https://….powerplatform.com/…/triggers/manual/paths/invoke?…"
            className="min-w-72 flex-1 font-mono text-xs"
            autoComplete="off"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            required
          />
        </div>

        {/* Reason on the left, actions on the right. `ml-auto` on the group rather than
            `justify-between` on the row: when the row wraps, the buttons stay together
            and stay right-aligned instead of one of them being flung to the far edge. */}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {/* A disabled button with no explanation reads as a broken page. */}
          <p className="text-xs text-muted-foreground">
            {!testable
              ? "Paste an https:// webhook URL, then send a test."
              : !proven
                ? "Send a test first — a card has to arrive before this can be saved."
                : !name.trim()
                  ? "Give it a name and it is ready to add."
                  : "Test delivered. Ready to add."}
          </p>
          <div className="ml-auto flex items-center gap-2">
            <Button
              type="button"
              variant={proven ? "outline" : "default"}
              size="sm"
              disabled={testing || !testable}
              onClick={runTest}
            >
              {proven ? <CheckCircle2 className="h-3.5 w-3.5" /> : <Send className="h-3.5 w-3.5" />}
              {testing ? "Sending…" : proven ? "Tested" : "Send test"}
            </Button>
            <Button type="submit" size="sm" disabled={busy || !proven || !name.trim()}>
              <Plus className="h-3.5 w-3.5" />
              {busy ? "Adding…" : "Add"}
            </Button>
          </div>
        </div>
      </form>

      {testResult && (
        <p
          className={`mt-2 text-sm ${testResult.ok ? "text-status-active" : "text-status-down"}`}
        >
          {testResult.message}
        </p>
      )}
      {state && (
        <p className={`mt-2 text-sm ${state.ok ? "text-status-active" : "text-status-down"}`}>
          {state.message}
        </p>
      )}
    </div>
  );
}
