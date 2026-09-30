// VAPT SAST #42 (app-watcher.tsx:50, missing HSTS) and the header set the console now
// sends on every route. The engine side (clients.go) is covered in Go by
// TestSecurityHeadersOnEveryResponse.
import { test } from "node:test";
import assert from "node:assert/strict";

async function productionConfig() {
  // The CSP differs in dev (eval for React Refresh); assert what ships.
  const prev = process.env.NODE_ENV;
  (process.env as Record<string, string>).NODE_ENV = "production";
  try {
    return (await import("../../next.config")).default;
  } finally {
    (process.env as Record<string, string | undefined>).NODE_ENV = prev;
  }
}

test("every route carries HSTS and the frame/sniff/referrer headers", async () => {
  const config = await productionConfig();
  const rules = await config.headers!();
  const all = rules.find((r) => r.source === "/:path*");
  assert.ok(all, "no header rule covers every path");
  const h = Object.fromEntries(all.headers.map((x) => [x.key.toLowerCase(), x.value]));

  const hsts = /max-age=(\d+)/.exec(h["strict-transport-security"] ?? "");
  assert.ok(hsts && Number(hsts[1]) >= 31536000, `HSTS: ${h["strict-transport-security"]}`);
  assert.equal(h["x-frame-options"], "DENY");
  assert.equal(h["x-content-type-options"], "nosniff");
  assert.equal(h["referrer-policy"], "strict-origin-when-cross-origin");
});

test("the production CSP forbids framing and eval, and the framework banner is off", async () => {
  const config = await productionConfig();
  const rules = await config.headers!();
  const csp = rules[0].headers.find((x) => x.key === "Content-Security-Policy")?.value ?? "";
  assert.match(csp, /frame-ancestors 'none'/);
  assert.match(csp, /object-src 'none'/);
  assert.doesNotMatch(csp, /unsafe-eval/);
  assert.equal(config.poweredByHeader, false);
});
