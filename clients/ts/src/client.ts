import { spawn } from "node:child_process";
import { accessSync, constants } from "node:fs";
import { createRequire } from "node:module";
import { delimiter, join } from "node:path";
import {
  assertEnvelope,
  assertRefusalEnvelope,
  type DataFor,
  type Envelope,
} from "./envelope.js";
import {
  VERDICT_REPAIR,
  VerdictProcessError,
  VerdictRefusalError,
  VerdictSchemaError,
  VerdictSpawnError,
  VerdictTimeoutError,
  VerdictUsageError,
} from "./errors.js";
import { PLATFORM_PACKAGES } from "./platforms.gen.js";

export const DEFAULT_TIMEOUT_MS = 300_000;

export const DEFAULT_MAX_STDOUT = 8 * 1024 * 1024;

export interface RunOptions {
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  timeoutMs?: number;
  stderr?: "inherit" | "pipe";
  maxStdoutBytes?: number;
}

export interface ClientOptions extends RunOptions {
  bin?: string;
}

export function resolveBin(explicit?: string): string {
  if (explicit) return explicit;
  const fromEnv = process.env["VERDICT_BIN"];
  if (fromEnv) return fromEnv;
  const key = `${process.platform}-${process.arch}`;
  const pkg = PLATFORM_PACKAGES[key];
  if (pkg !== undefined) {
    for (const sub of [`${pkg}/bin/verdict`, `${pkg}/bin/verdict.exe`]) {
      try {
        return createRequire(`${pkg}/package.json`).resolve(sub);
      } catch {}
    }
  }
  const found = findOnPath();
  if (found) return found;
  throw new VerdictSpawnError(
    fromEnv || "verdict",
    `no binary for ${key} and none on PATH (repair: ${VERDICT_REPAIR})`,
  );
}

function findOnPath(): string | undefined {
  const names =
    process.platform === "win32" ? ["verdict.exe", "verdict"] : ["verdict"];
  const dirs = (process.env["PATH"] ?? "").split(delimiter);
  for (const dir of dirs) {
    if (!dir) continue;
    for (const name of names) {
      const candidate = join(dir, name);
      try {
        accessSync(candidate, constants.X_OK);
        return candidate;
      } catch {}
    }
  }
  return undefined;
}

interface ChildResult {
  code: number | null;
  signal: string | null;
  stdout: string;
  stderr: string;
  spawnCause?: string;
}

function spawnOnce(
  bin: string,
  argv: string[],
  opts: Required<Omit<RunOptions, "cwd" | "env">> & {
    cwd?: string;
    env?: NodeJS.ProcessEnv;
  },
): Promise<ChildResult> {
  return new Promise((resolve) => {
    const stderrMode = opts.stderr === "pipe" ? "pipe" : "inherit";
    const child = spawn(bin, argv, {
      cwd: opts.cwd,
      env:
        opts.env === undefined ? process.env : { ...process.env, ...opts.env },
      stdio: ["ignore", "pipe", stderrMode],
      shell: false,
      windowsHide: true,
    });
    let stdout = Buffer.alloc(0);
    let stderr = Buffer.alloc(0);
    let settled = false;
    const finish = (result: ChildResult): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(result);
    };
    const timer = setTimeout(() => {
      const partial = stdout.toString("utf8");
      const partialErr = stderr.toString("utf8");
      try {
        child.kill("SIGKILL");
      } catch {}
      finish({
        code: null,
        signal: "SIGKILL",
        stdout: partial,
        stderr: partialErr,
        spawnCause: `timeout:${opts.timeoutMs}`,
      });
    }, opts.timeoutMs);
    timer.unref?.();
    child.on("error", (cause: unknown) => {
      const message = cause instanceof Error ? cause.message : String(cause);
      finish({
        code: null,
        signal: null,
        stdout: stdout.toString("utf8"),
        stderr: stderr.toString("utf8"),
        spawnCause: message,
      });
    });
    child.stdout?.on("data", (chunk: Buffer) => {
      stdout = Buffer.concat([stdout, chunk]);
      if (stdout.length > opts.maxStdoutBytes) {
        try {
          child.kill("SIGKILL");
        } catch {}
        finish({
          code: null,
          signal: "SIGKILL",
          stdout: stdout.toString("utf8", 0, opts.maxStdoutBytes),
          stderr: stderr.toString("utf8"),
          spawnCause: `stdout-cap:${opts.maxStdoutBytes}`,
        });
      }
    });
    if (stderrMode === "pipe") {
      child.stderr?.on("data", (chunk: Buffer) => {
        stderr = Buffer.concat([stderr, chunk]);
      });
    }
    child.on("close", (code, signal) => {
      finish({
        code,
        signal,
        stdout: stdout.toString("utf8"),
        stderr: stderr.toString("utf8"),
      });
    });
  });
}

