import Link from "next/link";
import { AlertTriangle, ArrowRight, Bug, Gauge, Siren } from "lucide-react";

import { requireUser } from "@/lib/rbac";
import { getApps } from "@/lib/bsio";
import { Button } from "@/components/ui/button";
import { CodeBlock } from "@/components/bsio/code-block";

export const dynamic = "force-dynamic";
export const metadata = { title: "How it works" };

/**
 * The instrumentation guide.
 *
 * It exists because the three Issues views are not three features to switch on — they
 * are three consequences of what you pass to one function. Someone who has read the
 * Setup page still does not know why their loop never reports STALLED (they passed a
 * loop counter as progress) or why it pages at 3am (grace too tight). That is what this
 * page answers, and it is deliberately prose rather than a wizard.
 */
export default async function LearnPage() {
  await requireUser();
  const apps = await getApps();
  const example = apps.ok ? apps.data.apps.find((a) => a.connected) : undefined;

  return (
    <div className="max-w-3xl space-y-10 pb-16">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">How it works</h1>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          Production breaks in two ways. The loud way: an exception is raised somewhere — that is{" "}
          <span className="font-medium text-foreground">error tracking</span>, and you get it by
          pointing the official <Code>sentry_sdk</Code> at this platform, same init as sentry.io.
          And the quiet way: nothing is raised at all. Every uptime checker asks your service{" "}
          <em>are you there?</em> from the outside, and that question was answered{" "}
          <span className="font-medium text-foreground">yes</span> for two days while our TTS API
          produced nothing — the HTTP server was fine and the batching loop was dead. For that,
          bettersentryio asks a different question, from the inside: <em>is the loop still doing
          work?</em>
        </p>
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
          Two instruments, one page. Errors are one <Code>sentry_sdk.init()</Code>. The monitor
          views are three consequences of the arguments you pass to one beat call.
        </p>
      </header>

      {/* ---- errors: the sentry_sdk half ----------------------------------- */}
      <section id="errors" className="scroll-mt-6 space-y-3">
        <div className="flex items-center gap-2">
          <Bug className="h-4 w-4 text-status-down" />
          <h2 className="text-lg font-semibold tracking-tight">
            Errors — the official sentry_sdk
          </h2>
        </div>
        <p className="text-sm font-medium">
          Exceptions are reported by the stock sentry_sdk. Nothing of ours to install or import —
          only the DSN differs from sentry.io.
        </p>
        <div className="space-y-3 text-sm leading-relaxed text-muted-foreground">
          <CodeBlock
            filename="main.py"
            language="python"
            code={`import sentry_sdk

sentry_sdk.init(
    dsn="https://<ingest key>@bsio-ingest.aies.scicom.dev/<project id>",
    environment="production",
    traces_sample_rate=0,   # errors only; transactions are dropped server-side
    send_default_pii=True,
)`}
          />
          <p>
            Your project&apos;s real DSN is on its{" "}
            <span className="font-medium text-foreground">Setup</span> page, ready to copy. From
            that one init, everything the SDK captures lands here: every unhandled exception,
            every <Code>logger.error(...)</Code>, and anything you pass to{" "}
            <Code>capture_exception</Code> or <Code>capture_message</Code> at any of the five
            levels — with locals, source lines, breadcrumbs and request data intact.
          </p>
          <p>
            The engine groups events into issues by stack fingerprint, keeps issues separate per
            environment, and sends a Teams card for every{" "}
            <span className="font-medium text-foreground">new issue</span> and every{" "}
            <span className="font-medium text-foreground">regression</span> — an issue you
            resolved that starts happening again. Triage lives in Errors &amp; Outages: resolve,
            archive, prioritise, or walk the stored events one by one.
          </p>
          <CodeBlock
            filename="prove it once"
            language="python"
            code={`@app.get("/sentry-debug")
async def trigger_error():
    return 1 / 0   # shows up as a grouped issue within seconds`}
          />
          <Advice title="Background tasks under uvicorn">
            <Code>AsyncioIntegration</Code> silently does nothing unless it is attached from
            inside the running event loop — uvicorn imports your module outside it. Call{" "}
            <Code>patch_asyncio()</Code> from a startup hook, or a dying background task is an
            error you never see. The Setup page snippet includes this.
          </Advice>
          <Advice title="Errors only, by design">
            Keep <Code>traces_sample_rate=0</Code>. Performance transactions, sessions and
            profiles are accepted and dropped server-side so the SDK never breaks — but they buy
            you nothing here. This platform is the error half of Sentry plus the loop monitoring
            Sentry does not have.
          </Advice>
        </div>
      </section>

      {/* ---- the one call ------------------------------------------------- */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">
          Loops — one beat call, three detections
        </h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The second instrument. An exception can only be reported if one is raised — a dead or
          frozen loop raises nothing, so no error tracker will ever see it. Beats catch what
          sentry_sdk cannot.
        </p>
        <CodeBlock
          filename="the whole API"
          language="python"
          code={`bsio.beat(
    "tts-batcher",       # monitor name — created on its first beat
    progress=batches,    # a counter that only goes up
    every=30,            # seconds you expect between beats
    grace=30,            # extra slack before it is an outage
    stall_window=180,    # frozen progress before it is a stall
)`}
        />
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">View</th>
                <th className="px-4 py-2 font-medium">Means</th>
                <th className="px-4 py-2 font-medium">Driven by</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              <tr>
                <td className="whitespace-nowrap px-4 py-2.5">
                  <span className="inline-flex items-center gap-1.5 font-medium">
                    <Siren className="h-3.5 w-3.5 text-status-down" />
                    Errors &amp; Outages
                  </span>
                </td>
                <td className="px-4 py-2.5 text-muted-foreground">
                  An exception arrived, or the beats stopped.
                </td>
                <td className="px-4 py-2.5 font-mono text-xs">sentry_sdk · every + grace</td>
              </tr>
              <tr>
                <td className="whitespace-nowrap px-4 py-2.5">
                  <span className="inline-flex items-center gap-1.5 font-medium">
                    <Gauge className="h-3.5 w-3.5 text-status-idle" />
                    Breached Metrics
                  </span>
                </td>
                <td className="px-4 py-2.5 text-muted-foreground">
                  The beats continue; the work stopped.
                </td>
                <td className="px-4 py-2.5 font-mono text-xs">progress + stall_window</td>
              </tr>
              <tr>
                <td className="whitespace-nowrap px-4 py-2.5">
                  <span className="inline-flex items-center gap-1.5 font-medium">
                    <AlertTriangle className="h-3.5 w-3.5 text-status-idle" />
                    Warnings
                  </span>
                </td>
                <td className="px-4 py-2.5 text-muted-foreground">Overdue, not yet an outage.</td>
                <td className="px-4 py-2.5 font-mono text-xs">grace</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p className="text-sm text-muted-foreground">
          Omit <Code>progress</Code> and you get liveness only — the first and third rows. That
          is the setup most tools stop at, and it is the one that was green for two days.
        </p>
      </section>

      {/* ---- outages ------------------------------------------------------ */}
      <Guide
        id="outages"
        icon={<Siren className="h-4 w-4 text-status-down" />}
        title="Errors & Outages"
        lede="Two feeds share this view: error issues reported by sentry_sdk, and outages detected when heartbeats stop arriving."
      >
        <p>
          The errors half needs no configuration beyond the <Code>sentry_sdk.init()</Code> above —
          grouped issues appear here with their full triage workflow. The outages half is the
          beat deadline:
        </p>
        <p>
          Each beat records when the next one is due: <Code>last_beat + every + grace</Code>. A
          sweep runs every few seconds looking for monitors past that deadline. Nothing in your
          service has to notice or report the failure — the <em>absence</em> is the signal, which
          is why this still fires when the process is killed outright.
        </p>
        <p>
          The monitor goes <Code>LATE</Code> once it is past <Code>every</Code>, then{" "}
          <Code>MISSING</Code> once it is past <Code>grace</Code> as well, and an incident opens.
          It recovers the moment a beat arrives.
        </p>
        <CodeBlock
          filename="liveness only"
          language="python"
          code={`from bettersentryio import Beat

bsio = Beat()  # reads BSIO_URL and BSIO_KEY from the environment

while True:
    do_one_pass()
    bsio.beat("nightly-export", every=3600, grace=600)`}
        />
        <Advice title="Choosing every and grace">
          Set <Code>every</Code> to what the loop actually achieves on a normal day, not to what
          you hope for. Set <Code>grace</Code> to the longest legitimate hiccup you would not
          want to be woken for — a slow batch, a deploy, a broker reconnect. Too tight and you
          teach people to ignore the alerts; too loose and you find out late.
        </Advice>
        <Advice title="Cron jobs">
          Beat only on success, so a failing script reads as MISSING rather than quietly passing:{" "}
          <Code>{`your-job.sh && curl -fsS "$BSIO_URL/api/0/beat/..."`}</Code>
        </Advice>
      </Guide>

      {/* ---- breached ----------------------------------------------------- */}
      <Guide
        id="breached"
        icon={<Gauge className="h-4 w-4 text-status-idle" />}
        title="Breached Metrics"
        lede="Heartbeats are still arriving, but the progress counter has not moved. The loop is alive and doing nothing."
      >
        <p>
          This is the case no health check can see, and the reason this project exists. A{" "}
          <Code>torch.compile</Code> shape mismatch, a wedged model call, a consumer whose broker
          connection died silently — the loop keeps spinning, the server keeps answering{" "}
          <Code>200</Code>, and no exception is ever raised.
        </p>
        <p>
          Passing <Code>progress</Code> is what makes it detectable. If beats keep arriving while
          that number stands still for <Code>stall_window</Code> seconds, the monitor goes{" "}
          <Code>STALLED</Code> and an incident opens. Only the counter moving clears it —{" "}
          <span className="font-medium text-foreground">a beat alone does not</span>, otherwise a
          stalled loop would flap between alert and all-clear on every heartbeat.
        </p>
        <CodeBlock
          filename="liveness + stall detection"
          language="python"
          code={`batches_done = 0

while True:
    await run_one_batch()
    batches_done += 1          # count the WORK, not the iterations

    bsio.beat(
        "tts-batcher",
        progress=batches_done,
        every=30,
        stall_window=180,      # 3 missed batches before we call it
    )`}
        />
        <Advice title="What makes a good progress counter">
          Something that only grows and that only grows{" "}
          <span className="font-medium text-foreground">because real work finished</span>: batches
          synthesised, rows exported, tokens generated, messages acked, frames encoded. Read it
          after the work, in the same iteration.
        </Advice>
        <Advice title="What does not work" tone="bad">
          A loop counter (<Code>i += 1</Code>) increments happily while the model does nothing —
          it makes stall detection useless while looking correct. Neither does a gauge that can
          go down (queue depth, memory, temperature): the engine compares against the last value
          it saw, so a falling number reads as no progress. Wrap those in a counter of{" "}
          <em>completions</em> instead.
        </Advice>
        <Advice title="Choosing stall_window">
          Two or three times the interval between units of work, so one slow batch is not an
          incident but a stopped pipeline is. For a loop that produces something every 30s, 120s
          to 180s is right. Pass <Code>-1</Code> to disable it for a monitor that legitimately
          idles.
        </Advice>
      </Guide>

      {/* ---- warnings ----------------------------------------------------- */}
      <Guide
        id="warnings"
        icon={<AlertTriangle className="h-4 w-4 text-status-idle" />}
        title="Warnings"
        lede="Overdue, but still inside the grace window. Nothing has broken yet."
      >
        <p>
          There is nothing to configure here. <Code>LATE</Code> is the state between{" "}
          <Code>every</Code> and <Code>every + grace</Code> — the beat is overdue but the engine
          has not given up on it. Widening <Code>grace</Code> widens this window.
        </p>
        <p>
          It is worth watching rather than alerting on. A loop that spends most of its life LATE
          is telling you <Code>every</Code> is set to what you hoped for rather than what it
          does, and it will eventually cross into a real outage on a bad day.
        </p>
        <Advice title="Reading this list">
          Persistent entries mean your timings are wrong, not that your service is broken. Raise{" "}
          <Code>every</Code> to match reality, and keep <Code>grace</Code> for genuine hiccups.
        </Advice>
      </Guide>

      {/* ---- where the beat goes ------------------------------------------ */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">Where the beat goes</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Placement matters more than any parameter. The beat has to sit{" "}
          <span className="font-medium text-foreground">downstream of the thing that can stop</span>
          . Every mistake below reports healthy through exactly the failure you were trying to
          catch.
        </p>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Instead of</th>
                <th className="px-4 py-2 font-medium">Why it lies</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {[
                [
                  "Beating at the top of the loop",
                  "Reports before the work is attempted, so a call that hangs forever still looks healthy.",
                ],
                [
                  "Beating from a background timer or thread",
                  "The timer outlives the loop. This is a heartbeat for the process, which is what /health already told you.",
                ],
                [
                  "Beating from a middleware or request handler",
                  "Measures traffic, not work. No requests over a quiet weekend then looks identical to a dead pipeline.",
                ],
                [
                  "One monitor for several loops",
                  "Whichever loop survives keeps the monitor green. One name per loop.",
                ],
              ].map(([a, b]) => (
                <tr key={a}>
                  <td className="px-4 py-2.5 align-top font-medium">{a}</td>
                  <td className="px-4 py-2.5 text-muted-foreground">{b}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="text-sm text-muted-foreground">
          The client is built for this position: it never raises and never blocks the caller, so a
          monitoring call inside your hot loop cannot become the outage. Failures are counted and
          visible via <Code>bsio.stats()</Code>.
        </p>
      </section>

      {/* ---- worked numbers ------------------------------------------------ */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">Starting numbers</h2>
        <p className="text-sm text-muted-foreground">
          Reasonable defaults to adjust once you have seen a week of real behaviour.
        </p>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Workload</th>
                <th className="px-4 py-2 font-medium">every</th>
                <th className="px-4 py-2 font-medium">grace</th>
                <th className="px-4 py-2 font-medium">stall_window</th>
                <th className="px-4 py-2 font-medium">progress</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border font-mono text-xs">
              {[
                ["TTS / inference batcher", "30", "30", "180", "batches done"],
                ["Queue worker", "60", "60", "300", "tasks completed"],
                ["Streaming consumer", "15", "30", "120", "messages acked"],
                ["Training loop", "300", "300", "1800", "steps completed"],
                ["Nightly export (cron)", "86400", "3600", "-1", "rows written"],
              ].map((row) => (
                <tr key={row[0]}>
                  <td className="px-4 py-2.5 font-sans text-sm">{row[0]}</td>
                  <td className="px-4 py-2.5">{row[1]}</td>
                  <td className="px-4 py-2.5">{row[2]}</td>
                  <td className="px-4 py-2.5">{row[3]}</td>
                  <td className="px-4 py-2.5 font-sans text-muted-foreground">{row[4]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="text-sm text-muted-foreground">
          The nightly export sets <Code>stall_window=-1</Code> because it is idle by design 23
          hours a day. Its <Code>progress</Code> still matters: a job that runs on time and
          exports zero rows is caught when the number fails to move between runs.
        </p>
      </section>

      {/* ---- prove it ------------------------------------------------------ */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">Prove it once</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Monitoring you have never seen fire is a guess. Break each one deliberately, on a
          service where it is safe, and watch the view fill in — it takes about a minute with a
          short <Code>every</Code>.
        </p>
        <CodeBlock
          filename="terminal"
          language="bash"
          code={`# Errors & Outages: stop beating. LATE, then MISSING.
#   kill the loop, leave the server up

# Breached Metrics: keep beating, stop incrementing progress.
#   this is the one worth seeing — /health stays 200 the whole time

# Warnings: beat slower than 'every' but inside 'grace'.`}
        />
        <p className="text-sm text-muted-foreground">
          <Code>examples/fastapi-tts</Code> in the repo does all three on demand
          (<Code>/break/freeze</Code>, <Code>/break/kill</Code>, <Code>/fix</Code>) next to a
          deliberately naive <Code>/health</Code>, so you can watch them disagree.
        </p>
      </section>

      {/* ---- reference ----------------------------------------------------- */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">Parameter reference</h2>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Parameter</th>
                <th className="px-4 py-2 font-medium">Default</th>
                <th className="px-4 py-2 font-medium">Meaning</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {[
                ["monitor", "required", "Name of the loop. Created on its first beat; one name per loop."],
                ["every", "60", "Seconds you expect between beats."],
                ["grace", "= every (min 30)", "Extra seconds before MISSING fires."],
                [
                  "stall_window",
                  "3 × every (min 120)",
                  "Seconds of frozen progress before STALLED fires. -1 disables it.",
                ],
                ["progress", "none", "Monotonic counter of completed work. Without it, no stall detection."],
                ["env", "production", "Environment name. Separates staging from production on the same monitor."],
              ].map(([p, d, m]) => (
                <tr key={p}>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs">{p}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-muted-foreground">
                    {d}
                  </td>
                  <td className="px-4 py-2.5 text-muted-foreground">{m}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="flex flex-wrap gap-2 border-t border-border pt-6">
        {example ? (
          <Button asChild size="sm">
            <Link href={`/apps/${example.slug}/setup`}>
              Snippets for {example.name}
              <ArrowRight className="h-3.5 w-3.5" />
            </Link>
          </Button>
        ) : (
          <Button asChild size="sm">
            <Link href="/apps/new">Create a project</Link>
          </Button>
        )}
        {example && (
          <Button asChild variant="outline" size="sm">
            <Link href={`/apps/${example.slug}/issues/breached`}>See Breached Metrics</Link>
          </Button>
        )}
      </section>
    </div>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] text-foreground">
      {children}
    </code>
  );
}

function Guide({
  id,
  icon,
  title,
  lede,
  children,
}: {
  id: string;
  icon: React.ReactNode;
  title: string;
  lede: string;
  children: React.ReactNode;
}) {
  return (
    <section id={id} className="scroll-mt-6 space-y-3">
      <div className="flex items-center gap-2">
        {icon}
        <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      </div>
      <p className="text-sm font-medium">{lede}</p>
      <div className="space-y-3 text-sm leading-relaxed text-muted-foreground [&_p]:text-sm">
        {children}
      </div>
    </section>
  );
}

function Advice({
  title,
  tone,
  children,
}: {
  title: string;
  tone?: "bad";
  children: React.ReactNode;
}) {
  return (
    <div
      className={
        tone === "bad"
          ? "rounded-lg border border-status-down/30 bg-status-down/[0.04] p-3"
          : "rounded-lg border border-border bg-muted/30 p-3"
      }
    >
      <p className="text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{children}</p>
    </div>
  );
}
