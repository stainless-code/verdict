import { describe, it, after } from "node:test";
import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  accessSync,
  chmodSync,
  constants,
  existsSync,
  realpathSync,
  rmSync,
} from "node:fs";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = join(HERE, "..", "dist");

const {
  createClient,
  run,
  resolveBin,
  VerdictRefusalError,
  VerdictUsageError,
  VerdictProcessError,
  VerdictTimeoutError,
  VerdictSpawnError,
  VerdictSchemaError,
  VerdictGateError,
} = await import(join(DIST, "index.js"));

const FAKE = join(HERE, "fixtures", "fake-verdict.mjs");

// What fake-verdict.mjs emits in "gate" mode: a check/gate failure on exit 1
// is a success-shaped envelope, not a refusal body.
const GATE_DATA = { passed: false, dimensions: [] };
const GATE_WARNINGS = ["gate read the stored bundle only"];
const GATE_NEXT = "address or triage the open findings, then re-run gate";

// Every call path whose exit 1 means "the verdict is no", not "refused".
const GATE_PATHS = [
  {
    label: 'run("gate")',
    verb: "gate",
    call: (bin, env) => run("gate", [], { bin, stderr: "pipe", env }),
  },
  {
    label: "report(... --check)",
    verb: "report",
    call: (bin, env) =>
      createClient({ bin, stderr: "pipe" }).report(
        ["acme/app", "4243", "--check"],
        { env },
      ),
  },
  {
    label: "gate(...)",
    verb: "gate",
    call: (bin, env) =>
      createClient({ bin, stderr: "pipe" }).gate(
        ["acme/app", "4243", "--max-severity", "critical"],
        { env },
      ),
  },
  {
    label: "ready(... --check)",
    verb: "ready",
    call: (bin, env) =>
      createClient({ bin, stderr: "pipe" }).ready(
        ["acme/app", "4243", "--check"],
        { env },
      ),
  },
  {
    label: "checkTree(...)",
    verb: "check-tree",
    call: (bin, env) =>
      createClient({ bin, stderr: "pipe" }).checkTree([], { env }),
  },
];

function runFake(mode, verb = "report", args = [], opts = {}, env = {}) {
  const client = createClient({ bin: FAKE, stderr: "pipe", ...opts });
  return client.run(verb, args, {
    ...opts,
    env: { ...process.env, FAKE_MODE: mode, ...env },
  });
}

// One-off refusal fakes live in a temp dir each; collect them and remove
// them once the file's tests have run.
const refusalDirs = [];
after(() => {
  for (const dir of refusalDirs) {
    rmSync(dir, { recursive: true, force: true });
  }
});

// Spawns a one-off fake binary emitting a refusal envelope whose `error`
// body is exactly `errorBody`, so the throw/envelope boundary is exercised
// with payloads the shared fake-verdict.mjs does not know.
function runInlineRefusal(errorBody) {
  const dir = mkdtempSync(join(tmpdir(), "verdict-refusal-"));
  refusalDirs.push(dir);
  const fake = join(dir, "fake-verdict.mjs");
  const envelope = {
    schema_version: "1",
    verb: "report",
    repo: "acme/app",
    pr: 4243,
    at: "2026-01-01T00:00:00Z",
    next: "address open threads",
    warnings: [],
    data: null,
    error: errorBody,
  };
  writeFileSync(
    fake,
    `#!/usr/bin/env node\nprocess.stdout.write(${JSON.stringify(JSON.stringify(envelope))} + "\\n");\nprocess.exit(1);\n`,
  );
  chmodSync(fake, 0o755);
  const client = createClient({ bin: fake, stderr: "pipe" });
  return client.run("report", []);
}

