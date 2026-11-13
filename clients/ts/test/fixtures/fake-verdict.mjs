#!/usr/bin/env node
const mode = process.env["FAKE_MODE"] ?? "success";

function envelope(overrides = {}) {
  return {
    schema_version: "1",
    verb: "report",
    repo: "acme/app",
    pr: 4243,
    at: "2026-01-01T00:00:00Z",
    next: "address open threads",
    warnings: [],
    data: { items: [] },
    error: null,
    ...overrides,
  };
}

function refusal(code, message) {
  return envelope({
    next: "",
    data: null,
    error: { code, message },
  });
}

function emit(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

switch (mode) {
  case "wait-timeout":
    emit(
      envelope({
        verb: "pr-wait",
        data: {
          outcome: "wait-timeout",
          until: "merged",
          snapshot: null,
          observations: 0,
        },
      }),
    );
    process.exit(1);

  case "success": {
    emit(envelope());
    break;
  }
  case "record-args": {
    const { writeFileSync } = await import("node:fs");
    writeFileSync(
      process.env["FAKE_ARGV_FILE"],
      JSON.stringify(process.argv.slice(2)),
    );
    emit(envelope());
    break;
  }
  // A gate/check failure: exit 1 carrying the success shape (error null, a
  // data object), which is what `report --check`, `gate`, `ready --check`,
  // and `check-tree` emit when the verdict is no. One envelope, never two.
  case "gate": {
    emit(
      envelope({
        verb: process.env["FAKE_VERB"] ?? "report",
        next: "address or triage the open findings, then re-run gate",
        warnings: ["gate read the stored bundle only"],
        data: { passed: false, dimensions: [] },
      }),
    );
    process.exitCode = 1;
    break;
  }
  // Exit 1 with neither a refusal body nor data: no verdict to report.
  case "null-data": {
    emit(envelope({ verb: process.env["FAKE_VERB"] ?? "report", data: null }));
    process.exitCode = 1;
    break;
  }
  case "refusal": {
    emit(
      refusal(
        "missing-findings",
        "missing .verdict/state/4243/findings.json (run post or record first)\ntry: run the fetch/record step named above first, then re-run",
      ),
    );
    process.exitCode = 1;
    break;
  }
  case "usage": {
    process.stderr.write(
      "usage: verdict fetch|post|triage|report|next|show [opts]\n",
    );
    process.exitCode = 2;
    break;
  }
  case "broke": {
    process.stderr.write("verdict: internal failure\n");
    process.exitCode = 3;
    break;
  }
  case "exit-5": {
    process.exitCode = 5;
    break;
  }
  case "garbage": {
    process.stdout.write("this is not json\n");
    break;
  }
  case "two": {
    emit(envelope());
    emit(envelope());
    break;
  }
  case "empty": {
    break;
  }
  case "slow": {
    process.stderr.write("human progress\n");
    await new Promise((resolve) => setTimeout(resolve, 2000));
    emit(envelope());
    break;
  }
  case "bad-version": {
    emit(envelope({ schema_version: "2" }));
    break;
  }
  case "missing-version": {
    const { schema_version: _dropped, ...rest } = envelope();
    emit(rest);
    break;
  }
  case "refusal-no-envelope": {
    process.exitCode = 1;
    break;
  }
  default: {
    process.stderr.write(`fake-verdict: unknown FAKE_MODE ${mode}\n`);
    process.exitCode = 3;
    break;
  }
}
