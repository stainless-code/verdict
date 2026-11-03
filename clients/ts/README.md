# @stainless-code/verdict client (TypeScript)

> **EXPERIMENTAL until 1.0.** The API may change without a major bump. The
> wire contract it wraps — the binary's `--json` envelope plus the published
> JSON Schemas — is versioned separately (`schema_version: "1"`).

A thin, dependency-free wrapper over the `verdict` binary you already
installed. It spawns `verdict <verb> … --json`, reads exactly one envelope
from stdout, and maps the frozen exit codes (0/1/2/3) to typed errors. It
reimplements no verb, starts no server, and downloads nothing.

## Install

One install carries both the binary and the client:

```sh
npm i @stainless-code/verdict
```

```ts
import { createClient } from "@stainless-code/verdict/client";
```

The binary is resolved as `VERDICT_BIN` → the platform package installed
alongside the wrapper → `PATH`. It is never auto-installed; a miss throws
`VerdictSpawnError` with the repair command.

## Quickstart

```ts
import {
  createClient,
  VerdictRefusalError,
} from "@stainless-code/verdict/client";

const verdict = createClient({ cwd: "path/to/repo" });

const report = await verdict.report(["acme/app", "4243"]);
console.log(report.data.verdict.open, "open findings");

const record = await verdict.record(["acme/app", "local-demo"]);
console.log(record.data.findings.open, "recorded");

try {
  await verdict.report(["acme/app", "9999"]);
} catch (error) {
  if (error instanceof VerdictRefusalError) {
    console.log(error.code); // "missing-findings"
    console.log(error.hint); // "try: ..."
  }
}
```

Typed data shapes exist for `report`, `post`, and `record` (generated from
`schemas/*.schema.json`); every other verb returns `Envelope<unknown>`.
`client.run("show", [...])` reaches any verb, including future ones.

Building a client in another language? The [Build a client guide](https://stainless-code.com/verdict/build-a-client) explains the same contract and walks through Python codegen; this client is the reference implementation.

## Error handling

| Outcome          | Class                 | Notes                                              |
| ---------------- | --------------------- | -------------------------------------------------- |
| Exit 0           | returns `Envelope<T>` | `schema_version` must be `"1"`                     |
| Exit 1 (refusal) | `VerdictRefusalError` | `code`, `message`, `hint`, `warnings`              |
| Exit 2 (usage)   | `VerdictUsageError`   | no envelope; `stderr` carries usage                |
| Exit 3 / crash   | `VerdictProcessError` | `exitCode`, `signal`, `stdout`, `stderr`           |
| Timeout          | `VerdictTimeoutError` | extends `VerdictProcessError`; the child is killed |
| Missing binary   | `VerdictSpawnError`   | `repair` is the reinstall command                  |
| Wrong envelope   | `VerdictSchemaError`  | version drift fails loud, never misreads           |

`VerdictRefusal` and `VerdictCrash` are aliases for `VerdictRefusalError`
and `VerdictProcessError`. `RefusalCode` is the generated union of exact
refusal codes (open to future codes); prefix families (`missing-*`,
`bad-*`) accept any suffix.

## Options

```ts
createClient({
  bin: "/custom/verdict", // default: resolved per VERDICT_BIN → package → PATH
  cwd: "path/to/repo", // default: process.cwd()
  env: { REVIEW_BUDGET_USD: "5" }, // merged over process.env
  timeoutMs: 60_000, // default: 300_000; the child is killed past it
  stderr: "pipe", // default: "inherit" (human progress stays on stderr)
  maxStdoutBytes: 8 * 1024 * 1024, // stdout bound; over it the child dies
});
```

## Regenerating the generated sources

`src/types.gen.ts` (envelope `$defs`) and the platform → npm package map
(`src/platforms.gen.ts` for the client, `scripts/npm-shim/platforms.gen.json`
for the install shim) are rendered from `schemas/envelope.schema.json` and
`release/contract.json`:

```sh
node clients/ts/scripts/gen-ts-types.mjs          # rewrite
node clients/ts/scripts/gen-ts-types.mjs --check  # drift gate (CI)
```

`src/refusal-codes.gen.ts` is rendered from the Go refusal hint table in
`internal/core/hint.go`, so a code added to a `hintRules` row cannot be
missed by a source scrape:

```sh
go run ./cmd/tsgen -write  # rewrite (make tsgen)
go run ./cmd/tsgen         # drift gate (runs in make clients-ts)
```

`make clients-ts` runs both gates, builds the binary, then tests and
typechecks. The Node generator needs Node ≥ 18 and works on Bun; it has no
dependencies outside Node builtins.