describe("unit: fake binary exit paths", () => {
  it("prWait uses the PR wait verb", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-prwait-"));
    const argvFile = join(dir, "argv.json");
    const client = createClient({
      bin: FAKE,
      stderr: "pipe",
      env: {
        ...process.env,
        FAKE_MODE: "record-args",
        FAKE_ARGV_FILE: argvFile,
      },
    });
    await client.prWait([
      "acme/app",
      "4243",
      "--until",
      "merged",
      "--timeout",
      "5s",
    ]);
    assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
      "pr-wait",
      "acme/app",
      "4243",
      "--until",
      "merged",
      "--timeout",
      "5s",
      "--json",
    ]);
  });
  it("prWait returns exit-1 outcome data", async () => {
    const client = createClient({
      bin: FAKE,
      stderr: "pipe",
      env: { ...process.env, FAKE_MODE: "wait-timeout" },
    });
    const result = await client.prWait([]);
    assert.equal(result.error, null);
    assert.equal(result.data.outcome, "wait-timeout");
  });
  it("success returns the envelope", async () => {
    const env = await runFake("success");
    assert.equal(env.schema_version, "1");
    assert.equal(env.verb, "report");
    assert.equal(env.pr, 4243);
    assert.equal(env.error, null);
    assert.ok(Array.isArray(env.warnings));
  });

  it("appends --json exactly once", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
    const argvFile = join(dir, "argv.json");
    await runFake(
      "record-args",
      "report",
      ["acme/app", "4243"],
      {},
      { FAKE_ARGV_FILE: argvFile },
    );
    assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
      "report",
      "acme/app",
      "4243",
      "--json",
    ]);
  });

  it("does not duplicate a caller --json", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
    const argvFile = join(dir, "argv.json");
    await runFake(
      "record-args",
      "report",
      ["acme/app", "4243", "--json"],
      {},
      { FAKE_ARGV_FILE: argvFile },
    );
    const argv = JSON.parse(readFileSync(argvFile, "utf8"));
    assert.equal(argv.filter((a) => a === "--json").length, 1);
  });

  it("adopt, ingest, fix, handoff, learn, init and risk reach their own verbs", async () => {
    for (const verb of [
      "adopt",
      "ingest",
      "fix",
      "learn",
      "init",
      "risk",
      "handoff",
      "journal",
    ]) {
      const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
      const argvFile = join(dir, "argv.json");
      const client = createClient({ bin: FAKE, stderr: "pipe" });
      const env = await client[verb](["acme/app", "4243"], {
        env: {
          ...process.env,
          FAKE_MODE: "record-args",
          FAKE_ARGV_FILE: argvFile,
        },
      });
      assert.equal(env.schema_version, "1");
      assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
        verb,
        "acme/app",
        "4243",
        "--json",
      ]);
    }
  });

  it("index reaches the index verb", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
    const argvFile = join(dir, "argv.json");
    const client = createClient({ bin: FAKE, stderr: "pipe" });
    const env = await client.index(["callers", "app.Target"], {
      env: {
        ...process.env,
        FAKE_MODE: "record-args",
        FAKE_ARGV_FILE: argvFile,
      },
    });
    assert.equal(env.schema_version, "1");
    assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
      "index",
      "callers",
      "app.Target",
      "--json",
    ]);
  });

  it("pack reaches the pack verb with list", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
    const argvFile = join(dir, "argv.json");
    const client = createClient({ bin: FAKE, stderr: "pipe" });
    const env = await client.pack(["list"], {
      env: {
        ...process.env,
        FAKE_MODE: "record-args",
        FAKE_ARGV_FILE: argvFile,
      },
    });
    assert.equal(env.schema_version, "1");
    assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
      "pack",
      "list",
      "--json",
    ]);
  });

  it("threads reaches the threads verb with its flags", async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-argv-"));
    const argvFile = join(dir, "argv.json");
    const client = createClient({ bin: FAKE, stderr: "pipe" });
    const env = await client.threads(
      ["acme/app", "4243", "--provider", "gitlab"],
      {
        env: {
          ...process.env,
          FAKE_MODE: "record-args",
          FAKE_ARGV_FILE: argvFile,
        },
      },
    );
    assert.equal(env.schema_version, "1");
    assert.deepEqual(JSON.parse(readFileSync(argvFile, "utf8")), [
      "threads",
      "acme/app",
      "4243",
      "--provider",
      "gitlab",
      "--json",
    ]);
  });

  it("refusal throws VerdictRefusalError with code, message, hint", async () => {
    await assert.rejects(runFake("refusal"), (error) => {
      assert.ok(error instanceof VerdictRefusalError);
      assert.equal(error.code, "missing-findings");
      assert.ok(
        error.message.includes("missing .verdict/state/4243/findings.json"),
      );
      assert.ok(!error.message.includes("try:"));
      assert.equal(
        error.hint,
        "try: run the fetch/record step named above first, then re-run",
      );
      assert.ok(Array.isArray(error.warnings));
      // No optional fields: the getters fall back to empty/zero.
      assert.deepEqual(error.problems, []);
      assert.deepEqual(error.candidates, []);
      assert.equal(error.problems_truncated, 0);
      assert.equal(error.retry_at, undefined);
      assert.equal(error.body.code, "missing-findings");
      return true;
    });
  });

  it("refusal carries problems, truncated count, candidates and retry_at", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "bad-review-input",
        message: "review.json has 1 problem(s); fix every one, then re-run",
        problems: [
          {
            path: "$.findings[0].anchor",
            code: "bad-enum",
            level: "error",
            message: "anchor is outside its hunk",
            fix: "move the anchor inside the hunk",
          },
        ],
        problems_truncated: 7,
        candidates: [{ kind: "oversize-fetch", suggestion: "app/handler.go" }],
        retry_at: "2026-01-02T00:00:00Z",
      }),
      (error) => {
        assert.ok(error instanceof VerdictRefusalError);
        assert.equal(error.body.code, "bad-review-input");
        assert.equal(error.problems.length, 1);
        assert.equal(error.problems[0].path, "$.findings[0].anchor");
        assert.equal(error.problems_truncated, 7);
        assert.equal(error.candidates[0].suggestion, "app/handler.go");
        assert.equal(error.retry_at, "2026-01-02T00:00:00Z");
        return true;
      },
    );
  });

  it("malformed problems item throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "bad-review-input",
        message: "review.json has 1 problem(s)",
        problems: [{ path: "$.findings[0].path", code: "wrong-type" }],
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(!(error instanceof VerdictRefusalError));
        assert.ok(
          error.message.includes(
            "refusal envelope error problems must be an array of { path, code, level, message, fix } objects",
          ),
        );
        return true;
      },
    );
  });

  it("problems item missing code throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "bad-review-input",
        message: "review.json has 1 problem(s)",
        problems: [
          {
            path: "$.findings[0].path",
            level: "error",
            message: "wrong type",
            fix: "use a string",
          },
        ],
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(!(error instanceof VerdictRefusalError));
        assert.ok(
          error.message.includes(
            "refusal envelope error problems must be an array of { path, code, level, message, fix } objects",
          ),
        );
        return true;
      },
    );
  });

  it("problems item missing level or message throws VerdictSchemaError", async () => {
    const base = {
      path: "$.findings[0].path",
      code: "wrong-type",
      fix: "use a string",
    };
    for (const bad of [
      { ...base, level: "error" },
      { ...base, message: "wrong type" },
      { ...base, level: "error", message: "wrong type", code: 5 },
    ]) {
      await assert.rejects(
        runInlineRefusal({
          code: "bad-review-input",
          message: "review.json has 1 problem(s)",
          problems: [bad],
        }),
        (error) => {
          assert.ok(error instanceof VerdictSchemaError);
          assert.ok(!(error instanceof VerdictRefusalError));
          assert.ok(
            error.message.includes(
              "refusal envelope error problems must be an array of { path, code, level, message, fix } objects",
            ),
          );
          return true;
        },
      );
    }
  });

  it("non-array problems throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "bad-review-input",
        message: "review.json has 1 problem(s)",
        problems: "x",
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(
          error.message.includes(
            "refusal envelope error problems must be an array of { path, code, level, message, fix } objects",
          ),
        );
        return true;
      },
    );
  });

  it("negative problems_truncated throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "bad-review-input",
        message: "review.json has 1 problem(s)",
        problems_truncated: -1,
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(
          error.message.includes(
            "refusal envelope error problems_truncated must be a non-negative integer",
          ),
        );
        return true;
      },
    );
  });

  it("malformed candidates throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "oversize-fetch",
        message: "bundle too large",
        candidates: [{ kind: "oversize-fetch" }],
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(
          error.message.includes(
            "refusal envelope error candidates must be an array of { kind, suggestion } objects",
          ),
        );
        return true;
      },
    );
  });

  it("non-string retry_at throws VerdictSchemaError", async () => {
    await assert.rejects(
      runInlineRefusal({
        code: "oversize-fetch",
        message: "bundle too large",
        retry_at: 5,
      }),
      (error) => {
        assert.ok(error instanceof VerdictSchemaError);
        assert.ok(
          error.message.includes(
            "refusal envelope error retry_at must be a string",
          ),
        );
        return true;
      },
    );
  });

  it("usage error throws VerdictUsageError with stderr", async () => {
    await assert.rejects(runFake("usage", "bogus"), (error) => {
      assert.ok(error instanceof VerdictUsageError);
      assert.ok(error.stderr.includes("usage: verdict"));
      return true;
    });
  });

  it("exit 3 throws VerdictProcessError with the code", async () => {
    await assert.rejects(runFake("broke"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      assert.equal(error.exitCode, 3);
      return true;
    });
  });

  it("unknown exit code throws VerdictProcessError", async () => {
    await assert.rejects(runFake("exit-5"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      assert.equal(error.exitCode, 5);
      assert.ok(!(error instanceof VerdictTimeoutError));
      return true;
    });
  });

  it("malformed stdout throws VerdictProcessError", async () => {
    await assert.rejects(runFake("garbage"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      assert.ok(error.message.includes("exactly one JSON envelope"));
      return true;
    });
  });

  it("two envelopes throws VerdictProcessError", async () => {
    await assert.rejects(runFake("two"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      assert.ok(error.message.includes("exactly one JSON envelope"));
      return true;
    });
  });

  it("empty stdout throws VerdictProcessError", async () => {
    await assert.rejects(runFake("empty"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      return true;
    });
  });

  it("slow child past timeoutMs throws VerdictTimeoutError", async () => {
    await assert.rejects(
      runFake("slow", "report", [], { timeoutMs: 100 }),
      (error) => {
        assert.ok(error instanceof VerdictTimeoutError);
        assert.ok(error instanceof VerdictProcessError);
        assert.equal(error.timeoutMs, 100);
        return true;
      },
    );
  });

  it("schema_version 2 throws VerdictSchemaError", async () => {
    await assert.rejects(runFake("bad-version"), (error) => {
      assert.ok(error instanceof VerdictSchemaError);
      assert.ok(error.message.includes("schema_version"));
      return true;
    });
  });

  it("missing schema_version throws VerdictSchemaError", async () => {
    await assert.rejects(runFake("missing-version"), (error) => {
      assert.ok(error instanceof VerdictSchemaError);
      return true;
    });
  });

  it("empty exit-1 stdout throws VerdictProcessError, not a refusal", async () => {
    await assert.rejects(runFake("refusal-no-envelope"), (error) => {
      assert.ok(error instanceof VerdictProcessError);
      assert.ok(!(error instanceof VerdictRefusalError));
      return true;
    });
  });

  for (const path of GATE_PATHS) {
    it(`gate-shaped exit 1 throws VerdictGateError for ${path.label}`, async () => {
      const env = {
        ...process.env,
        FAKE_MODE: "gate",
        FAKE_VERB: path.verb,
      };
      await assert.rejects(path.call(FAKE, env), (error) => {
        assert.equal(error.name, "VerdictGateError");
        assert.ok(error instanceof VerdictGateError);
        assert.ok(!(error instanceof VerdictRefusalError));
        assert.ok(!(error instanceof VerdictSchemaError));
        // The envelope rides along intact: verb, data, warnings, next.
        assert.equal(error.envelope.verb, path.verb);
        assert.deepEqual(error.envelope.data, GATE_DATA);
        assert.deepEqual(error.envelope.warnings, GATE_WARNINGS);
        assert.equal(error.envelope.next, GATE_NEXT);
        assert.equal(error.envelope.error, null);
        // ... and is reachable without reaching into it.
        assert.equal(error.verb, path.verb);
        assert.deepEqual(error.data, GATE_DATA);
        assert.deepEqual(error.warnings, GATE_WARNINGS);
        assert.equal(error.next, GATE_NEXT);
        return true;
      });
    });
  }

  it("exit 1 with error null and data null is a schema error, not a gate", async () => {
    await assert.rejects(runFake("null-data"), (error) => {
      assert.ok(error instanceof VerdictSchemaError);
      assert.ok(!(error instanceof VerdictGateError));
      assert.ok(!(error instanceof VerdictRefusalError));
      return true;
    });
  });

  it("missing binary throws VerdictSpawnError with the repair", async () => {
    const client = createClient({ bin: join(tmpdir(), "no-such-verdict-bin") });
    await assert.rejects(client.run("report", ["x"]), (error) => {
      assert.ok(error instanceof VerdictSpawnError);
      assert.ok(
        error.message.includes("npm i -g @stainless-code/verdict@latest"),
      );
      return true;
    });
  });

  it("resolveBin honors VERDICT_BIN", () => {
    const prev = process.env["VERDICT_BIN"];
    process.env["VERDICT_BIN"] = "/tmp/custom/verdict";
    try {
      assert.equal(resolveBin(), "/tmp/custom/verdict");
      assert.equal(resolveBin("/explicit/verdict"), "/explicit/verdict");
    } finally {
      if (prev === undefined) delete process.env["VERDICT_BIN"];
      else process.env["VERDICT_BIN"] = prev;
    }
  });

  it("one-shot run() works", async () => {
    const env = await run("report", [], {
      bin: FAKE,
      stderr: "pipe",
      env: { ...process.env, FAKE_MODE: "success" },
    });
    assert.equal(env.verb, "report");
  });
});

