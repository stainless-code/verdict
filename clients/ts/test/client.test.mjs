import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  accessSync,
  constants,
} from "node:fs";
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
} = await import(join(DIST, "index.js"));

const FAKE = join(HERE, "fixtures", "fake-verdict.mjs");

function runFake(mode, verb = "report", args = [], opts = {}, env = {}) {
  const client = createClient({ bin: FAKE, stderr: "pipe", ...opts });
  return client.run(verb, args, {
    ...opts,
    env: { ...process.env, FAKE_MODE: mode, ...env },
  });
}

describe("unit: fake binary exit paths", () => {
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

  it("adopt, fix, learn and init reach their own verbs", async () => {
    for (const verb of ["adopt", "fix", "learn", "init"]) {
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
      assert.ok(error.message.includes("missing .review/4243/findings.json"));
      assert.ok(!error.message.includes("try:"));
      assert.equal(
        error.hint,
        "try: run the fetch/record step named above first, then re-run",
      );
      assert.ok(Array.isArray(error.warnings));
      return true;
    });
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
}

function mkBundle(pr) {
  const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
  const bundle = join(dir, ".review", String(pr));
  mkdirSync(bundle, { recursive: true });
  writeFileSync(
    join(bundle, "PR.json"),
    '{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}',
  );
  writeFileSync(
    join(bundle, "full.diff"),
    "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n",
  );
  seedControls(bundle, "b".repeat(40));
  return dir;
}

function writeFindings(dir, pr) {
  writeFileSync(
    join(dir, ".review", String(pr), "findings.json"),
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
    "record success envelope over a local bundle",
    { skip: !haveBinary },
    async () => {
      const dir = mkdtempSync(join(tmpdir(), "verdict-ts-"));
      const bundle = join(dir, ".review", "local-demo");
      mkdirSync(bundle, { recursive: true });
      writeFileSync(
        join(bundle, "source.json"),
        '{"state":"LOCAL","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}',
      );
      seedControls(bundle, "b".repeat(40));
      writeFileSync(
        join(bundle, "full.diff"),
        "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n",
      );
      writeFileSync(
        join(bundle, "review.json"),
        JSON.stringify({
          schema_version: "1",
          body: "this body is long enough to pass the degenerate guard without any marker at all yes",
          approved: false,
          comments: [
            {
              path: "notes.txt",
              line: 2,
              side: "RIGHT",
              body: "Consider clarifying this line.",
              rigor: "pointed",
              severity: "informational",
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
      join(dir, ".review", "4244", "review.json"),
      JSON.stringify({
        schema_version: "1",
        body: "> Looks good overall — one nit below.",
        approved: false,
        comments: [
          {
            path: "notes.txt",
            line: 2,
            side: "RIGHT",
            body: "Consider clarifying this line.",
            rigor: "pointed",
            severity: "informational",
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
        "budget:create",
        "panel:create",
        "kb-policy:create",
        "kb.jsonl:create",
      ]);
      const budget = readFileSync(join(dir, ".review", "budget"), "utf8");
      assert.ok(budget.includes("2.00"));
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