function parseSingleEnvelope(stdout: string): unknown {
  const trimmed = stdout.trim();
  if (!trimmed) {
    throw new VerdictProcessError("verdict printed no stdout envelope", {
      stdout,
    });
  }
  try {
    return JSON.parse(trimmed);
  } catch (cause) {
    const detail = cause instanceof Error ? cause.message : String(cause);
    throw new VerdictProcessError(
      `verdict stdout is not exactly one JSON envelope: ${detail}`,
      { stdout },
    );
  }
}

function throwForExit<T>(
  verb: string,
  result: ChildResult,
  stdout: string,
): Envelope<T> {
  if (result.spawnCause !== undefined) {
    if (result.spawnCause.startsWith("timeout:")) {
      const timeoutMs = Number(result.spawnCause.slice("timeout:".length));
      throw new VerdictTimeoutError(timeoutMs, {
        stdout: result.stdout,
        stderr: result.stderr,
      });
    }
    if (result.spawnCause.startsWith("stdout-cap:")) {
      throw new VerdictProcessError(
        `verdict stdout exceeded the ${result.spawnCause.slice("stdout-cap:".length)}-byte cap`,
        { stdout: result.stdout, stderr: result.stderr },
      );
    }
    throw new VerdictProcessError(
      `verdict ${verb} could not spawn: ${result.spawnCause}`,
      { stdout: result.stdout, stderr: result.stderr },
    );
  }
  if (result.code === 0) {
    const parsed = parseSingleEnvelope(stdout);
    assertEnvelope<T>(parsed, stdout);
    return parsed;
  }
  if (result.code === 1) {
    const parsed = parseSingleEnvelope(stdout);
    assertRefusalEnvelope(parsed, stdout);
    throw new VerdictRefusalError(
      parsed.error.code,
      parsed.error.message,
      parsed.warnings,
    );
  }
  if (result.code === 2) {
    throw new VerdictUsageError(
      result.stderr || `verdict ${verb}: usage error`,
    );
  }
  throw new VerdictProcessError(
    `verdict ${verb} failed: exit ${result.code ?? "?"}${result.signal ? ` (${result.signal})` : ""}`,
    {
      exitCode: result.code,
      signal: result.signal,
      stdout: result.stdout,
      stderr: result.stderr,
    },
  );
}