const REPO_ROOT = join(HERE, "..", "..", "..");
const REAL_BIN =
  process.env["VERDICT_BIN"] ??
  (process.platform === "win32"
    ? join(REPO_ROOT, "bin", "verdict.exe")
    : join(REPO_ROOT, "bin", "verdict"));

// SDK_RUN_STATE is the typed evidence bundle the integrity gate requires
// beside a controls snapshot: the fixture is path- and name-independent, so
// one capture binds every hand-made bundle here. It is generated by the Go
// writer (config.WriteRunState), never typed by hand.
const SDK_BASE_SHA = "5cb3ae427b70d3f7bd15bfb94c8be962abb22aa8";

const SDK_RUN_STATE =
  JSON.stringify(
    {
      resolved: {
        spend: {
          require_known_estimate: false,
          enforce_cap: false,
        },
        plan: {
          effort_tier: "standard",
          status: "pending",
          groups: [
            {
              profile: "",
              depth: "standard",
              checks: null,
              files: ["notes.txt"],
              scopes: [],
              id: "3d31006af0e2525ec9250ddb7e893dc8e1613bab708ef688b33eb5c7efa36893",
              label: "group 1",
            },
          ],
        },
        files: [
          {
            path: "notes.txt",
            profile: "",
            scopes: [],
            requirements: {
              check_origin: null,
              clauses: [],
              checks: null,
              bind_sources: null,
              require_bind: false,
            },
            defaults: {
              origins: {
                budget_usd: { layer: "built-in", source: "built-in" },
                chunk: { layer: "built-in", source: "built-in" },
                display_min_severity: { layer: "built-in", source: "built-in" },
                effort: { layer: "built-in", source: "built-in" },
                "kb_policy.max_chars": {
                  layer: "built-in",
                  source: "built-in",
                },
                "kb_policy.max_fp_rate": {
                  layer: "built-in",
                  source: "built-in",
                },
                "kb_policy.min_judged": {
                  layer: "built-in",
                  source: "built-in",
                },
                max_bytes: { layer: "built-in", source: "built-in" },
                panel: { layer: "built-in", source: "built-in" },
                post_min_severity: { layer: "built-in", source: "built-in" },
                spec_coverage_min: { layer: "built-in", source: "built-in" },
              },
              shadowed: null,
              values: {
                effort: "standard",
                max_bytes: 512000,
                kb_min_judged: 5,
                kb_max_fp_rate: 0.5,
                spec_coverage_min: 80,
                chunk: false,
              },
            },
          },
        ],
        privacy: {
          exclusions: null,
        },
        defaults: {
          origins: {
            budget_usd: { layer: "built-in", source: "built-in" },
            chunk: { layer: "built-in", source: "built-in" },
            display_min_severity: { layer: "built-in", source: "built-in" },
            effort: { layer: "built-in", source: "built-in" },
            "kb_policy.max_chars": { layer: "built-in", source: "built-in" },
            "kb_policy.max_fp_rate": { layer: "built-in", source: "built-in" },
            "kb_policy.min_judged": { layer: "built-in", source: "built-in" },
            max_bytes: { layer: "built-in", source: "built-in" },
            panel: { layer: "built-in", source: "built-in" },
            post_min_severity: { layer: "built-in", source: "built-in" },
            spec_coverage_min: { layer: "built-in", source: "built-in" },
          },
          shadowed: null,
          values: {
            effort: "standard",
            max_bytes: 512000,
            kb_min_judged: 5,
            kb_max_fp_rate: 0.5,
            spec_coverage_min: 80,
            chunk: false,
          },
        },
      },
      schema_version: "1",
      name: "local-fixture",
      base_sha: SDK_BASE_SHA,
      captured_at: "2026-11-19T09:38:05Z",
      config_sha256: "",
      inputs_sha256:
        "60a6b4b1994ca862a0c0a9f6902cccad83ddaab884c3314bf5274550c61a2fbf",
      evidence: {
        plan: {
          effort_tier: "standard",
          status: "pending",
          groups: [
            {
              profile: "",
              depth: "standard",
              checks: null,
              files: ["notes.txt"],
              scopes: [],
              id: "3d31006af0e2525ec9250ddb7e893dc8e1613bab708ef688b33eb5c7efa36893",
              label: "group 1",
            },
          ],
        },
        schema_version: "1",
        captured_at: "2026-11-19T09:38:05Z",
        base_sha: SDK_BASE_SHA,
        config_sha256: "",
        patch_sha256:
          "94d2a258c8308641db7b0ada6e81eda8fae398466cb7652b30a837ba95076caa",
        filtered_patch_sha256:
          "94d2a258c8308641db7b0ada6e81eda8fae398466cb7652b30a837ba95076caa",
        consumed_inputs: ["operator settings"],
        values: [
          {
            key: "panel",
            value: null,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "budget_usd",
            value: null,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "max_bytes",
            value: 512000,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "chunk",
            value: false,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "kb_policy.min_judged",
            value: 5,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "kb_policy.max_fp_rate",
            value: 0.5,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "effort",
            value: "standard",
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "display_min_severity",
            value: null,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "post_min_severity",
            value: null,
            origin: { layer: "built-in", source: "built-in" },
          },
          {
            key: "spec_coverage_min",
            value: 80,
            origin: { layer: "built-in", source: "built-in" },
          },
        ],
        shadowed: null,
        requirements: [],
      },
      root: {
        check_origin: null,
        clauses: null,
        checks: null,
        bind_sources: null,
        require_bind: false,
      },
      trusted: false,
      requires_evidence: true,
    },
    null,
    2,
  ) + "\n";

