import {
  createClient,
  run,
  VerdictRefusalError,
  type AdoptData,
  type CalibrateData,
  type CheckTreeData,
  type DataFor,
  type DoctorData,
  type Envelope,
  type FetchData,
  type FetchLocalData,
  type FixData,
  type InitData,
  type LearnData,
  type NextData,
  type PostData,
  type ReadyData,
  type RecordData,
  type ReportData,
  type ShowData,
  type ThreadsData,
  type TrendData,
  type TriageData,
  type VerifyData,
} from "../src/index.js";
import type {
  FetchGroupEstimate,
  ShowFix,
  VerifyHint,
} from "../src/types.gen.js";

// True only when A and B are the same type, so a widened union cannot pass.
type Exact<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2
    ? true
    : false;
type IsAny<T> = 0 extends 1 & T ? true : false; // a bare `any` passes Exact

// Every wire verb the client exposes, spelled the way the envelope reports it.
type Verb =
  | "report"
  | "post"
  | "record"
  | "next"
  | "show"
  | "ready"
  | "triage"
  | "fetch"
  | "fetch-local"
  | "threads"
  | "verify"
  | "check-tree"
  | "doctor"
  | "calibrate"
  | "trend"
  | "adopt"
  | "fix"
  | "learn"
  | "init"
  | "config"
  | "gate";

export async function typeChecks(): Promise<void> {
  const client = createClient({ bin: "verdict", timeoutMs: 1000 });

  // DataFor covers every verb and adds no key a verb cannot produce.
  const everyVerbHasAProfile: Exact<keyof DataFor, Verb> = true;
  void everyVerbHasAProfile;

  const report: Envelope<ReportData> = await client.report([
    "acme/app",
    "4243",
  ]);
  const open: number = report.data.verdict.open;
  const sig: string = report.data.items[0]?.sig ?? "";
  void open;
  void sig;

  const gate: Envelope<DataFor["gate"]> = await client.gate([
    "acme/app",
    "4243",
    "--max-severity",
    "critical",
  ]);
  const passed: boolean | undefined = gate.data.gate?.passed;
  const gateStatus: "pass" | "fail" | "skip" | "unknown" | undefined =
    gate.data.gate?.dimensions[0]?.status;
  void passed;
  void gateStatus;
  // @ts-expect-error - dimension status is a closed vocabulary
  const badStatus: "clean" | undefined = gate.data.gate?.dimensions[0]?.status;
  void badStatus;
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

  const next: Envelope<NextData> = await client.next([]);
  const nextSig: string | null = next.data.item?.sig ?? null;
  void nextSig;

  const show: Envelope<ShowData> = await client.show([]);
  const showFix: ShowFix = show.data.fix;
  void showFix;

  const ready: Envelope<ReadyData> = await client.ready([]);
  const mergeable: string = ready.data.mergeable;
  const failing: number = ready.data.ci.failing;
  void mergeable;
  void failing;

  const triage: Envelope<TriageData> = await client.triage([]);
  const status: TriageData["status"] = triage.data.status;
  void status;

  const fetch: Envelope<FetchData> = await client.fetch([]);
  const counted: number = fetch.data.files + fetch.data.hunks;
  void counted;
  // The v2 estimate keeps its documented shape: the fields stay reachable
  // without a cast, and the grouped variant keeps its own types. `unknown`
  // (what a bare-required anyOf used to generate) would fail both reads.
  if (fetch.data.estimate) {
    const tier: string | null | undefined = fetch.data.estimate.tier;
    const projected: number | null | undefined =
      fetch.data.estimate.projected_usd;
    const groups: FetchGroupEstimate[] | undefined = fetch.data.estimate.groups;
    void tier;
    void projected;
    void groups;
  }

  const local: Envelope<FetchLocalData> = await client.fetchLocal([]);
  const kind: string = local.data.kind;
  void kind;

  const exactThreads: Exact<DataFor["threads"], ThreadsData> = true;
  void exactThreads;

  const threads: Envelope<ThreadsData> = await client.threads([
    "acme/app",
    "4243",
    "--provider",
    "gitlab",
  ]);
  const openThreads: number = threads.data.open;
  void openThreads;

  const verify: Envelope<VerifyData> = await client.verify([]);
  const exit: number = verify.data.exit;
  void exit;

  const tree: Envelope<CheckTreeData> = await client.checkTree([]);
  const clean: boolean = tree.data.clean;
  void clean;

  const doctor: Envelope<DoctorData> = await client.doctor([]);
  const healthy: boolean = doctor.data.healthy;
  void healthy;

  const calibrate: Envelope<CalibrateData> = await client.calibrate([]);
  const graded: number = calibrate.data.counts?.findings ?? 0;
  const outcomes: number = calibrate.data.outcomes?.length ?? 0;
  const agreement: number | null =
    calibrate.data.judgment?.agreement_rate ?? null;
  void graded;
  void outcomes;
  void agreement;

  const trend: Envelope<TrendData> = await client.trend([]);
  const bundles: number = trend.data.per_bundle.length;
  const critical: number = trend.data.by_tier.critical;
  void bundles;
  void critical;

  const adopt: Envelope<AdoptData> = await client.adopt([]);
  const adopted: number = adopt.data.adopted;
  void adopted;

  const fixed: Envelope<FixData> = await client.fix([]);
  const complete: boolean = fixed.data.complete;
  void complete;

  const learn: Envelope<LearnData> = await client.learn([]);
  const rules: number = learn.data.rules?.length ?? 0;
  void rules;

  const init: Envelope<InitData> = await client.init([]);
  const preset: string = init.data.preset;
  void preset;

  // Each method resolves its own profile, not the generic envelope: an
  // accidental `Envelope<unknown>` fails the assignment above, and a field
  // that silently widened cannot satisfy the pinned type below.
  // @ts-expect-error - mergeable is a string, never a number
  const badMergeable: number = ready.data.mergeable;
  void badMergeable;

  // The hand-written profiles stay pinned: a generator change that widens a
  // field to unknown/any breaks one of these.
  const exactReady: Exact<DataFor["ready"], ReadyData> = true;
  const exactFetchLocal: Exact<DataFor["fetch-local"], FetchLocalData> = true;
  const exactCheckTree: Exact<DataFor["check-tree"], CheckTreeData> = true;
  const exactTrend: Exact<DataFor["trend"], TrendData> = true;
  const exactCalibrate: Exact<DataFor["calibrate"], CalibrateData> = true;
  void exactReady;
  void exactFetchLocal;
  void exactCheckTree;
  void exactTrend;
  void exactCalibrate;
  const readyStatusesAreNotAny: IsAny<TrendData["by_status"]> = false;
  const calibratePrecisionIsDeliberatelyUnknown: Exact<
    CalibrateData["precision"],
    unknown
  > = true;
  const fixKBIsDeliberatelyUnknown: Exact<FixData["kb"], unknown> = true;
  void readyStatusesAreNotAny;
  void calibratePrecisionIsDeliberatelyUnknown;
  void fixKBIsDeliberatelyUnknown;

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
