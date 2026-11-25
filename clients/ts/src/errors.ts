import type { RefusalCode } from "./refusal-codes.gen.js";
import type { Envelope } from "./envelope.js";
import type { Candidate, Problem, RefusalErrorBody } from "./types.gen.js";

export const VERDICT_REPAIR = "npm i -g @stainless-code/verdict@latest";

export class VerdictRefusalError extends Error {
  readonly code: RefusalCode | (string & {});
  readonly hint: string;
  readonly warnings: string[];
  readonly body: RefusalErrorBody;

  constructor(
    code: string,
    message: string,
    warnings: string[] = [],
    body?: RefusalErrorBody,
  ) {
    const { cause, hint } = splitHint(message);
    super(cause);
    this.name = "VerdictRefusalError";
    this.code = code;
    this.hint = hint;
    this.warnings = warnings;
    this.body = body ?? { code, message };
  }

  get problems(): Problem[] {
    return this.body.problems ?? [];
  }

  get problems_truncated(): number {
    return this.body.problems_truncated ?? 0;
  }

  get candidates(): Candidate[] {
    return this.body.candidates ?? [];
  }

  get retry_at(): string | undefined {
    return this.body.retry_at;
  }
}

export type VerdictRefusal = VerdictRefusalError;

/**
 * Exit 1 carrying a success-shaped envelope: a gate or check run whose verdict
 * is no (`error` is null and `data` is an object). The envelope rides along, so
 * a caller reads the failing dimensions instead of guessing from a message.
 */
export class VerdictGateError<T = unknown> extends Error {
  readonly envelope: Envelope<T>;

  constructor(envelope: Envelope<T>) {
    super(`verdict ${envelope.verb} failed its gate`);
    this.name = "VerdictGateError";
    this.envelope = envelope;
  }

  get verb(): string {
    return this.envelope.verb;
  }

  get data(): T {
    return this.envelope.data;
  }

  get warnings(): string[] {
    return this.envelope.warnings;
  }

  get next(): string {
    return this.envelope.next;
  }
}

export class VerdictUsageError extends Error {
  readonly stderr: string;

  constructor(stderr: string) {
    super(firstLine(stderr) || "verdict usage error");
    this.name = "VerdictUsageError";
    this.stderr = stderr;
  }
}

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

export type VerdictCrash = VerdictProcessError;

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

export class VerdictSchemaError extends Error {
  readonly stdout: string;

  constructor(message: string, stdout = "") {
    super(message);
    this.name = "VerdictSchemaError";
    this.stdout = stdout;
  }
}

function splitHint(message: string): { cause: string; hint: string } {
  const marker = "\ntry: ";
  const at = message.lastIndexOf(marker);
  if (at < 0) return { cause: message, hint: "" };
  return { cause: message.slice(0, at), hint: message.slice(at + 1).trim() };
}

function firstLine(text: string): string {
  const line = text.split("\n", 1)[0] ?? "";
  return line.trim();
}
