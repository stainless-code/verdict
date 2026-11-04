import {
  createClient,
  run,
  VerdictRefusalError,
  type Envelope,
  type ReportData,
  type PostData,
  type RecordData,
} from "../src/index.js";
import type { ShowFix, VerifyHint } from "../src/types.gen.js";

// True only when A and B are the same type, so a widened union cannot pass.
type Exact<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2
    ? true
    : false;
type IsAny<T> = 0 extends 1 & T ? true : false; // a bare `any` passes Exact

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

  const show = await client.show(["acme/app", "4243", "--sig", "01234567"]);
  const unknownData: unknown = show.data;
  void unknownData;

  // show --json fix.verify_hint is `VerifyHint | null` (an anyOf in
  // envelope.schema.json). Every line below breaks the typecheck if the
  // generator ever widens it back to `unknown` or `any`.
  const fix = null as unknown as ShowFix;
  const verifyHint: VerifyHint | null = fix.verify_hint;
  void verifyHint;
  const exactVerifyHint: Exact<ShowFix["verify_hint"], VerifyHint | null> =
    true;
  void exactVerifyHint;
  const verifyHintNotAny: IsAny<ShowFix["verify_hint"]> = false;
  void verifyHintNotAny;
  if (fix.verify_hint !== null) {
    const verifyCmd: string = fix.verify_hint.verifyCmd;
    void verifyCmd;
  }
  // @ts-expect-error - verify_hint is VerifyHint | null, never a bare string
  const badVerifyHint: string = fix.verify_hint;
  void badVerifyHint;
  // @ts-expect-error - unknown data has no .verdict
  void show.data.verdict;

  const learn = await client.learn(["--status"]);
  const learnData: unknown = learn.data;
  void learnData;
  // @ts-expect-error - learn data is unknown, not a rules array
  void learn.data.rules;

  const generic: Envelope<ReportData> = await run<ReportData>(
    "report",
    ["acme/app", "4243"],
    {
      bin: "verdict",
    },
  );
  void generic;

  function onRefusal(error: VerdictRefusalError): string {
    const code: string = error.code;
    if (error.code === "missing-findings") return code;
    return code;
  }
  void onRefusal;
}
