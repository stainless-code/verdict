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

  const show = await client.show(["acme/app", "4243", "--sig", "01234567"]);
  const unknownData: unknown = show.data;
  void unknownData;
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
