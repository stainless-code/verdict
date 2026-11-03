#!/usr/bin/env node
// verdict wrapper shim: exec the platform package binary, nothing else.
// No downloads, no install scripts, no background work. A missing platform
// pair refuses with the repair command instead of fetching anything.
"use strict";

const { spawnSync } = require("node:child_process");

const REPAIR = "npm i -g @stainless-code/verdict@latest";
// Generated from release/contract.json by clients/ts/scripts/gen-ts-types.mjs
// (the typed copy the client imports is clients/ts/src/platforms.gen.ts).
const PACKAGES = require("./platforms.gen.json");

function refuse(code, detail) {
  process.stderr.write(`verdict: ${code}: ${detail}\nrepair: ${REPAIR}\n`);
  process.exit(1);
}

function resolvePair() {
  const key = `${process.platform}-${process.arch}`;
  const pkg = PACKAGES[key];
  if (pkg === undefined) {
    refuse("unsupported-platform", `no binary ships for ${key}`);
  }
  for (const sub of [`${pkg}/bin/verdict`, `${pkg}/bin/verdict.exe`]) {
    try {
      return require.resolve(sub);
    } catch {
      // Fall through to the next candidate below.
    }
  }
  refuse(
    "missing-platform-binary",
    `optional dependency ${pkg} is not installed for ${key}`,
  );
}

function main() {
  const child = spawnSync(resolvePair(), process.argv.slice(2), {
    stdio: "inherit",
  });
  if (child.error) {
    refuse("spawn-failed", child.error.message);
  }
  process.exit(child.status ?? 1);
}

main();
