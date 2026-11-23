import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// The runnable examples under examples/typescript-client are the documented
// path through the mutating verbs, so they are executed here rather than
// trusted to compile: each one builds its own sandbox, calls the shipped
// client, spawns this checkout's binary, and prints one `ok:` line per
// checkpoint. A stale example fails the TS client gate that runs this file.

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", "..", "..");
const EXAMPLES = join(ROOT, "examples", "typescript-client");

// The checkpoints each example must print, in order. An example that stops
// proving one fails here; one that grows a checkpoint nobody recorded fails
// too, because the printed set is compared exactly.
const CHECKPOINTS = {
  "config.ts": [
    "config --explain renders the stored policy and its origins",
    "config --preview resolves a proposed file and writes nothing",
    "config --preview refuses a bad proposal with a typed bad-config",
    "config without a mode is an exit-2 usage error, not an envelope",
  ],
  "post-dry-run.ts": [
    "post --dry-run previews one payload and posts nothing",
    "post refuses a local bundle with a typed no-pr-target and zero writes",
  ],
  "record.ts": [
    "record --dry-run previews the merge and writes nothing",
    "record writes findings.json and record.json",
    "record refuses a provider PR bundle with a typed not-local",
    "record refuses a malformed review with typed problems",
  ],
};

// The examples pin the binary through VERDICT_BIN, so the one this suite runs
// is the one `make build` wrote — never a different verdict on PATH.
const configured = process.env["VERDICT_BIN"];
const BIN =
  configured !== undefined && configured !== ""
    ? configured
    : join(ROOT, "bin", "verdict");
const skip = existsSync(BIN) ? false : `no binary at ${BIN}: run make build`;

function runExample(file) {
  return spawnSync(process.execPath, [join(EXAMPLES, file)], {
    cwd: ROOT,
    encoding: "utf8",
    env: { ...process.env, VERDICT_BIN: BIN },
    timeout: 120_000,
  });
}

describe("examples: the mutating verbs, run end to end", () => {
  for (const [file, checkpoints] of Object.entries(CHECKPOINTS)) {
    it(
      `examples/typescript-client/${file} prints every checkpoint`,
      { skip },
      () => {
        const result = runExample(file);
        const output = `${result.stdout ?? ""}${result.stderr ?? ""}`;
        assert.equal(
          result.status,
          0,
          `${file} exited ${result.status}\n--- output ---\n${output}`,
        );
        const printed = output
          .split("\n")
          .filter((line) => line.startsWith("ok: "))
          .map((line) => line.slice("ok: ".length));
        assert.deepEqual(
          printed,
          checkpoints,
          `${file} printed a different checkpoint set\n--- output ---\n${output}`,
        );
      },
    );
  }
});
