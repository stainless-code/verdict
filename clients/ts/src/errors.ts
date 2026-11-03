// Typed errors for the verdict TS client. EXPERIMENTAL until 1.0, like the
// envelope contract they mirror: every class maps one documented outcome of
// `verdict <verb> --json` (exit codes 0/1/2/3 in internal/cli/cli.go).
import type { RefusalCode } from "./refusal-codes.gen.js";

// Repair command printed whenever the binary cannot be spawned.
export const VERDICT_REPAIR = "npm i -g @stainless-code/verdict@latest";

// VerdictRefusalError is exit 1: the binary ran and refused with a typed
// envelope (data null, error {code, message}). The hint is the trailing
// `try: ...` line the CLI appends to every refusal message (HintFor in
// internal/core/hint.go), split off so callers can render it separately.
export class VerdictRefusalError extends Error {
  readonly code: RefusalCode | (string & {});
  readonly hint: string;
  readonly warnings: string[];

  constructor(code: string, message: string, warnings: string[] = []) {
    const { cause, hint } = splitHint(message);
    super(cause);
    this.name = "VerdictRefusalError";
    this.code = code;
    this.hint = hint;
    this.warnings = warnings;
  }
}

// VerdictRefusal is the short alias for the refusal error.
export type VerdictRefusal = VerdictRefusalError;

// VerdictUsageError is exit 2: the argv never formed a run (unknown verb,
// bad flags). There is no envelope; stderr carries the usage block.
export class VerdictUsageError extends Error {
  readonly stderr: string;

  constructor(stderr: string) {
    super(firstLine(stderr) || "verdict usage error");
    this.name = "VerdictUsageError";
    this.stderr = stderr;
  }
}

// VerdictProcessError is exit 3 or a crash: the binary broke its contract
// (unparseable stdout, zero/one-envelope violation, signal death). It is
// also the base class for timeouts.
export class VerdictProcessError extends Error {
  readonly exitCode: number | null;
  readonly signal: string | null;
  readonly stdout: string;
  readonly stderr: string;

  constructor(
    message: string,
    opts: {
      exitCode?: number | null;
      signal?: string | null;
      stdout?: string;
      stderr?: string;
    } = {},
  ) {
    super(message);
    this.name = "VerdictProcessError";
    this.exitCode = opts.exitCode ?? null;
    this.signal = opts.signal ?? null;
    this.stdout = opts.stdout ?? "";
    this.stderr = opts.stderr ?? "";
  }
}

// VerdictCrash is the short alias for a crashed/broken child.
export type VerdictCrash = VerdictProcessError;

// VerdictTimeoutError is a kill after timeoutMs: the child may still have
// been working; nothing about the bundle changed observably.
export class VerdictTimeoutError extends VerdictProcessError {
  readonly timeoutMs: number;

  constructor(
    timeoutMs: number,
    opts: { stdout?: string; stderr?: string } = {},
  ) {
    super(`verdict timed out after ${timeoutMs}ms`, {
      stdout: opts.stdout,
      stderr: opts.stderr,
    });
    this.name = "VerdictTimeoutError";
    this.timeoutMs = timeoutMs;
  }
}

// VerdictSpawnError is ENOENT (or EACCES) on the resolved binary: verdict
// is not installed where the resolver looked. The repair command is the
// same one the npm shim prints.
export class VerdictSpawnError extends Error {
  readonly bin: string;
  readonly repair: string = VERDICT_REPAIR;

  constructor(bin: string, cause: string) {
    super(
      `cannot spawn verdict at ${bin}: ${cause}\nrepair: ${VERDICT_REPAIR}`,
    );
    this.name = "VerdictSpawnError";
    this.bin = bin;
  }
}

// VerdictSchemaError is a wire-contract break on an otherwise successful
// run: schema_version missing or other than "1", or a required envelope key
// absent. The envelope stays versioned, so the client fails loud instead
// of misreading a newer shape.
export class VerdictSchemaError extends Error {
  readonly stdout: string;

  constructor(message: string, stdout = "") {
    super(message);
    this.name = "VerdictSchemaError";
    this.stdout = stdout;
  }
}

// splitHint splits the trailing `try: ...` hint off a refusal message, so
// `cause` reads as the cause and `hint` as the remediation line.
function splitHint(message: string): { cause: string; hint: string } {
  const marker = "\ntry: ";
  const at = message.lastIndexOf(marker);
  if (at < 0) return { cause: message, hint: "" };
  return { cause: message.slice(0, at), hint: message.slice(at + 1).trim() };
}

// firstLine keeps usage errors to one line for Error.message.
function firstLine(text: string): string {
  const line = text.split("\n", 1)[0] ?? "";
  return line.trim();
}