function seedControls(bundle, base) {
  writeFileSync(
    join(bundle, "controls.json"),
    JSON.stringify({
      schema_version: "1",
      policy: "head",
      base_sha: base,
      sha: "",
      files: [],
    }),
  );
  writeFileSync(join(bundle, "run.json"), SDK_RUN_STATE);
}

function mkBundle(pr) {
  const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
  const bundle = join(dir, ".verdict", "state", String(pr));
  mkdirSync(bundle, { recursive: true });
  writeFileSync(
    join(bundle, "PR.json"),
    JSON.stringify({
      state: "OPEN",
      headRefOid: "a".repeat(40),
      baseRefOid: SDK_BASE_SHA,
      author: { login: "teammate" },
    }),
  );
  writeFileSync(
    join(bundle, "full.diff"),
    "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n",
  );
  seedControls(bundle, SDK_BASE_SHA);
  return dir;
}

function writeFindings(dir, pr) {
  writeFileSync(
    join(dir, ".verdict", "state", String(pr), "findings.json"),
    JSON.stringify([
      {
        kind: "inline",
        path: "notes.txt",
        side: "RIGHT",
        line: 2,
        anchored: true,
        body: "Consider clarifying this line.",
        suggestion: null,
        inBounds: true,
        rigor: "pointed",
        severity: "informational",
        sig: "0123456789abcdef0123456789abcdef01234567",
        sig_version: "1",
        firstSeen: "aaaaaaa",
        lastSeen: "aaaaaaa",
        status: "open",
        rationale: "",
        history: [],
      },
    ]),
  );
}

