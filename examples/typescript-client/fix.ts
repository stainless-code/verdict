// fix: one bounded pass over a bundle. A single pass prints the top
// open inline finding with its KB context and writes the pass ledger; --all
// hands out one ready row per open finding but refuses without the command
// that proves the passes. Nothing is applied or pushed; the worktree diff it
// would write lands beside the ledger only when the tree is dirty.
//
//   make build && make clients-ts
//   node examples/typescript-client/fix.ts

import { existsSync } from "node:fs";
import { join } from "node:path";
import { createClient } from "../../clients/ts/dist/index.js";
import {
  expect,
  expectMatch,
  expectPresent,
  expectRefusal,
  line,
  ok,
} from "./lib/expect.ts";
import { LOCAL_SLUG, REPO, bundleDir, createSandbox } from "./lib/sandbox.ts";

const sandbox = createSandbox();
try {
  // `bin` pins the binary this checkout built. Without it the client would
  // resolve VERDICT_BIN → the platform package → PATH, which on a machine with
  // an older `verdict` installed silently exercises that one instead.
  const verdict = createClient({
    bin: sandbox.bin,
    cwd: sandbox.cwd,
    env: sandbox.env,
    stderr: "pipe",
  });
  const bundle = join(sandbox.cwd, bundleDir(LOCAL_SLUG));

  // Fix consumes what a record landed, so the finding is recorded first.
  const recorded = await verdict.record([REPO, LOCAL_SLUG]);
  expect(recorded.data.findings.open, 1, "the record left one finding open");

  console.log(`fix ${REPO} ${LOCAL_SLUG}`);
  const single = await verdict.fix([REPO, LOCAL_SLUG]);
  const anchors = expectPresent(
    single.data.anchors,
    "a single pass carries its anchors",
  );
  const kb = expectPresent(single.data.kb, "a single pass carries its kb");
  line("sig", single.data.sig);
  line("locator", single.data.locator);
  line("severity", single.data.severity);
  line("pass", single.data.pass);
  line("ledger", single.data.ledger);
  line("next", single.next);

  expectMatch(
    expectPresent(single.data.sig, "a single pass names its sig"),
    /^[0-9a-f]{40}$/,
    "the sig is the 40-hex finding identity the ledger keys on",
  );
  expect(
    single.data.locator,
    "app/handler.go:5",
    "the pass names the finding's anchor line",
  );
  expect(single.data.severity, "important", "the finding is important");
  expect(
    single.data.body,
    "Return the wrapped error so the caller keeps the cause.",
    "the pass prints the finding's claim",
  );
  expect(anchors.path, "app/handler.go", "the anchor names the file");
  expect(anchors.line, "5", "the anchor names the line");
  expect(anchors.side, "RIGHT", "the anchor is on the new side");
  expect(single.data.pass, 1, "the first pass consumes one of three");
  expect(single.data.max_passes, 3, "a sig gets at most three passes");
  expect(
    single.data.ledger,
    ".verdict/state/local-demo/fix.json",
    "the envelope names the ledger it wrote",
  );
  expect(
    (kb as { file?: unknown }).file,
    ".verdict/state/local-demo/kb.txt",
    "the pass cites the KB context file",
  );
  expect(
    single.next,
    "patch the anchor file, re-run fetch-local, run verdict verify, write verification.json, run record to land it, then re-run fix",
    "the envelope names the verify/land/re-run steps",
  );
  expect(
    existsSync(join(bundle, "fix.json")),
    true,
    "the pass wrote its ledger",
  );
  // The fixture tree is clean, so the worktree diff has nothing to hold.
  expect(single.data.patch ?? null, null, "a clean tree writes no patch");
  expect(
    existsSync(join(bundle, "fix.patch")),
    false,
    "no fix.patch sits beside the ledger yet",
  );
  ok("fix prints one pass over the open finding and writes the ledger");

  console.log(`fix ${REPO} ${LOCAL_SLUG} --all --verify-cmd true`);
  const batch = await verdict.fix([
    REPO,
    LOCAL_SLUG,
    "--all",
    "--verify-cmd",
    "true",
  ]);
  const rows = expectPresent(batch.data.batch, "a batch carries its rows");
  const row = expectPresent(rows[0], "the batch holds one row");
  line("open_inline", batch.data.open_inline);
  line("batch", batch.data.batch);
  line("next", batch.next);

  expect(batch.data.open_inline, 1, "one finding is open and inline");
  expect(batch.data.fixed, 0, "nothing was fixed");
  expect(batch.data.open, 1, "the finding is still open");
  expect(batch.data.stopped, 0, "nothing stopped the batch");
  expect(batch.data.complete, false, "the store is not complete");
  expect(
    batch.data.min_severity,
    "important",
    "the default floor is important",
  );
  expect(batch.data.verify_cmd, "true", "the batch named its evidence");
  expect(rows.length, 1, "one row in rank order");
  expect(row.sig, single.data.sig, "the batch hands out the same finding");
  expect(row.locator, "app/handler.go:5", "the row names the anchor line");
  expect(row.severity, "important", "the row carries the severity");
  expect(row.pass, 2, "the batch consumed the second pass");
  expect(row.outcome, "ready", "the row is ready to patch");
  expect(
    batch.next,
    "patch each anchor, re-run fetch-local so the bundle captures the fixes (no commit needed), run the verify command through verdict verify, write one verification.json claim per sig, land with record, then re-run fix --all",
    "the envelope names the batch steps",
  );
  ok("fix --all hands out one ready pass per open finding");

  console.log(`fix ${REPO} ${LOCAL_SLUG} --all`);
  const refusal = await expectRefusal(
    "fix --all without a verify command",
    () => verdict.fix([REPO, LOCAL_SLUG, "--all"]),
  );
  line("code", refusal.code);
  line("hint", refusal.hint);
  expect(
    refusal.code,
    "no-verify-cmd",
    "a batch without a proof command refuses typed",
  );
  expectMatch(refusal.hint, /^try: /, "the refusal carries its fix hint");
  ok("fix --all without a verify command refuses typed");
} finally {
  sandbox.cleanup();
}
