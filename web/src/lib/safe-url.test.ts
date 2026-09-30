// VAPT SAST #38 (errors/[id]/page.tsx:339, reflected XSS): event values arrive through a
// public ingest key, so the only way one may become a link is as a parsed http(s) URL.
import { test } from "node:test";
import assert from "node:assert/strict";
import { externalHttpUrl } from "./safe-url";

test("http(s) URLs come back as the parser's serialisation", () => {
  assert.equal(externalHttpUrl("https://example.com/a?b=1"), "https://example.com/a?b=1");
  assert.equal(externalHttpUrl("  HTTP://Example.com/x "), "http://example.com/x");
});

test("script-bearing and non-http schemes are never linkable", () => {
  for (const hostile of [
    "javascript:alert(1)",
    " JAVASCRIPT:alert(1)",
    "java\tscript:alert(1)",
    "java\nscript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "vbscript:msgbox(1)",
    "file:///etc/passwd",
    "//evil.example/x",
    "http://x\njavascript:alert(1)",
    "not a url",
    "",
  ]) {
    assert.equal(externalHttpUrl(hostile), null, JSON.stringify(hostile));
  }
});
