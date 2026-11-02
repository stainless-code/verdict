#!/usr/bin/env node
// verdict wrapper shim: exec the platform package binary, nothing else.
// No downloads, no install scripts, no background work. A missing platform
// pair refuses with the repair command instead of fetching anything.
"use strict";

const { spawnSync } = require("node:child_process");

const REPAIR = "npm i -g @stainless-code/verdict@latest";
const PACKAGES = {
  "darwin-x64": "@stainless-code/verdict-darwin-x64",
  "darwin-arm64": "@stainless-code/verdict-darwin-arm64",
  "linux-x64": "@stainless-code/verdict-linux-x64",
  "linux-arm64": "@stainless-code/verdict-linux-arm64",
  "win32-x64": "@stainless-code/verdict-win32-x64",
  "win32-arm64": "@stainless-code/verdict-win32-arm64",
};

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
