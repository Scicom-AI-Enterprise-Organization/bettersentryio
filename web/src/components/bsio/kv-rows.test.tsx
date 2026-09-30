// VAPT SAST #38 (errors/[id]/page.tsx:339, reflected XSS): what the event page actually
// renders for attacker-supplied request headers and environment values.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToStaticMarkup } from "react-dom/server";
import { KVRows } from "./kv-rows";

const hostile: [string, string][] = [
  ["X-Js", "javascript:alert(1)"],
  ["X-Js-Tab", "java\tscript:alert(2)"],
  ["X-Data", "data:text/html,<script>alert(3)</script>"],
  ["X-Html", "<img src=x onerror=alert(4)>"],
  ["Referer", "https://ok.example.test/from"],
];

function hrefs(html: string): string[] {
  return [...html.matchAll(/href="([^"]*)"/g)].map((m) => m[1]);
}

test("no script-bearing value reaches an href", () => {
  const html = renderToStaticMarkup(<KVRows rows={hostile} />);
  assert.deepEqual(hrefs(html), ["https://ok.example.test/from"]);
});

test("markup in a value is escaped text, not HTML", () => {
  const html = renderToStaticMarkup(<KVRows rows={hostile} />);
  assert.ok(!html.includes("<img src=x"), "raw <img> must not be emitted");
  assert.ok(html.includes("&lt;img src=x onerror=alert(4)&gt;"));
  assert.ok(!html.includes("<script>"));
});

// VAPT SAST #52 (errors/[id]/page.tsx:484, unsafe target=_blank): every link that opens a
// new tab gives the opened page no handle back to the console.
test("new-tab links carry rel=noopener noreferrer", () => {
  const html = renderToStaticMarkup(
    <KVRows
      rows={[
        ["Referer", "https://ok.example.test/from"],
        ["Docs", "http://docs.example.test/"],
      ]}
    />,
  );
  const anchors = [...html.matchAll(/<a [^>]*>/g)].map((m) => m[0]);
  assert.equal(anchors.length, 2);
  for (const a of anchors) {
    assert.match(a, /target="_blank"/);
    const rel = /rel="([^"]*)"/.exec(a)?.[1].split(/\s+/) ?? [];
    assert.ok(rel.includes("noopener") && rel.includes("noreferrer"), a);
  }
});
