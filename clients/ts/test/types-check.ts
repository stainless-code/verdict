// Compile-time checks for the TS client: every typed helper returns its
// pinned data shape, untyped verbs stay unknown, and optionality is exact.
// This file is never executed; `tsc --noEmit` (tsconfig.types.json) is the
// assertion. Misuse lines carry @ts-expect-error so a widened type fails.
import {
  createClient,
  run,
  VerdictRefusalError,
  type Envelope,
  type ReportData,
  type PostData,
  type RecordData,
} from "../src/index.js";

export async function typeChecks(): Promise<void> {
  const client = createClient({ bin: "verdict", timeoutMs: 1000 });

  const report: Envelope<ReportData> = await client.report([
    "acme/app",
    "4243",
  ]);
  const open: number = report.data.verdict.open;
  const sig: string = report.data.items[0]?.sig ?? "";
  void open;
  void sig;
  // @ts-expect-error - verdict.open is a number, not a string
  const badOpen: string = report.data.verdict.open;

  const post: Envelope<PostData> = await client.post([
    "acme/app",
    "4244",
    "--dry-run",
  ]);
  const dry: boolean = post.data.dry_run;
  void dry;
  void badOpen;

  const record: Envelope<RecordData> = await client.record([
    "acme/app",
    "local-demo",
  ]);
  const findings: number = record.data.findings.open;
  void findings;

  // Unpinned verbs stay unknown: reading data needs a narrowing cast.
  const show = await client.show(["acme/app", "4243", "--sig", "01234567"]);
  const unknownData: unknown = show.data;
  void unknownData;
  // @ts-expect-error - unknown data has no .verdict
  void show.data.verdict;

  // Generic run() infers the same shapes.
  const generic: Envelope<ReportData> = await run<ReportData>(
    "report",
    ["acme/app", "4243"],
    {
      bin: "verdict",
    },
  );
  void generic;

  // Refusal codes are the generated union, open to future codes.
  function onRefusal(error: VerdictRefusalError): string {
    const code: string = error.code;
    if (error.code === "missing-findings") return code;
    return code;
  }
  void onRefusal;
}
