// config: the read-only policy surface. `--explain` renders the policy a
// fetched run stored, `--preview --file` resolves a proposed .verdict/config.json
// and writes nothing, a bad proposal refuses typed, and asking for neither mode
// is a usage error with no envelope.
//
//   make build && make clients-ts
//   node examples/typescript-client/config.ts

import {
  createClient,
  VerdictUsageError,
  type DataFor,
} from "../../clients/ts/dist/index.js";
import {
  expect,
  expectIncludes,
  expectMatch,
  expectRefusal,
  line,
  ok,
} from "./lib/expect.ts";
import { PR, REPO, createSandbox } from "./lib/sandbox.ts";

/** The one resolved policy value `key`, or a failure naming what is missing. */
function value(
  data: DataFor["config"],
  key: string,
): DataFor["config"]["values"][number] {
  const found = data.values.find((entry) => entry.key === key);
  if (found === undefined)
    throw new Error(`config data carries no ${key} value`);
  return found;
}

/** Runs `call`, requiring the exit-2 usage error it must produce. */
async function expectUsage(
  what: string,
  call: () => Promise<unknown>,
): Promise<VerdictUsageError> {
  try {
    await call();
  } catch (error) {
    if (error instanceof VerdictUsageError) return error;
    throw error;
  }
  throw new Error(`${what}: want a usage error, got a success envelope`);
}

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

  console.log(`config ${REPO} ${PR} --explain`);
  const explain = await verdict.config([REPO, PR, "--explain"]);
  line("mode", explain.data.mode);
  line("trusted", explain.data.trusted);
  line("source", explain.data.source);
  line("plan_status", explain.data.plan_status);
  line("max_bytes", value(explain.data, "max_bytes").value);
  line("spec_coverage_min", value(explain.data, "spec_coverage_min").value);
  line("spend", explain.data.spend);

  expect(
    explain.data.mode,
    "explain",
    "the envelope reports the mode asked for",
  );
  expect(explain.data.trusted, false, "a local bundle is never a trusted base");
  expect(explain.data.plan_status, "pending", "the captured plan has not run");
  expect(explain.data.plan.length, 1, "the capture holds one plan group");
  expect(explain.data.requirements.length, 0, "no policy clause was captured");
  expect(explain.data.privacy.length, 0, "no exclusion was captured");
  const maxBytes = value(explain.data, "max_bytes");
  expect(maxBytes.value, 512000, "the built-in diff cap is what the run used");
  expect(maxBytes.layer, "built-in", "no layer overrode the cap");
  expect(maxBytes.source, "built-in", "the cap's origin is the built-in floor");
  expect(
    value(explain.data, "spec_coverage_min").value,
    80,
    "the spec floor is the built-in 80",
  );
  expect(
    explain.data.spend.enforce_cap,
    false,
    "the captured spend cap is off",
  );
  ok("config --explain renders the stored policy and its origins");

  console.log(
    `config ${REPO} ${PR} --preview --file proposals/proposed-config.json`,
  );
  const preview = await verdict.config([
    REPO,
    PR,
    "--preview",
    "--file",
    "proposals/proposed-config.json",
  ]);
  line("mode", preview.data.mode);
  line("plan_status", preview.data.plan_status);
  line("spend", preview.data.spend);
  line("budget_usd", value(preview.data, "budget_usd").value);
  line("budget source", value(preview.data, "budget_usd").source);

  expect(
    preview.data.mode,
    "preview",
    "the envelope reports the mode asked for",
  );
  expect(preview.data.plan_status, "pending", "a preview plans, it never runs");
  const budget = value(preview.data, "budget_usd");
  expect(budget.value, 3, "the proposed budget is what the preview resolved");
  expect(budget.layer, "repo", "the proposal wins over the built-in floor");
  expect(
    budget.source,
    "proposed file proposals/proposed-config.json",
    "the value names the file it came from",
  );
  expect(
    preview.data.spend.guard_usd,
    3,
    "the proposed budget becomes the spend guard",
  );
  ok("config --preview resolves a proposed file and writes nothing");

  console.log(
    `config ${REPO} ${PR} --preview --file proposals/bad-config.json`,
  );
  const refusal = await expectRefusal(
    "config --preview on a bad proposal",
    () =>
      verdict.config([
        REPO,
        PR,
        "--preview",
        "--file",
        "proposals/bad-config.json",
      ]),
  );
  line("code", refusal.code);
  line("message", refusal.message);
  line("hint", refusal.hint);
  expect(
    refusal.code,
    "bad-config",
    "an invalid proposal refuses as bad-config",
  );
  expectIncludes(
    refusal.message,
    "$.defaults.budget_usd",
    "the refusal names the JSONPath to fix",
  );
  expectMatch(refusal.hint, /^try: /, "the refusal carries its fix hint");
  ok("config --preview refuses a bad proposal with a typed bad-config");

  console.log(`config ${REPO} ${PR}`);
  const usage = await expectUsage("config with no mode", () =>
    verdict.config([REPO, PR]),
  );
  line("usage", usage.message);
  expectIncludes(
    usage.stderr,
    "config wants exactly one of --explain or --preview",
    "exit 2 carries the usage line on stderr",
  );
  ok("config without a mode is an exit-2 usage error, not an envelope");
} finally {
  sandbox.cleanup();
}
