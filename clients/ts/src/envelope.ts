import type {
  PostData,
  RecordData,
  RefusalErrorBody,
  ReportData,
} from "./types.gen.js";
import { VerdictSchemaError } from "./errors.js";

export interface Envelope<T = unknown> {
  schema_version: "1";
  verb: string;
  repo: string;
  pr: number | null;
  at: string;
  next: string;
  warnings: string[];
  data: T;
  error: null;
}

export type RefusalBody = RefusalErrorBody;

export interface RefusalEnvelope {
  schema_version: "1";
  verb: string;
  repo: string;
  pr: number | null;
  at: string;
  next: string;
  warnings: string[];
  data: null;
  error: RefusalBody;
}

export interface DataFor {
  report: ReportData;
  post: PostData;
  record: RecordData;
}

const REQUIRED_KEYS = [
  "schema_version",
  "verb",
  "repo",
  "pr",
  "at",
  "next",
  "warnings",
  "data",
  "error",
] as const;

export const SCHEMA_VERSION = "1";

export function assertEnvelope<T = unknown>(
  value: unknown,
  stdout: string,
): asserts value is Envelope<T> {
  assertEnvelopeShape(value, stdout);
  if (value["error"] !== null) {
    throw new VerdictSchemaError(
      "envelope carries error on an exit-0 run; refusing to misread it",
      stdout,
    );
  }
}

export function assertRefusalEnvelope(
  value: unknown,
  stdout: string,
): asserts value is RefusalEnvelope {
  assertEnvelopeShape(value, stdout);
  const record = value as Record<string, unknown>;
  if (record["data"] !== null) {
    throw new VerdictSchemaError(
      "refusal envelope must carry data null",
      stdout,
    );
  }
  const error = record["error"];
  if (
    typeof error !== "object" ||
    error === null ||
    typeof (error as Record<string, unknown>)["code"] !== "string" ||
    typeof (error as Record<string, unknown>)["message"] !== "string"
  ) {
    throw new VerdictSchemaError(
      "refusal envelope error needs string code and message",
      stdout,
    );
  }
}

function assertEnvelopeShape(
  value: unknown,
  stdout: string,
): asserts value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new VerdictSchemaError(
      `expected exactly one JSON envelope object, got ${describe(value)}`,
      stdout,
    );
  }
  const record = value as Record<string, unknown>;
  for (const key of REQUIRED_KEYS) {
    if (!(key in record)) {
      throw new VerdictSchemaError(
        `envelope is missing required key ${JSON.stringify(key)}`,
        stdout,
      );
    }
  }
  if (record["schema_version"] !== SCHEMA_VERSION) {
    throw new VerdictSchemaError(
      `unsupported schema_version ${JSON.stringify(record["schema_version"] ?? null)}; want "1"`,
      stdout,
    );
  }
  if (!Array.isArray(record["warnings"])) {
    throw new VerdictSchemaError("envelope warnings is not an array", stdout);
  }
}

function describe(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}
