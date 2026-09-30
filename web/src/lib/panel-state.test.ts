// VAPT SAST #48 (layout.tsx:25 reads, panel-state.ts:26 writes; unprotected cookie).
// The sidebar preference is written by document.cookie, so it cannot be HttpOnly; it
// must still be Secure and SameSite.
import { test } from "node:test";
import assert from "node:assert/strict";
import { NAV_PANEL_COOKIE, PROJECT_PANEL_COOKIE, panelCollapsed, rememberPanel } from "./panel-state";

function captureCookieWrites(): string[] {
  const writes: string[] = [];
  (globalThis as { document?: unknown }).document = {
    set cookie(v: string) {
      writes.push(v);
    },
  };
  return writes;
}

test("the preference cookie is written Secure and SameSite=Lax", () => {
  const writes = captureCookieWrites();
  rememberPanel(NAV_PANEL_COOKIE, true);
  rememberPanel(PROJECT_PANEL_COOKIE, false);
  assert.equal(writes.length, 2);
  for (const w of writes) {
    const attrs = w.split(";").map((a) => a.trim().toLowerCase());
    assert.ok(attrs.includes("secure"), `no Secure attribute: ${w}`);
    assert.ok(attrs.includes("samesite=lax"), `no SameSite=Lax: ${w}`);
    assert.ok(attrs.includes("path=/"), `no path: ${w}`);
  }
  assert.match(writes[0], /^bsio\.nav-collapsed=1;/);
  assert.match(writes[1], /^bsio\.project-collapsed=0;/);
});

test("only an explicit 1 reads as collapsed", () => {
  assert.equal(panelCollapsed("1"), true);
  for (const v of [undefined, "", "0", "true", "1; secure"]) assert.equal(panelCollapsed(v), false);
});
