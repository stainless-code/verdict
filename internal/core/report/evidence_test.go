package reportcore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/readcommon"
	reportcli "github.com/stainless-code/verdict/internal/report"
)

func writeRecordWithEvidence(t *testing.T, ev *core.EvidenceReceipt) {
	t.Helper()
	rec := core.Record{
		SchemaVersion: "1",
		Status:        "pr",
		Head:          storeHeadSHA,
		Evidence:      ev,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	writeFile(t, ".verdict/state/5101/record.json", string(raw))
}

func runReportExitCode(t *testing.T, args ...string) int {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: fixedRuntime().Now}
	return reportcli.Run(context.Background(), r, args, reportcli.Request{Repo: "acme/app", Pr: "5101"})
}

func findEvidenceDim(t *testing.T, resp *reportcore.Response) core.GateDimension {
	t.Helper()
	if resp.Data.Gate == nil {
		t.Fatal("data.gate is nil on --check run")
	}
	for _, dim := range resp.Data.Gate.Dimensions {
		if dim.ID == "evidence" {
			return dim
		}
	}
	t.Fatalf("evidence dimension missing from %+v", resp.Data.Gate.Dimensions)
	return core.GateDimension{}
}

func TestEvidenceGatePass(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       storeHeadSHA,
		DiffSHA256: strings.Repeat("d", 64),
		Quotes: []core.EvidenceQuote{
			{Path: "a.go", Side: "RIGHT", Status: "verified", Line: 10},
			{Path: "b.go", Side: "RIGHT", Status: "verified", Line: 20},
		},
		Receipts:     core.EvidenceReceipts{Total: 4, Covered: 4, Exempt: 0, Complete: true},
		Spec:         core.EvidenceSpec{Total: 10, Covered: 8, Min: 80, Met: true},
		Insufficient: false,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("GateFailed = true, want false (lines %q)", resp.CheckLines)
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "pass" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "pass")
	}
	const wantLine = "check: evidence checks satisfied (2 quotes verified, 4/4 hunks receipted, 8/10 spec items checked) — gate passes"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runReportExitCode(t, "--check", "important", "--live", "skip"); code != cli.ExitOK {
		t.Fatalf("report CLI exit = %d, want %d", code, cli.ExitOK)
	}
	requireApproval(t, resp, true, false, "no gating findings")
}

func TestEvidenceGateFailInsufficient(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       storeHeadSHA,
		DiffSHA256: strings.Repeat("d", 64),
		Quotes: []core.EvidenceQuote{
			{Path: "a.go", Side: "RIGHT", Status: "unverified", Reason: "text-differs", Line: 10},
			{Path: "b.go", Side: "RIGHT", Status: "unverified", Reason: "path-not-in-bundle", Line: 5},
		},
		Gaps:         []string{"quote gap 1", "quote gap 2", "hunk gap"},
		Receipts:     core.EvidenceReceipts{Total: 4, Covered: 3, Exempt: 0, Complete: false},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: true,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for insufficient evidence")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "fail" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "fail")
	}
	const wantLine = "check: evidence checks insufficient (2 quote gap(s), 1 hunk without receipt) [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runReportExitCode(t, "--check", "important", "--live", "skip"); code != cli.ExitRefused {
		t.Fatalf("report CLI exit = %d, want %d", code, cli.ExitRefused)
	}
}

func TestEvidenceGateMissingIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	if err := os.Remove(".verdict/state/5101/record.json"); err != nil {
		t.Fatal(err)
	}
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for missing evidence receipt")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt missing for head aaaaaaa [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runReportExitCode(t, "--check", "important", "--live", "skip"); code != cli.ExitRefused {
		t.Fatalf("report CLI exit = %d, want %d", code, cli.ExitRefused)
	}
	// R3: no legacy path. A bundle with no receipt may not read as approved
	// beside the failing gate dimension.
	requireApproval(t, resp, false, true, "review-insufficient: evidence receipt missing for head aaaaaaa")
}

func TestEvidenceGateStaleIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:         strings.Repeat("c", 40),
		DiffSHA256:   strings.Repeat("d", 64),
		Receipts:     core.EvidenceReceipts{Total: 1, Covered: 1, Complete: true},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: false,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for stale evidence receipt")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt stale for head aaaaaaa (recorded ccccccc) [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runReportExitCode(t, "--check", "important", "--live", "skip"); code != cli.ExitRefused {
		t.Fatalf("report CLI exit = %d, want %d", code, cli.ExitRefused)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: evidence receipt stale for head aaaaaaa (recorded ccccccc)")
}

func TestEvidenceGateUnreadableIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/record.json", `{broken`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for unreadable record.json")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt unreadable for head aaaaaaa [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
}

