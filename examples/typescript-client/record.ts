// record: the local merge. `--dry-run` runs every check and writes nothing; a
// real record writes findings.json and record.json; a provider PR bundle is
// not record's target and refuses typed; a malformed review refuses with the
// typed problems the fix needs.
//
//   make build && make clients-ts
//   node examples/typescript-client/record.ts

import { existsSync, readFileSync, writeFileSync } from "node:fs";
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
} from "./lib/sandbox.ts";

const sandbox = createSandbox();
try {
  // The client spawns `verdict <verb> … --json` in the fixture checkout and
  // maps the exit code to a typed error; nothing here re-implements a verb.
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
  const findingsFile = join(bundle, "findings.json");
  const recordFile = join(bundle, "record.json");

  console.log(`record ${REPO} ${LOCAL_SLUG} --dry-run`);
  const dry = await verdict.record([REPO, LOCAL_SLUG, "--dry-run"]);
  line("verb", dry.verb);
  line("dry_run", dry.data.dry_run);
  line("check", dry.data.check);
  line("receipts", expectPresent(dry.data.evidence, "evidence").receipts);
  line("warnings", dry.warnings);

  expect(dry.data.dry_run, true, "record --dry-run reports data.dry_run");
  const check = expectPresent(
    dry.data.check,
    "record --dry-run carries data.check",
  );
  expect(check.findings, 1, "the dry run counted the review's one finding");
  expect(check.anchors_moved_to_body, 0, "the finding sits inside its hunk");
  const evidence = expectPresent(
    dry.data.evidence,
    "record --dry-run carries data.evidence",
  );
  expect(evidence.receipts.total, 1, "one changed hunk row awaits a receipt");
  expect(
    evidence.receipts.complete,
    true,
    "every changed hunk carries a matched receipt",
  );
  expect(
    evidence.insufficient,
    false,
    "the evidence is sufficient to decide on",
  );
  expect(dry.warnings.length, 0, "the clean review raises no warning");
  // The raw finding count is what a caller branches on; the file name below is
  // the write the flag does not perform.
  expect(dry.data.findings.file, "", "a dry run names no findings file");
  expect(
    existsSync(findingsFile),
    false,
    "record --dry-run writes no findings.json",
  );
  expect(
    existsSync(recordFile),
    false,
    "record --dry-run writes no record.json",
  );
  ok("record --dry-run previews the merge and writes nothing");

  console.log(`record ${REPO} ${LOCAL_SLUG}`);
  const real = await verdict.record([REPO, LOCAL_SLUG]);
  line("findings", real.data.findings);
  line("anchors", real.data.anchors);
  line("next", real.next);
  expect(real.data.dry_run === true, false, "a real record is not a dry run");
  expect(real.data.findings.open, 1, "one finding is open");
  expect(real.data.findings.deferred, 0, "no finding is deferred");
  expect(
    real.data.findings.file,
    ".verdict/state/local-demo/findings.json",
    "the envelope names the store it wrote",
  );
  expect(existsSync(findingsFile), true, "the real record wrote findings.json");
  expect(existsSync(recordFile), true, "the real record wrote record.json");
  ok("record writes findings.json and record.json");

  console.log(`record ${REPO} ${PR}`);
  const refusal = await expectRefusal("record on a provider PR bundle", () =>
    verdict.record([REPO, PR]),
  );
  line("code", refusal.code);
  line("hint", refusal.hint);
  expect(
    refusal.code,
    "not-local",
    "a GitHub PR bundle is not record's target",
  );
  expectMatch(refusal.hint, /^try: /, "the refusal carries its re-run hint");
  ok("record refuses a provider PR bundle with a typed not-local");

  console.log(`record ${REPO} ${LOCAL_SLUG} --dry-run (malformed review)`);
  const reviewPath = join(bundle, "review.json");
  const review = JSON.parse(readFileSync(reviewPath, "utf8"));
  review.findings[0].path = 5;
  writeFileSync(reviewPath, `${JSON.stringify(review, null, 2)}\n`);
  const badReview = await expectRefusal("record on a malformed review", () =>
    verdict.record([REPO, LOCAL_SLUG, "--dry-run"]),
  );
  line("code", badReview.code);
  line("problem path", badReview.problems[0]?.path);
  expect(
    badReview.code,
    "bad-review-input",
    "a malformed review refuses as bad-review-input",
  );
  expect(
    badReview.problems[0]?.path,
    "$.findings[0].path",
    "the refusal names the broken JSONPath",
  );
  expect(badReview.problems_truncated, 0, "the problem list is complete");
  ok("record refuses a malformed review with typed problems");
} finally {
  sandbox.cleanup();
}
