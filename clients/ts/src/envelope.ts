import type {
  AdoptData,
  CalibrateData,
  CheckTreeData,
  ConfigData,
  DoctorData,
  FetchData,
  FetchLocalData,
  FixData,
  HandoffData,
  IndexData,
  IngestData,
  InitData,
  JournalData,
  LearnData,
  NextData,
  PackData,
  PostData,
  ReadyData,
  PrWaitData,
  RecordData,
  RefusalErrorBody,
  ReportData,
  ReportGate,
  RiskData,
  ShowData,
  ThreadsData,
  ThreadsResolveData,
  TriageData,
  TrendData,
  VerifyData,
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

/**
 * Each verb's `data` profile, keyed by the verb the envelope reports. Hyphenated
 * verbs keep their wire spelling (`fetch-local`, `check-tree`).
 */
export interface DataFor {
  report: ReportData;
  gate: ReportGate;
  post: PostData;
  record: RecordData;
  next: NextData;
  show: ShowData;
  ready: ReadyData;
  "pr-wait": PrWaitData;
  triage: TriageData;
  fetch: FetchData;
  "fetch-local": FetchLocalData;
  threads: ThreadsData | ThreadsResolveData;
  verify: VerifyData;
  "check-tree": CheckTreeData;
  config: ConfigData;
  doctor: DoctorData;
  calibrate: CalibrateData;
  trend: TrendData;
  risk: RiskData;
  handoff: HandoffData;
  index: IndexData;
  journal: JournalData;
  adopt: AdoptData;
  ingest: IngestData;
  fix: FixData;
  learn: LearnData;
  pack: PackData;
  init: InitData;
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
  assertRefusalErrorBody(error as Record<string, unknown>, stdout);
}

function assertRefusalErrorBody(
  body: Record<string, unknown>,
  stdout: string,
): void {
  if ("problems" in body) {
    const problems = body["problems"];
    if (
      !Array.isArray(problems) ||
      problems.some(
        (item) =>
          typeof item !== "object" ||
          item === null ||
          typeof (item as Record<string, unknown>)["path"] !== "string" ||
          typeof (item as Record<string, unknown>)["code"] !== "string" ||
          typeof (item as Record<string, unknown>)["level"] !== "string" ||
          typeof (item as Record<string, unknown>)["message"] !== "string" ||
          typeof (item as Record<string, unknown>)["fix"] !== "string",
      )
    ) {
      throw new VerdictSchemaError(
        "refusal envelope error problems must be an array of { path, code, level, message, fix } objects",
        stdout,
      );
    }
  }
  if ("problems_truncated" in body) {
    const count = body["problems_truncated"];
    if (typeof count !== "number" || !Number.isInteger(count) || count < 0) {
      throw new VerdictSchemaError(
        "refusal envelope error problems_truncated must be a non-negative integer",
        stdout,
      );
    }
  }
  if ("candidates" in body) {
    const candidates = body["candidates"];
    if (
      !Array.isArray(candidates) ||
      candidates.some(
        (item) =>
          typeof item !== "object" ||
          item === null ||
          typeof (item as Record<string, unknown>)["kind"] !== "string" ||
          typeof (item as Record<string, unknown>)["suggestion"] !== "string",
      )
    ) {
      throw new VerdictSchemaError(
        "refusal envelope error candidates must be an array of { kind, suggestion } objects",
        stdout,
      );
    }
  }
  if ("retry_at" in body && typeof body["retry_at"] !== "string") {
    throw new VerdictSchemaError(
      "refusal envelope error retry_at must be a string",
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
