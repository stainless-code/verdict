package reportcore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The two-finder fixtures below state one finding, one cross check, and one
// recorded execution. The sig and claim are the plan's own identity, so a
// changed claim cannot silently pass the bound plan.
const (
	crossSig    = "1fd35ceaaa1fc0a98e1486617b35249c646ee577"
	crossBase   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	crossStore  = ".verdict/state/5101"
	crossDiff   = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-return safeValue\n+return unsafeValue\n"
	crossReview = `{"schema_version":"2","execution":{"id":"execution-A","finders":2},"findings":[` +
		`{"path":"notes.txt","line":1,"side":"RIGHT","severity":"important",` +
		`"claim":"Unhandled await error path here.","quote":"return unsafeValue","confidence":"high"}]}`
	crossLine = "a finding within the requested tier lacks a confirmed cross check"
)

func crossRequest() *reportcore.Request {
	read := core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "5101"}
	return &reportcore.Request{ReadRequest: read, Flags: reportcore.Flags{Check: "important", Live: "skip"}}
}

func digestOf(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// crossEntryJSON is one stored finding: quote verified and the given cross
// suffix, so the only standing it can carry is the one the panel derives.
func crossEntryJSON(severity, cross string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":1,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + crossSig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[],"verification":{"quote":"verified"` + cross + `}}`
}

// crossBundleBase writes the PR, the store, and a current evidence receipt.
func crossBundleBase(t *testing.T, entry string) {
	t.Helper()
	t.Chdir(t.TempDir())
	writeFile(t, crossStore+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+storeHeadSHA+`","baseRefOid":"`+crossBase+`"}`)
	writeFile(t, crossStore+"/findings.json", "["+entry+"]") // an empty entry is an empty store
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       storeHeadSHA,
		DiffSHA256: digestOf(crossDiff),
		Receipts:   core.EvidenceReceipts{Complete: true},
		Spec:       core.EvidenceSpec{Min: 80, Met: true},
	})
}

