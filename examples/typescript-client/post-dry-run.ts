// post: the guarded submission. `--dry-run` validates the payload, performs the
// one live head read the stale-head guard needs, writes the preview artifact,
// and posts nothing. A local bundle is not a post target and refuses before any
// host call.
//
//   make build && make clients-ts
//   node examples/typescript-client/post-dry-run.ts

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
import {
  LOCAL_SLUG,
  PR,
  REPO,
  bundleDir,
  createSandbox,
  ghCalls,
} from "./lib/sandbox.ts";

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
  const bundle = join(sandbox.cwd, bundleDir(PR));
  const dryRunFile = join(bundle, "findings.dryrun.json");
  const receiptFile = join(bundle, "post.json");

  console.log(`post ${REPO} ${PR} --dry-run`);
  const dry = await verdict.post([REPO, PR, "--dry-run"]);
  line("verb", dry.verb);
  line("dry_run", dry.data.dry_run);
  line("event", dry.data.event);
  line("commit_id", dry.data.commit_id);
  line("findings", dry.data.findings);
  line("payload_hash", dry.data.payload_hash);
  line("next", dry.next);
  line("gh calls", ghCalls(sandbox));

  expect(dry.data.dry_run, true, "post --dry-run reports data.dry_run");
  expect(
    dry.data.event,
    "COMMENT",
    "an unbound approval downgrades to a comment",
  );
  expect(
    dry.data.commit_id,
    sandbox.head,
    "the payload pins the live head it read",
  );
  expect(dry.data.findings.open, 1, "one finding rides the payload");
  expect(
    dry.data.findings.file,
    ".verdict/state/4243/findings.dryrun.json",
    "the preview names the file it wrote",
  );
  expectMatch(
    dry.data.payload_hash,
    /^[0-9a-f]{40}$/,
    "the payload hash is the 40-hex digest a duplicate guard compares",
  );
  expect(
    dry.next,
    "payload NOT posted (dry-run)",
    "the envelope says nothing was posted",
  );
  expect(
    dry.warnings.length,
    0,
    "the live head read answered, so nothing warned",
  );
  const validation = expectPresent(
    dry.data.validation,
    "post --dry-run carries data.validation",
  );
  expect(
    validation.mode,
    "warn",
    "unjudged findings warn instead of failing closed",
  );
  expect(
    existsSync(dryRunFile),
    true,
    "the dry run wrote its findings.dryrun.json",
  );
  expect(existsSync(receiptFile), false, "the dry run wrote no post.json");

  // Exactly one host call, and it is the read the stale-head guard needs: the
  // stub refuses every other argv, so this list is the whole host surface.
  expect(
    ghCalls(sandbox).length,
    1,
    "post --dry-run made exactly one host call",
  );
  expect(
    ghCalls(sandbox)[0],
    "api repos/acme/app/pulls/4243 --jq .head.sha",
    "the one host call is the live head read",
  );
  ok("post --dry-run previews one payload and posts nothing");

  console.log(`post ${REPO} ${LOCAL_SLUG} --dry-run`);
  const refusal = await expectRefusal("post on a local bundle", () =>
    verdict.post([REPO, LOCAL_SLUG, "--dry-run"]),
  );
  line("code", refusal.code);
  line("hint", refusal.hint);
  expect(refusal.code, "no-pr-target", "a local bundle is never a post target");
  expectMatch(refusal.hint, /^try: /, "the refusal carries its re-run hint");
  expect(ghCalls(sandbox).length, 1, "the refusal made no further host call");
  ok("post refuses a local bundle with a typed no-pr-target and zero writes");
} finally {
  sandbox.cleanup();
}