export interface VerdictClient {
  readonly bin: string;
  run<T = unknown>(
    verb: string,
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<T>>;
  report(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["report"]>>;
  post(args?: string[], opts?: RunOptions): Promise<Envelope<DataFor["post"]>>;
  record(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["record"]>>;
  next(args?: string[], opts?: RunOptions): Promise<Envelope<DataFor["next"]>>;
  show(args?: string[], opts?: RunOptions): Promise<Envelope<DataFor["show"]>>;
  ready(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["ready"]>>;
  triage(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["triage"]>>;
  fetch(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["fetch"]>>;
  fetchLocal(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["fetch-local"]>>;
  verify(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["verify"]>>;
  checkTree(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["check-tree"]>>;
  doctor(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["doctor"]>>;
  calibrate(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["calibrate"]>>;
  trend(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["trend"]>>;
  adopt(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["adopt"]>>;
  fix(args?: string[], opts?: RunOptions): Promise<Envelope<DataFor["fix"]>>;
  learn(
    args?: string[],
    opts?: RunOptions,
  ): Promise<Envelope<DataFor["learn"]>>;
  init(args?: string[], opts?: RunOptions): Promise<Envelope<DataFor["init"]>>;
}

export function createClient(defaults: ClientOptions = {}): VerdictClient {
  async function run<T = unknown>(
    verb: string,
    args: string[] = [],
    opts: RunOptions = {},
  ): Promise<Envelope<T>> {
    const merged: Required<Omit<RunOptions, "cwd" | "env">> & {
      cwd?: string;
      env?: NodeJS.ProcessEnv;
    } = {
      timeoutMs: opts.timeoutMs ?? defaults.timeoutMs ?? DEFAULT_TIMEOUT_MS,
      stderr: opts.stderr ?? defaults.stderr ?? "inherit",
      maxStdoutBytes:
        opts.maxStdoutBytes ?? defaults.maxStdoutBytes ?? DEFAULT_MAX_STDOUT,
      cwd: opts.cwd ?? defaults.cwd,
      env: opts.env ?? defaults.env,
    };
    const bin = defaults.bin ?? resolveBin();
    const argv =
      args.includes("--json") || args.includes("--json=true")
        ? [verb, ...args]
        : [verb, ...args, "--json"];
    const result = await spawnOnce(bin, argv, merged);
    if (
      result.spawnCause !== undefined &&
      !result.spawnCause.startsWith("timeout:") &&
      !result.spawnCause.startsWith("stdout-cap:")
    ) {
      throw new VerdictSpawnError(bin, result.spawnCause);
    }
    return throwForExit<T>(verb, result, result.stdout);
  }

  const client: VerdictClient = {
    get bin() {
      return defaults.bin ?? resolveBin();
    },
    run,
    report: (args, opts) => run<DataFor["report"]>("report", args, opts),
    post: (args, opts) => run<DataFor["post"]>("post", args, opts),
    record: (args, opts) => run<DataFor["record"]>("record", args, opts),
    next: (args, opts) => run<DataFor["next"]>("next", args, opts),
    show: (args, opts) => run<DataFor["show"]>("show", args, opts),
    ready: (args, opts) => run<DataFor["ready"]>("ready", args, opts),
    triage: (args, opts) => run<DataFor["triage"]>("triage", args, opts),
    fetch: (args, opts) => run<DataFor["fetch"]>("fetch", args, opts),
    fetchLocal: (args, opts) =>
      run<DataFor["fetch-local"]>("fetch-local", args, opts),
    verify: (args, opts) => run<DataFor["verify"]>("verify", args, opts),
    checkTree: (args, opts) =>
      run<DataFor["check-tree"]>("check-tree", args, opts),
    doctor: (args, opts) => run<DataFor["doctor"]>("doctor", args, opts),
    calibrate: (args, opts) =>
      run<DataFor["calibrate"]>("calibrate", args, opts),
    trend: (args, opts) => run<DataFor["trend"]>("trend", args, opts),
    adopt: (args, opts) => run<DataFor["adopt"]>("adopt", args, opts),
    fix: (args, opts) => run<DataFor["fix"]>("fix", args, opts),
    learn: (args, opts) => run<DataFor["learn"]>("learn", args, opts),
    init: (args, opts) => run<DataFor["init"]>("init", args, opts),
  };
  return client;
}

export function run<T = unknown>(
  verb: string,
  args: string[] = [],
  opts: ClientOptions = {},
): Promise<Envelope<T>> {
  return createClient(opts).run<T>(verb, args);
}