// writeCrossCapture writes the typed run capture and the controls it needs.
func writeCrossCapture(t *testing.T, rs *config.RunState) {
	t.Helper()
	rs.BaseSHA, rs.Trusted = crossBase, true
	if err := config.WriteRunState(crossStore, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: crossStore, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// crossBundle writes a live two-finder bundle the report can decide: the
// captured run, and the manifest and receipt bound to the reviewed bytes.
func crossBundle(t *testing.T, entry string) {
	t.Helper()
	crossBundleBase(t, entry)
	writeFile(t, crossStore+"/full.diff", crossDiff)
	writeFile(t, crossStore+"/review.json", crossReview)
	plan := `{"checks":[{"sig":"` + crossSig + `","origin":"finder-1","checker":"finder-2"}]}`
	writeFile(t, crossStore+"/verification/plan.json", plan)
	bindings := `"head_sha":"` + storeHeadSHA + `","diff_sha256":"` + digestOf(crossDiff) + `",` +
		`"review_sha256":"` + digestOf(crossReview) + `","plan_sha256":"` + digestOf(plan) + `"`
	writeFile(t, crossStore+"/launcher.json",
		`{"schema_version":"2","execution_id":"execution-A","status":"complete",`+bindings+`}`)
	writeFile(t, crossStore+"/verification/verdicts.json",
		`{"schema_version":"2","execution_id":"execution-A",`+bindings+`,`+
			`"results":[{"sig":"`+crossSig+`","checker":"finder-2","verdict":"confirmed",`+
			`"evidence":"The selected branch changes the return value.",`+
			`"quote":{"path":"notes.txt","side":"RIGHT","line":1,"text":"return unsafeValue"}}]}`)
	writeCrossCapture(t, &config.RunState{
		Execution: &core.ExecutionReference{ID: "execution-A", Finders: 2},
		Finders:   2,
	})
}

// incompleteBundle writes the live two-finder bundle as a run leaves it when
// its cross check exhausted its rounds: the manifest is partial and names the
// failed phase, and the receipt holds no row for the finding.
func incompleteBundle(t *testing.T, entry string) {
	t.Helper()
	crossBundleBase(t, entry)
	writeFile(t, crossStore+"/full.diff", crossDiff)
	writeFile(t, crossStore+"/review.json", crossReview)
	plan := `{"checks":[{"sig":"` + crossSig + `","origin":"finder-1","checker":"finder-2"}]}`
	writeFile(t, crossStore+"/verification/plan.json", plan)
	bindings := `"head_sha":"` + storeHeadSHA + `","diff_sha256":"` + digestOf(crossDiff) + `",` +
		`"review_sha256":"` + digestOf(crossReview) + `","plan_sha256":"` + digestOf(plan) + `"`
	writeFile(t, crossStore+"/launcher.json",
		`{"schema_version":"2","execution_id":"execution-A","status":"partial",`+bindings+`,`+
			`"failures":[{"phase":"x-cross-2","code":"cross-incomplete"}]}`)
	writeFile(t, crossStore+"/verification/verdicts.json",
		`{"schema_version":"2","execution_id":"execution-A",`+bindings+`,"results":[]}`)
	writeCrossCapture(t, &config.RunState{
		Execution: &core.ExecutionReference{ID: "execution-A", Finders: 2},
		Finders:   2,
	})
}

// TestCrossGateIncompleteExecutionNeverReadsAsNoFindings proves an open
// in-tier finding an incomplete execution left unchecked is counted by the
// findings dimension as unknown, never reported as absent, while the evidence
// dimension still fails and names the incomplete execution.
func TestCrossGateIncompleteExecutionNeverReadsAsNoFindings(t *testing.T) {
	incompleteBundle(t, crossEntryJSON("important", ""))
	resp := runCross(t)
	if !resp.GateFailed {
		t.Fatalf("GateFailed = false, want true\n%s", dimSummary(resp))
	}
	findings := dimOf(t, resp, "findings")
	wantFindings := "1 open finding(s) at-or-above important await the cross checks of an incomplete " +
		"execution (execution manifest is incomplete) — unknown, never a pass"
	if findings.Status != "unknown" || findings.Count == nil || *findings.Count != 1 || findings.Detail != wantFindings {
		t.Fatalf("findings = %+v, want unknown 1 %q", findings, wantFindings)
	}
	evidence := findEvidenceDim(t, resp)
	if evidence.Status != "fail" || evidence.Detail != crossLine+" (the execution is incomplete)" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

// TestCrossGateIncompleteExecutionResumesInEveryMode proves any execution that
// stopped before it finished leaves the gate failed and marked for `run
// --resume`, whichever dimensions were asked for: a finding below the tier,
// outside the category, or absent from the store still reads unknown rather
// than absent, a missing aggregate plan or panel receipt is the same stopped
// run, and a gate on the head alone still names the recovery.
func TestCrossGateIncompleteExecutionResumesInEveryMode(t *testing.T) {
	tier := reportcore.Flags{Check: "important", Live: "skip"}
	for _, tc := range []struct {
		prepare  func(t *testing.T)
		name     string
		entry    string
		flags    reportcore.Flags
		findings bool
	}{
		{name: "below the tier", entry: crossEntryJSON("informational", ""), flags: tier, findings: true},
		{
			name: "outside the category", entry: crossEntryJSON("important", ""), findings: true,
			flags: reportcore.Flags{Check: "important", Category: "security", Live: "skip"},
		},
		{name: "absent", flags: tier, findings: true},
		{
			name: "plan missing", entry: crossEntryJSON("important", ""), flags: tier, findings: true,
			prepare: func(t *testing.T) { t.Helper(); removeFile(t, crossStore+"/verification/plan.json") },
		},
		{
			name: "receipt missing", entry: crossEntryJSON("important", ""), flags: tier, findings: true,
			prepare: func(t *testing.T) { t.Helper(); removeFile(t, crossStore+"/verification/verdicts.json") },
		},
		{name: "head only", entry: crossEntryJSON("important", ""), flags: reportcore.Flags{Completeness: true, Live: "skip"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incompleteBundle(t, tc.entry)
			if tc.prepare != nil {
				tc.prepare(t)
			}
			assertResumeGate(t, &tc.flags, tc.findings)
		})
	}
}

// assertResumeGate runs the gate over the stopped execution the bundle holds
// and requires it failed, marked for resume, and — when the gate asked for
// findings — reading them unknown.
func assertResumeGate(t *testing.T, flags *reportcore.Flags, findings bool) {
	t.Helper()
	req := crossRequest()
	req.Flags = *flags
	resp, ref := reportcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed || resp.Gate == nil || !resp.Gate.Resume {
		t.Fatalf("gate = %+v, want failed and marked for resume\n%s", resp.Gate, dimSummary(resp))
	}
	if !findings {
		return
	}
	if dim := dimOf(t, resp, "findings"); dim.Status != "unknown" {
		t.Fatalf("findings = %+v, want unknown", dim)
	}
}

// TestCrossGatePartialExecutionResumeRefusesNeverPasses proves a stopped
// execution whose gap --resume refuses — here a manifest whose plan binding no
// longer matches — still reads its findings unknown, never "no open findings",
// and marks the gate for a fresh run naming the gap instead of a resume.
func TestCrossGatePartialExecutionResumeRefusesNeverPasses(t *testing.T) {
	incompleteBundle(t, crossEntryJSON("informational", ""))
	launcher := crossStore + "/launcher.json"
	raw, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, launcher, strings.Replace(string(raw), `"plan_sha256":"`, `"plan_sha256":"0`, 1))
	resp := runCross(t)
	if !resp.GateFailed || resp.Gate == nil || resp.Gate.Resume ||
		resp.Gate.Restart != "execution manifest binding mismatch: plan_sha256" {
		t.Fatalf("gate = %+v, want failed and marked for a fresh run\n%s", resp.Gate, dimSummary(resp))
	}
	if dim := dimOf(t, resp, "findings"); dim.Status != "unknown" {
		t.Fatalf("findings = %+v, want unknown", dim)
	}
}

// TestCrossGateCompleteExecutionNeverResumes proves the resume marker belongs
// to a stopped run alone: the finished execution the other cross tests use
// gates without it.
func TestCrossGateCompleteExecutionNeverResumes(t *testing.T) {
	crossBundle(t, crossEntryJSON("important", ""))
	if resp := runCross(t); resp.Gate == nil || resp.Gate.Resume {
		t.Fatalf("gate = %+v, want no resume marker", resp.Gate)
	}
}

func removeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// recordedBundle writes the same finding with no live capture at all, but the
// stored record still states the two finders that recorded it.
func recordedBundle(t *testing.T, entry string) {
	t.Helper()
	crossBundleBase(t, entry)
	writeFile(t, crossStore+"/record.json", `{"schema_version":"1","status":"pr","head":"`+storeHeadSHA+`",`+
		`"verification":{"finders":{"completed":2,"confirmed":1,"contested":0,"pending":0}},`+
		`"evidence":{"head":"`+storeHeadSHA+`","diff_sha256":"`+digestOf(crossDiff)+`",`+
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},`+
		`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`)
}

func runCross(t *testing.T) *reportcore.Response {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), crossRequest())
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

// TestCrossGateFailsStrippedCrossAfterRecordedExecution proves a finding whose
// cross stamp was stripped still cannot gate as a manual finding: the stored
// record states the two-finder capture that requires the stamp, no bound
// artifact remains to restore it, and the evidence dimension fails on it.
func TestCrossGateFailsStrippedCrossAfterRecordedExecution(t *testing.T) {
	recordedBundle(t, crossEntryJSON("important", ""))
	resp := runCross(t)
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for an unresolvable cross check")
	}
	if dim := findEvidenceDim(t, resp); dim.Status != "fail" || dim.Detail != crossLine {
		t.Fatalf("evidence = %s %q, want fail %q", dim.Status, dim.Detail, crossLine)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: execution: captured execution artifacts missing blocking cross check missing, unverified or contested")
}

// TestCrossGateIgnoresForgedStamp proves a stored confirmed stamp is not
// evidence: with no bound artifact behind it, the finding stays ineligible and
// the approval stays insufficient.
func TestCrossGateIgnoresForgedStamp(t *testing.T) {
	recordedBundle(t, crossEntryJSON("important", `,"cross":{"execution_id":"execution-A","status":"confirmed",`+
		`"expected":["finder-2"],"checks":[{"checker":"finder-2","verdict":"confirmed","quote_status":"verified"}]}`))
	resp := runCross(t)
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for a forged cross stamp")
	}
	if dim := findEvidenceDim(t, resp); dim.Status != "fail" || dim.Detail != crossLine {
		t.Fatalf("evidence = %s %q, want fail %q", dim.Status, dim.Detail, crossLine)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: execution: captured execution artifacts missing blocking cross check missing, unverified or contested")
}

// TestCrossGateRestoresStrippedCrossFromBindings proves the recompute: the
// same stripped finding, with its bound receipt intact, becomes an eligible
// gating finding again, so the gate fails on the finding rather than on
// missing evidence and the approval withholds without claiming insufficiency.
func TestCrossGateRestoresStrippedCrossFromBindings(t *testing.T) {
	crossBundle(t, crossEntryJSON("important", ""))
	resp := runCross(t)
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for an eligible gating finding")
	}
	if dim := findEvidenceDim(t, resp); dim.Status != "pass" {
		t.Fatalf("evidence = %s %q, want pass (restored confirmation)", dim.Status, dim.Detail)
	}
	requireApproval(t, resp, false, false, "open findings at-or-above important")
	if got := resp.Data.Approval.GatingSigs; len(got) != 1 || got[0] != crossSig {
		t.Fatalf("gating sigs = %v, want [%s]", got, crossSig)
	}
	cross := readcommon.FieldOf(readcommon.FieldOf(resp.Data.Items[0].Raw, "verification"), "cross")
	if readcommon.FieldOf(cross, "status") != "confirmed" {
		t.Fatalf("stamped cross = %v, want the bound confirmation restored", cross)
	}
}

// TestCrossGateManualCaptureNeedsNoCross proves the opt-out is untouched: a
// one-finder capture keeps quote-only eligibility, so the same finding with
// no cross stamp neither gates nor withholds below the tier.
func TestCrossGateManualCaptureNeedsNoCross(t *testing.T) {
	crossBundleBase(t, crossEntryJSON("informational", ""))
	writeCrossCapture(t, &config.RunState{Finders: 1})
	resp := runCross(t)
	if resp.GateFailed {
		t.Fatalf("GateFailed = true for a manual capture (gate %+v)", resp.Gate.Dimensions)
	}
	requireApproval(t, resp, true, false, "no gating findings")
}