const explicitBin = process.env["VERDICT_BIN"] !== undefined;
const ciRun =
  process.env["CI"] !== undefined &&
  process.env["CI"] !== "" &&
  process.env["CI"] !== "false";
const requireBinary = explicitBin || ciRun;
let haveBinary = false;
try {
  accessSync(REAL_BIN, constants.X_OK);
  haveBinary = true;
} catch {
  process.stderr.write(
    `ts-client tests: no binary at ${REAL_BIN}, ` +
      (requireBinary ? "failing integration\n" : "skipping integration\n"),
  );
}

describe("integration: real binary", () => {
  it(
    "requires an executable binary",
    { skip: haveBinary || !requireBinary },
    () => {
      assert.fail(
        `ts-client integration requires an executable binary at ${REAL_BIN}; run make build first`,
      );
    },
  );

  it(
    "report success envelope over a temp bundle",
    { skip: !haveBinary },
    async () => {
      const dir = mkBundle(4243);
      writeFindings(dir, 4243);
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      const env = await client.report(["acme/app", "4243"]);
      assert.equal(env.schema_version, "1");
      assert.equal(env.verb, "report");
      assert.equal(env.pr, 4243);
      assert.equal(env.error, null);
      assert.equal(env.data.verdict.open, 1);
      assert.equal(env.data.items.length, 1);
    },
  );

  it(
    "verify group receipt certifies the reviewed root over a real capture",
    { skip: !haveBinary },
    async () => {
      const dir = mkdtempSync(join(tmpdir(), "verdict-ts-deep-"));
      const git = (...args) =>
        execFileSync("git", args, { cwd: dir, encoding: "utf8" }).trim();
      git("init", "-q");
      git("config", "user.email", "deep@example.com");
      git("config", "user.name", "deep verify");
      mkdirSync(join(dir, ".verdict"));
      writeFileSync(
        join(dir, ".verdict", "config.json"),
        JSON.stringify({
          schema_version: "1",
          profiles: { deep: { depth: "deep" } },
          default_profile: "deep",
          defaults: { effort: "deep" },
        }),
      );
      writeFileSync(join(dir, "rootmarker"), "reviewed root marker\n");
      mkdirSync(join(dir, "sub"));
      writeFileSync(join(dir, "sub", "notes.txt"), "top\nold line\nbottom\n");
      git("add", ".verdict/config.json", "rootmarker", "sub/notes.txt");
      git("commit", "-qm", "base");
      const base = git("rev-parse", "HEAD");
      writeFileSync(join(dir, "sub", "notes.txt"), "top\nnew line\nbottom\n");
      git("add", "sub/notes.txt");
      git("commit", "-qm", "reviewed change");
      const head = git("rev-parse", "HEAD");

      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      const fetched = await client.fetchLocal([
        "acme/app",
        "tsdeep",
        "--range",
        `${base}..${head}`,
        "--worktree",
      ]);
      assert.equal(fetched.error, null, JSON.stringify(fetched.error));
      assert.equal(fetched.verb, "fetch-local");

      const bundle = join(dir, ".verdict", "state", "local-tsdeep");
      const run = JSON.parse(readFileSync(join(bundle, "run.json"), "utf8"));
      const group = run.resolved.plan.groups[0].id;
      assert.equal(run.resolved.plan.groups[0].depth, "deep");

      const env = await client.verify(
        [
          "acme/app",
          "local-tsdeep",
          "--group",
          group,
          "--command",
          "cat rootmarker",
        ],
        { cwd: join(dir, "sub"), stderr: "pipe" },
      );
      assert.equal(env.error, null, JSON.stringify(env.error));
      assert.equal(env.data.exit, 0);
      assert.equal(env.data.timedOut, false);
      assert.equal(env.data.group, group);
      assert.equal(env.data.head, head);
      assert.equal(env.data.outputTail.includes("reviewed root marker"), true);
      assert.equal(env.data.patch, "");

      const receipt = JSON.parse(readFileSync(env.data.receipt, "utf8"));
      assert.equal(receipt.status, "passed");
      assert.equal(receipt.passed, true);
      assert.equal(receipt.run, "local-tsdeep");
      assert.equal(receipt.group, group);
      assert.equal(receipt.inputs_sha256, run.inputs_sha256);
      assert.equal(receipt.reviewed_head, head);
      assert.equal(receipt.execution_head, head);
      assert.equal(receipt.repo_root, realpathSync(dir));
      assert.equal(receipt.command, "cat rootmarker");
      assert.equal(receipt.exit, 0);
      assert.equal(existsSync(join(bundle, "patch.json")), false);
    },
  );

  it(
    "record success envelope over a local bundle",
    { skip: !haveBinary },
    async () => {
      const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
      const bundle = join(dir, ".verdict", "state", "local-demo");
      mkdirSync(bundle, { recursive: true });
      writeFileSync(
        join(bundle, "source.json"),
        JSON.stringify({
          state: "LOCAL",
          headRefOid: "a".repeat(40),
          base_sha: SDK_BASE_SHA,
        }),
      );
      seedControls(bundle, SDK_BASE_SHA);
      writeFileSync(
        join(bundle, "full.diff"),
        "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n",
      );
      writeFileSync(
        join(bundle, "review.json"),
        JSON.stringify({
          schema_version: "1",
          findings: [
            {
              path: "notes.txt",
              line: 2,
              side: "RIGHT",
              severity: "informational",
              claim: "Consider clarifying this line.",
              quote: "new line",
              confidence: "high",
              rigor: "pointed",
            },
          ],
        }),
      );
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      const env = await client.record(["acme/app", "local-demo"]);
      assert.equal(env.error, null);
      assert.equal(env.data.findings.open, 1);
    },
  );

  it("post --dry-run success envelope", { skip: !haveBinary }, async () => {
    const dir = mkBundle(4244);
    writeFileSync(
      join(dir, ".verdict", "state", "4244", "review.json"),
      JSON.stringify({
        schema_version: "1",
        summary: "Looks good overall — one nit below.",
        findings: [
          {
            path: "notes.txt",
            line: 2,
            side: "RIGHT",
            severity: "informational",
            claim: "Consider clarifying this line.",
            quote: "new line",
            confidence: "high",
            rigor: "pointed",
          },
        ],
      }),
    );
    const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
    const env = await client.post(["acme/app", "4244", "--dry-run"]);
    assert.equal(env.error, null);
    assert.equal(env.data.dry_run, true);
  });

  it("learn --status success envelope", { skip: !haveBinary }, async () => {
    const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
    const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
    const env = await client.learn(["--status"]);
    assert.equal(env.schema_version, "1");
    assert.equal(env.verb, "learn");
    assert.equal(env.error, null);
    assert.deepEqual(env.data.rules, []);
  });

  it(
    "init writes the preset control files",
    { skip: !haveBinary },
    async () => {
      const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      const env = await client.init(["--preset", "chill", "--yes"]);
      assert.equal(env.verb, "init");
      assert.equal(env.error, null);
      assert.equal(env.data.preset, "chill");
      assert.deepEqual(env.data.files, [
        "config.json:create",
        "kb.jsonl:create",
        ".gitignore:create",
      ]);
      const cfg = readFileSync(join(dir, ".verdict", "config.json"), "utf8");
      assert.ok(cfg.includes("0.10"));
    },
  );

  it(
    "report refusal carries the typed code",
    { skip: !haveBinary },
    async () => {
      const dir = mkBundle(4243);
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      await assert.rejects(client.report(["acme/app", "4243"]), (error) => {
        assert.ok(error instanceof VerdictRefusalError);
        assert.equal(error.code, "missing-findings");
        assert.ok(error.hint.startsWith("try:"));
        return true;
      });
    },
  );

  it(
    "report --check gate failure carries the success-shaped envelope",
    { skip: !haveBinary },
    async () => {
      const dir = mkBundle(4243);
      writeFindings(dir, 4243);
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      await assert.rejects(
        client.report([
          "acme/app",
          "4243",
          "--check",
          "informational",
          "--live",
          "skip",
        ]),
        (error) => {
          assert.ok(error instanceof VerdictGateError, error.name);
          assert.equal(error.envelope.verb, "report");
          assert.equal(error.envelope.error, null);
          assert.equal(error.data.gate?.passed, false);
          assert.equal(typeof error.next, "string");
          assert.ok(Array.isArray(error.warnings));
          return true;
        },
      );
    },
  );

  it(
    "report with no args is a usage error",
    { skip: !haveBinary },
    async () => {
      const dir = mkBundle(4243);
      const client = createClient({ bin: REAL_BIN, cwd: dir, stderr: "pipe" });
      await assert.rejects(client.report([]), (error) => {
        assert.ok(error instanceof VerdictUsageError);
        return true;
      });
    },
  );
});
