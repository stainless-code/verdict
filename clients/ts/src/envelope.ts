// The verdict --json envelope. EXPERIMENTAL until 1.0: the wire shape is
// schemas/envelope.schema.json (generated from Go types); this generic is
// the hand-written view over it with the per-verb data types filled in from
// types.gen.ts. Verbs without a pinned data shape use unknown.
import type {
  PostData,
  RecordData,
  RefusalErrorBody,
  ReportData,
} from "./types.gen.js";
import { VerdictSchemaError } from "./errors.js";

// Envelope is one successful run: error null, data carrying the verb shape.
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

// RefusalBody is the typed refusal the binary returns with exit 1.
export type RefusalBody = RefusalErrorBody;

// RefusalEnvelope is the exit-1 shape: data null, error set.
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

// DataFor pins the published data shapes per verb; every other verb is
// unknown until its data stabilizes (schemas keep the generic object).
export interface DataFor {
  report: ReportData;
  post: PostData;
  record: RecordData;
}

// REQUIRED_KEYS is the documented envelope key set (envelope-shape eval).
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

// SCHEMA_VERSION is the only wire version this client reads.
export const SCHEMA_VERSION = "1";

// assertEnvelope narrows unknown JSON to an Envelope<T>: exactly the
// documented keys, schema_version "1", error null. Anything else is a
// VerdictSchemaError (missing/other version) or a contract break.
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

// assertRefusalEnvelope narrows unknown JSON to the exit-1 refusal shape.
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

// assertEnvelopeShape checks keys, version, and warnings without judging
// error/data; it is the one shape implementation both assertions use.
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

// describe names a value for error messages without dumping it.
function describe(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}
