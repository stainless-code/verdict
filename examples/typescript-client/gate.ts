// gate: one composed verdict over the dimensions report already computes.
// This example runs it offline on the recorded local bundle: --live skip
// makes zero network calls, --coverage warn reports the absent attestation
// without failing the gate, the findings tier fires on the open finding,
// and the always-on spec and evidence dimensions pass. Asking for no
// dimension at all is a usage error with no envelope.
//
//   make build && make clients-ts
//   node examples/typescript-client/gate.ts

import {
  createClient,
  VerdictGateError,
  VerdictUsageError,
  type DataFor,
} from "../../clients/ts/dist/index.js";
import {
  expect,
  expectIncludes,
  expectPresent,
  line,
  ok,
} from "./lib/expect.ts";
import { LOCAL_SLUG, REPO, createSandbox } from "./lib/sandbox.ts";

/** The one dimension `id`, or a failure naming what is missing. */
function dimension(
  data: DataFor["gate"],
  id: DataFor["gate"]["dimensions"][number]["id"],
): DataFor["gate"]["dimensions"][number] {
  const found = data.dimensions.find((entry) => entry.id === id);
  if (found === undefined)
    throw new Error(`gate data carries no ${id} dimension`);
  return found;
}

/** Runs `call`, requiring the exit-1 gate failure it must produce. */
async function expectGateFailure(
  what: string,
  call: () => Promise<unknown>,
): Promise<VerdictGateError<DataFor["gate"]>> {
  try {
    await call();
  } catch (error) {
    if (error instanceof VerdictGateError) return error;
    throw error;
  }
  throw new Error(`${what}: want a gate failure, got a success envelope`);
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

  // Gate reads the stored record, so the finding has to be landed first.
  const recorded = await verdict.record([REPO, LOCAL_SLUG]);
  expect(recorded.data.findings.open, 1, "the record left one finding open");

  console.log(
    `gate ${REPO} ${LOCAL_SLUG} --max-severity important --live skip --coverage warn`,
  );
  const failed = await expectGateFailure("gate at the important tier", () =>
    verdict.gate([
      REPO,
      LOCAL_SLUG,
      "--max-severity",
      "important",
      "--live",
      "skip",
      "--coverage",
      "warn",
    ]),
  );
  const failedData = failed.data;
  line("passed", failedData.passed);
  line("findings", dimension(failedData, "findings"));
  line("spec", dimension(failedData, "spec"));
  line("evidence", dimension(failedData, "evidence"));
  line("next", failed.next);

  expect(failedData.passed, false, "one open important finding fails the gate");
  expect(
    dimension(failedData, "findings").status,
    "fail",
    "the findings dimension fails at the important tier",
  );
  expect(
    dimension(failedData, "findings").count,
    1,
    "one open finding sits at-or-above important",
  );
  expect(
    dimension(failedData, "spec").status,
    "pass",
    "no spec check is violated",
  );
  expect(
    dimension(failedData, "spec").count,
    0,
    "zero spec checks are violated",
  );
  expect(dimension(failedData, "evidence").status, "pass", "evidence passes");
  expect(dimension(failedData, "evidence").count, 0, "there are no gaps");
  expect(
    dimension(failedData, "live_threads").status,
    "skip",
    "--live skip makes no network call",
  );
  expect(
    dimension(failedData, "coverage").status,
    "skip",
    "--coverage warn reports the absent attestation without failing",
  );
  expect(
    failed.next,
    "address or triage the open findings",
    "the envelope names the work the failure asks for",
  );
  expect(failed.warnings.length, 2, "both skipped dimensions warn");
  ok("gate fails on one open finding at the important tier");

  console.log(
    `gate ${REPO} ${LOCAL_SLUG} --max-severity critical --live skip --coverage warn`,
  );
  const passed = await verdict.gate([
    REPO,
    LOCAL_SLUG,
    "--max-severity",
    "critical",
    "--live",
    "skip",
    "--coverage",
    "warn",
  ]);
  line("passed", passed.data.passed);
  line("findings", dimension(passed.data, "findings"));
  line("next", passed.next);

  expect(passed.data.passed, true, "a floor above every open finding passes");
  expect(
    dimension(passed.data, "findings").status,
    "pass",
    "nothing sits at-or-above critical",
  );
  expect(
    dimension(passed.data, "findings").count,
    0,
    "zero findings are at-or-above critical",
  );
  expect(
    passed.next,
    "none — every requested dimension passed",
    "a passing gate asks for nothing",
  );
  ok("gate passes when the floor sits above every open finding");

  console.log(`gate ${REPO} ${LOCAL_SLUG}`);
  const usage = await expectUsage("gate with no dimension", () =>
    verdict.gate([REPO, LOCAL_SLUG]),
  );
  line("usage", usage.message);
  expectIncludes(
    usage.stderr,
    "gate needs at least one dimension",
    "exit 2 carries the usage line on stderr",
  );
  ok("gate with no dimension is an exit-2 usage error, not an envelope");
} finally {
  sandbox.cleanup();
}
