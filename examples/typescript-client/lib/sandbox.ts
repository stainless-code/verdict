// The sandbox every example runs in: a throwaway git checkout, the bundle the
// real `fetch-local` producer writes for it, the same bundle reshaped as a
// provider PR, and a network-dead `gh` stub on the child PATH.
//
// Nothing here mocks the client or its return value: an example calls the
// shipped client, which spawns the real binary, which reads the bundle this
// module produced and answers in its own envelope.

import { execFileSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { devNull, tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));

/** This examples directory. */
export const EXAMPLES_DIR = dirname(HERE);

/** The repository root; `make build` writes `bin/verdict` there. */
export const REPO_ROOT = join(EXAMPLES_DIR, "..", "..");

/** The repository every example addresses. */
export const REPO = "acme/app";

/** The PR number the provider bundle is addressed by. */
export const PR = "4243";

/** The local bundle name `fetch-local acme/app demo` writes. */
export const LOCAL_SLUG = "local-demo";

// The reviewed change, closed by the fixture's second commit. The example
// proves the merge, not the diff, so the change stays small enough to read.
const BASE_HANDLER = [
  "package app",
  "",
  "func Handle() error {",
  "\treturn nil",
  "}",
  "",
].join("\n");

const REVIEWED_HANDLER = [
  "package app",
  "",
  "func Handle() error {",
  "\tif err := Validate(); err != nil {",
  "\t\treturn err",
  "\t}",
  "\treturn nil",
  "}",
  "",
].join("\n");

/**
 * The review the agent would have written. The finding quotes the added line
 * verbatim, and the two receipts are the hunk rows `fetch-local` indexes, so a
 * fixture that drifts from the diff fails the example's `warnings` and
 * `evidence.receipts` assertions instead of documenting an envelope nobody
 * read.
 */
const REVIEW = {
  schema_version: "1",
  summary:
    "Adds an input check before the handler proceeds; the returned error is unwrapped, so the caller keeps no cause to log.",
  findings: [
    {
      path: "app/handler.go",
      line: 5,
      severity: "important",
      claim: "Return the wrapped error so the caller keeps the cause.",
      quote: "\t\treturn err",
      rigor: "walked",
      confidence: "high",
    },
  ],
  receipts: [
    { hunk: "app/handler.go\tOLD\t1\t5", quote: "\treturn nil" },
    { hunk: "app/handler.go\tNEW\t1\t8", quote: "\t\treturn err" },
  ],
};

// A wall-clock date would give every run a different commit SHA and every
// capture a different base; the bundle would still work, but the output these
// examples document would move on its own.
const PINNED_DATE = "2024-06-01T00:00:00Z";

export interface Sandbox {
  /** The throwaway root; the checkout and the stub live inside it. */
  readonly root: string;
  /** The fixture checkout the client runs in: it holds `.verdict/state/`. */
  readonly cwd: string;
  /** The verdict binary the client spawns. */
  readonly bin: string;
  /** The fixture head SHA both bundles record. */
  readonly head: string;
  /** Child environment: the `gh` stub first on PATH, empty personal layers. */
  readonly env: NodeJS.ProcessEnv;
  /** The `gh` stub's call log, one call per line. */
  readonly ghLog: string;
  cleanup(): void;
}

/**
 * Builds the sandbox and returns it. The caller owns `cleanup()`; every
 * example calls it in a `finally`.
 */
export function createSandbox(): Sandbox {
  const bin = resolveBinary();
  const root = mkdtempSync(join(tmpdir(), "verdict-ts-example-"));
  const checkout = join(root, "checkout");
  const stubDir = join(root, "stub-bin");
  const ghLog = join(root, "gh.log");
  const env = sandboxEnv(root, stubDir, ghLog);
  const cleanup = (): void => {
    rmSync(root, { recursive: true, force: true });
  };
  try {
    const head = buildCheckout(checkout, bin, env);
    // The stub answers the one live head read post --dry-run performs; the
    // value is the fixture's, so the example compares against `sandbox.head`.
    env["GH_STUB_HEAD"] = head;
    writeReview(join(checkout, ".verdict", "state", LOCAL_SLUG));
    buildPrBundle(checkout);
    writeProposals(checkout);
    installGhStub(stubDir);
    return { root, cwd: checkout, bin, head, env, ghLog, cleanup };
  } catch (error) {
    cleanup();
    throw error;
  }
}

/** The `gh` calls the stub has answered, in order. */
export function ghCalls(sandbox: Sandbox): string[] {
  if (!existsSync(sandbox.ghLog)) return [];
  return readFileSync(sandbox.ghLog, "utf8")
    .split("\n")
    .filter((line) => line !== "");
}

/** The bundle directory of a local slug or a PR number, relative to the cwd. */
export function bundleDir(name: string): string {
  return join(".verdict", "state", name);
}

/**
 * Resolves the binary the same way a reader would: `VERDICT_BIN` when set,
 * else the `bin/verdict` that `make build` wrote at the repository root.
 */