func TestEvidenceAdvisoryReportWarnsAndNeverFails(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:         storeHeadSHA,
		DiffSHA256:   strings.Repeat("d", 64),
		Gaps:         []string{"hunk a.go\tNEW\t1\t3 has no matched receipt"},
		Receipts:     core.EvidenceReceipts{Total: 1, Covered: 0, Complete: false},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: true,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatal("advisory report set GateFailed = true")
	}
	const wantWarn = "evidence: evidence checks insufficient (1 hunk without receipt) [review-insufficient]"
	if !containsLine(resp.Warnings, wantWarn) {
		t.Fatalf("Warnings = %q, want %q", resp.Warnings, wantWarn)
	}
	if code := runReportExitCode(t); code != cli.ExitOK {
		t.Fatalf("advisory report CLI exit = %d, want %d", code, cli.ExitOK)
	}
}

// requireApproval asserts the report's approval decision, which a reviewer
// reads beside the gate: a withheld gate may never sit next to an approval.
func requireApproval(t *testing.T, resp *reportcore.Response, approved, insufficient bool, reason string) {
	t.Helper()
	got := resp.Data.Approval
	if got == nil {
		t.Fatal("report data omits the approval decision")
	}
	if got.Approved != approved || got.Insufficient != insufficient || got.Reason != reason {
		t.Fatalf("approval = %+v, want approved=%v insufficient=%v reason=%q",
			got, approved, insufficient, reason)
	}
}

// The two-finder fixtures below state one finding, one cross check, and one
// recorded execution. The sig and claim are the plan's own identity, so a
// changed claim cannot silently pass the bound plan.
const (
	crossSig    = "1fd35ceaaa1fc0a98e1486617b35249c646ee577"
	crossHead   = storeHeadSHA
	crossBase   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	crossDiff   = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-return safeValue\n+return unsafeValue\n"
	crossReview = `{"schema_version":"2","execution":{"id":"execution-A","finders":2},"findings":[` +
		`{"path":"notes.txt","line":1,"side":"RIGHT","severity":"important",` +
		`"claim":"Unhandled await error path here.","quote":"return unsafeValue","confidence":"high"}]}`
)

// crossRequest is the read half every cross-gate case reviews: the one pulled
// request identity the report resolves its bundle from.
func crossRequest() core.ReadRequest {
	return core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "5101"}
}

func digestOf(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// crossEntryJSON is one stored finding: quote verified, cross absent, so the
// only standing it can carry is the one the panel derives.
func crossEntryJSON(severity string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":1,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + crossSig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[],"verification":{"quote":"verified"}}`
}

// crossBundle writes a two-finder bundle the report can decide: the captured
// run policy, the manifest and receipt bound to the reviewed bytes, and the
// current evidence receipt. It returns the bundle directory.
func crossBundle(t *testing.T, entry string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	store := ".verdict/state/5101"
	writeFile(t, store+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+crossHead+`","baseRefOid":"`+crossBase+`"}`)
	writeFile(t, store+"/findings.json", "["+entry+"]")
	writeFile(t, store+"/full.diff", crossDiff)
	writeFile(t, store+"/review.json", crossReview)
	plan := `{"checks":[{"sig":"` + crossSig + `","origin":"finder-1","checker":"finder-2"}]}`
	writeFile(t, store+"/verification/plan.json", plan)
	writeFile(t, store+"/launcher.json",
		`{"schema_version":"2","execution_id":"execution-A","status":"complete",`+
			`"head_sha":"`+crossHead+`","diff_sha256":"`+digestOf(crossDiff)+`",`+
			`"review_sha256":"`+digestOf(crossReview)+`","plan_sha256":"`+digestOf(plan)+`"}`)
	writeFile(t, store+"/verification/verdicts.json",
		`{"schema_version":"2","execution_id":"execution-A",`+
			`"head_sha":"`+crossHead+`","diff_sha256":"`+digestOf(crossDiff)+`",`+
			`"review_sha256":"`+digestOf(crossReview)+`","plan_sha256":"`+digestOf(plan)+`",`+
			`"results":[{"sig":"`+crossSig+`","checker":"finder-2","verdict":"confirmed",`+
			`"evidence":"The selected branch changes the return value.",`+
			`"quote":{"path":"notes.txt","side":"RIGHT","line":1,"text":"return unsafeValue"}}]}`)
	rs := &config.RunState{
		BaseSHA:   crossBase,
		Trusted:   true,
		Execution: &core.ExecutionReference{ID: "execution-A", Finders: 2},
		Finders:   2,
	}
	if serr := config.WriteRunState(store, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: store, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       crossHead,
		DiffSHA256: digestOf(crossDiff),
		Receipts:   core.EvidenceReceipts{Total: 0, Covered: 0, Exempt: 0, Complete: true},
		Spec:       core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
	})
}

