// VAPT SAST #41 (seed.ts:51, client privacy violation): the seed had a built-in admin
// password (admin1234) and printed whichever one it used.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

// `npm test` runs from web/, which is where the seed and its tsx live.
const web = process.cwd();

test("the seed refuses to run without SEED_ADMIN_PASSWORD, before touching the database", () => {
  const env = { ...process.env };
  delete env.SEED_ADMIN_PASSWORD;
  // Nothing listens here: if the seed reached the database first, the failure would be
  // a connection error rather than the password check.
  env.DATABASE_URL = "postgresql://nobody@127.0.0.1:1/none?schema=auth";

  const run = spawnSync(path.join(web, "node_modules/.bin/tsx"), ["prisma/seed.ts"], {
    cwd: web,
    env,
    encoding: "utf8",
    timeout: 60_000,
  });
  const out = `${run.stdout}\n${run.stderr}`;
  assert.notEqual(run.status, 0, "seed exited 0 without a password");
  assert.match(out, /SEED_ADMIN_PASSWORD is not set/);
  assert.doesNotMatch(out, /admin1234/);
  assert.doesNotMatch(out, /Can't reach database|ECONNREFUSED/);
});

test("the seed source has no fallback password and never logs the one it uses", () => {
  const src = readFileSync(path.join(web, "prisma/seed.ts"), "utf8");
  assert.doesNotMatch(src, /admin1234/);
  for (const line of src.split("\n").filter((l) => /console\.(log|info|warn|error)/.test(l))) {
    assert.doesNotMatch(line, /adminPassword|SEED_ADMIN_PASSWORD/, line.trim());
  }
});