function resolveBinary(): string {
  const explicit = process.env["VERDICT_BIN"];
  if (explicit !== undefined && explicit !== "") return explicit;
  const built = join(REPO_ROOT, "bin", "verdict");
  if (existsSync(built)) return built;
  throw new Error(
    `no verdict binary: run \`make build\` in ${REPO_ROOT}, or set VERDICT_BIN`,
  );
}

/**
 * The child environment. An empty value reads as unset to the binary
 * (`internal/config.envTrim`), so the knobs and personal settings a developer
 * happens to carry cannot change the output these examples document.
 */
function sandboxEnv(
  root: string,
  stubDir: string,
  ghLog: string,
): NodeJS.ProcessEnv {
  return {
    PATH: `${stubDir}${delimiter}${process.env["PATH"] ?? ""}`,
    GH_STUB_LOG: ghLog,
    // An empty personal layer: `config --preview` resolves operator settings,
    // and a developer's own file must not decide the answer.
    HOME: root,
    XDG_CONFIG_HOME: root,
    REVIEW_BUDGET_USD: "",
    REVIEW_MAX_BYTES: "",
    REVIEW_PANEL: "",
    REVIEW_KB_MAX_CHARS: "",
    REVIEW_KB_MIN_JUDGED: "",
    REVIEW_KB_MAX_FP_RATE: "",
  };
}

function git(cwd: string, args: string[]): string {
  return execFileSync(
    "git",
    [
      "-c",
      "user.name=verdict examples",
      "-c",
      "user.email=examples@verdict.invalid",
      "-c",
      "commit.gpgsign=false",
      "-c",
      "init.defaultBranch=main",
      ...args,
    ],
    {
      cwd,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        GIT_AUTHOR_DATE: PINNED_DATE,
        GIT_COMMITTER_DATE: PINNED_DATE,
        // The fixture pins its own identity and its own bytes: a developer's
        // global config must not add signatures or rewrite line endings.
        GIT_CONFIG_GLOBAL: devNull,
        GIT_CONFIG_SYSTEM: devNull,
      },
    },
  );
}

function write(file: string, body: string): void {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, body);
}

/** Commits the reviewed change, then has the real producer capture it. */
function buildCheckout(
  checkout: string,
  bin: string,
  env: NodeJS.ProcessEnv,
): string {
  mkdirSync(checkout, { recursive: true });
  git(checkout, ["init", "-q"]);
  // Generated state stays out of the fixture's own diff and status.
  write(join(checkout, ".gitignore"), ".verdict/\n");
  write(join(checkout, "README.md"), "# acme/app\n\nA fixture checkout.\n");
  write(join(checkout, "app", "handler.go"), BASE_HANDLER);
  git(checkout, ["add", "-A"]);
  git(checkout, ["commit", "-qm", "base"]);
  write(join(checkout, "app", "handler.go"), REVIEWED_HANDLER);
  git(checkout, ["add", "-A"]);
  git(checkout, ["commit", "-qm", "review target"]);

  // The one command that turns a checkout into a bundle: it writes source.json,
  // full.diff, hunks.tsv and the controls + run capture record and post gate on.
  // A hand-written bundle cannot, which is why the examples produce theirs.
  execFileSync(
    bin,
    ["fetch-local", REPO, "demo", "--range", "HEAD~1..HEAD", "--json"],
    { cwd: checkout, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
  return git(checkout, ["rev-parse", "HEAD"]).trim();
}

/** Writes the review the merge reads: `<bundle>/review.json`. */
function writeReview(bundle: string): void {
  write(join(bundle, "review.json"), `${JSON.stringify(REVIEW, null, 2)}\n`);
}

/**
 * Reshapes a copy of the local bundle as a GitHub PR: `PR.json` instead of
 * `source.json`. `post` accepts that as a target; a local bundle never posts.
 */
function buildPrBundle(checkout: string): void {
  const local = join(checkout, bundleDir(LOCAL_SLUG));
  const pr = join(checkout, bundleDir(PR));
  const source = JSON.parse(
    readFileSync(join(local, "source.json"), "utf8"),
  ) as { head_sha: string; base_sha: string };
  cpSync(local, pr, { recursive: true });
  rmSync(join(pr, "source.json"));
  write(
    join(pr, "PR.json"),
    `${JSON.stringify(
      {
        state: "OPEN",
        headRefOid: source.head_sha,
        baseRefOid: source.base_sha,
        author: { login: "teammate" },
      },
      null,
      2,
    )}\n`,
  );
}

/** Copies the proposed config files into the checkout under a stable path. */
function writeProposals(checkout: string): void {
  const proposals = join(checkout, "proposals");
  mkdirSync(proposals, { recursive: true });
  for (const name of ["proposed-config.json", "bad-config.json"]) {
    copyFileSync(join(EXAMPLES_DIR, "fixtures", name), join(proposals, name));
  }
}

/** Installs the network-dead `gh` stub as the first `gh` on the child PATH. */
function installGhStub(stubDir: string): void {
  mkdirSync(stubDir, { recursive: true });
  const stub = join(stubDir, "gh");
  copyFileSync(join(EXAMPLES_DIR, "fixtures", "gh"), stub);
  chmodSync(stub, 0o755);
}