// recordedBundle writes the same finding with no live capture at all, but the
// stored receipt still states the two finders that recorded it.
func recordedBundle(t *testing.T, entry string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	store := ".verdict/state/5101"
	writeFile(t, store+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+crossHead+`","baseRefOid":"`+crossBase+`"}`)
	writeFile(t, store+"/findings.json", "["+entry+"]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       crossHead,
		DiffSHA256: digestOf(crossDiff),
		Receipts:   core.EvidenceReceipts{Total: 0, Covered: 0, Exempt: 0, Complete: true},
		Spec:       core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
	})
	writeFile(t, store+"/record.json", `{"schema_version":"1","status":"pr","head":"`+crossHead+`",`+
		`"verification":{"finders":{"completed":2,"confirmed":1,"contested":0,"pending":0}},`+
		`"evidence":{"head":"`+crossHead+`","diff_sha256":"`+digestOf(crossDiff)+`",`+
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},`+
		`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`)
}

// TestCrossGateFailsStrippedCrossAfterRecordedExecution proves a finding whose
// cross stamp was stripped still cannot gate: the captured execution the
// receipt states requires the stamp, no bound artifact remains to restore it,
// and the gate fails on it instead of passing over a silently tagged finding.
func TestCrossGateFailsStrippedCrossAfterRecordedExecution(t *testing.T) {
	recordedBundle(t, crossEntryJSON("important"))
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: crossRequest(),
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for an unresolvable cross check")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "fail" {
		t.Fatalf("evidence status = %q, want fail", dim.Status)
	}
	const wantLine = "check: a finding within the requested tier lacks a confirmed cross check — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: execution: captured execution artifacts missing blocking cross check missing, unverified or contested")
}

// TestCrossGateIgnoresForgedStamp proves a stored confirmed stamp is not
// evidence: with no bound artifact behind it, the finding stays ineligible and
// the approval stays insufficient.
func TestCrossGateIgnoresForgedStamp(t *testing.T) {
	recordedBundle(t, `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":1,`+
		`"anchored":true,"body":"Usable body.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":"important",`+
		`"sig":"`+crossSig+`","sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",`+
		`"rationale":"","history":[],"verification":{"quote":"verified",`+
		`"cross":{"execution_id":"execution-A","status":"confirmed","expected":["finder-2"],`+
		`"checks":[{"checker":"finder-2","verdict":"confirmed","quote_status":"verified"}]}}}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: crossRequest(),
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for a forged cross stamp")
	}
	if dim := findEvidenceDim(t, resp); dim.Status != "fail" {
		t.Fatalf("evidence status = %q, want fail", dim.Status)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: execution: captured execution artifacts missing blocking cross check missing, unverified or contested")
}

// TestCrossGateRestoresStrippedCrossFromBindings proves the recompute: the same
// stripped finding, with its bound receipt intact, becomes an eligible gating
// finding again, so the gate fails on the finding rather than on missing
// evidence and the approval withholds without claiming insufficiency.
func TestCrossGateRestoresStrippedCrossFromBindings(t *testing.T) {
	crossBundle(t, crossEntryJSON("important"))
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: crossRequest(),
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for an eligible gating finding")
	}
	if dim := findEvidenceDim(t, resp); dim.Status != "pass" {
		t.Fatalf("evidence status = %q, want pass (restored confirmation)", dim.Status)
	}
	requireApproval(t, resp, false, false, "open findings at-or-above important")
	if len(resp.Data.Approval.GatingSigs) != 1 || resp.Data.Approval.GatingSigs[0] != crossSig {
		t.Fatalf("gating sigs = %v, want [%s]", resp.Data.Approval.GatingSigs, crossSig)
	}
	entry := resp.Data.Items[0].Raw
	if entry == nil {
		t.Fatal("item raw is nil, want the stamped entry")
	}
	cross := readcommon.FieldOf(readcommon.FieldOf(entry, "verification"), "cross")
	if readcommon.FieldOf(cross, "status") != "confirmed" {
		t.Fatalf("stamped cross = %v, want the bound confirmation restored", cross)
	}
}

// TestCrossGateManualCaptureNeedsNoCross proves the opt-out is untouched: a
// one-finder capture keeps quote-only eligibility, so the same finding with no
// cross stamp neither gates nor withholds.
func TestCrossGateManualCaptureNeedsNoCross(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	store := ".verdict/state/5101"
	writeFile(t, store+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+crossHead+`","baseRefOid":"`+crossBase+`"}`)
	writeFile(t, store+"/findings.json", "["+crossEntryJSON("informational")+"]")
	rs := &config.RunState{BaseSHA: crossBase, Trusted: true, Finders: 1}
	if serr := config.WriteRunState(store, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: store, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       crossHead,
		DiffSHA256: digestOf(crossDiff),
		Receipts:   core.EvidenceReceipts{Total: 0, Covered: 0, Exempt: 0, Complete: true},
		Spec:       core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: crossRequest(),
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("GateFailed = true for a manual capture (lines %q)", resp.CheckLines)
	}
	requireApproval(t, resp, true, false, "no gating findings")
}
