package postcore

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	reqHeadSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	reqBaseSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reqMovedSHA = "cccccccccccccccccccccccccccccccccccccccc"
	reqBundle   = ".verdict/state/7"
	reqGroupA   = "a1b2c3d4e5f60718"
	reqGroupB   = "0192837465afbecd"
	reqProfile  = "deep-review"
)

const reqReview = `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`

const reqDiff = "diff --git a/a.txt b/a.txt\nindex 1111111..2222222 100644\n" +
	"--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n top\n-old a\n+new a\n" +
	"diff --git a/b.txt b/b.txt\nindex 3333333..4444444 100644\n" +
	"--- a/b.txt\n+++ b/b.txt\n@@ -1,2 +1,2 @@\n top\n-old b\n+new b\n"

// reqPlan is the hand-authored typed plan: two groups share one profile label
// while owning different files and different selected checks, so a verdict
// for one can never stand in for the other.
func reqPlan() config.Plan {
	return config.Plan{Status: config.PendingStatus, Groups: []config.PlanGroup{
		{
			ID: reqGroupA, Label: reqProfile, Profile: reqProfile, Depth: "standard",
			Files:  []string{"a.txt"},
			Checks: []string{config.CheckNoOpenAboveBlock, config.CheckHeadCurrent},
		},
		{
			ID: reqGroupB, Label: reqProfile, Profile: reqProfile, Depth: "standard",
			Files:  []string{"b.txt"},
			Checks: []string{config.CheckNoOpenAboveBlock, config.CheckHeadCurrent, config.CheckNoLiveThreads},
		},
	}}
}

// reqRoot is the captured root requirement set: one clause covering every
// stored finding, and the findings check every group selects.
func reqRoot() config.RequirementSet {
	return config.RequirementSet{
		Clauses: []config.ClauseProvenance{{
			Clause:  core.BlockClause{Name: "block-all", Threshold: "critical", Paths: []string{"**"}},
			Sources: []string{"repo"},
		}},
		Checks: []string{config.CheckNoOpenAboveBlock},
	}
}

// reqRunState is the captured bundle evidence for the two-group plan.
func reqRunState() *config.RunState {
	root := reqRoot()
	files := make([]config.FileResolution, 0, len(reqPlan().Groups))
	groups := reqPlan().Groups
	for i := range groups {
		files = append(files, config.FileResolution{
			Path:         groups[i].Files[0],
			Requirements: config.RequirementSet{Clauses: slices.Clone(root.Clauses), Checks: slices.Clone(root.Checks)},
		})
	}
	return &config.RunState{
		BaseSHA:    reqBaseSHA,
		Trusted:    true,
		CapturedAt: "2026-11-06T00:00:00Z",
		Root:       root,
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: reqBaseSHA, Plan: reqPlan()},
		Resolved:   &config.Resolved{Files: files, Plan: reqPlan()},
	}
}

func seedRequirementBundle(t *testing.T, prior []config.CheckObservation) {
	t.Helper()
	files := []struct{ path, body string }{
		{reqBundle + "/PR.json", `{"state":"OPEN","headRefOid":"` + reqHeadSHA + `","baseRefOid":"` + reqBaseSHA + `",` +
			`"author":{"login":"someone-else"}}`},
		{reqBundle + "/full.diff", reqDiff},
		{reqBundle + "/review.json", reqReview},
	}
	for i := range files {
		if err := writeTestFile(files[i].path, files[i].body); err != nil {
			t.Fatal(err)
		}
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: reqBundle, BaseSHA: reqBaseSHA, AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	rs := reqRunState()
	rs.Inventory = config.InventoryHashes(snap)
	rs.Outcomes = prior
	if serr := config.WriteRunState(reqBundle, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
}

// requirementCommander scripts the full post path with a chosen live head
// and a chosen number of unresolved live threads; a nil head or thread
// count fails the read instead.
func requirementCommander(t *testing.T, live *string, unresolved int, threadsFail bool) *scriptCommander {
	t.Helper()
	cmdr := postCommander(t)
	cmdr.out["gh\x00api\x00user\x00--jq\x00.login"] = scriptResult{stdout: "reviewer\n"}
	if live == nil {
		cmdr.out[liveSHAArgv] = scriptResult{stderr: "HTTP 404: Not Found\n", fail: true}
	} else {
		cmdr.out[liveSHAArgv] = scriptResult{stdout: *live + "\n"}
	}
	threads := "gh\x00api\x00graphql\x00--paginate\x00-F\x00owner=acme\x00-F\x00name=app\x00-F\x00pr=7\x00-f\x00query=" + queryLiveCount
	if threadsFail {
		cmdr.out[threads] = scriptResult{stderr: "HTTP 502: Bad Gateway\n", fail: true}
		return cmdr
	}
	nodes := make([]string, 0, unresolved)
	for range unresolved {
		nodes = append(nodes, `{"isResolved":false}`)
	}
	cmdr.out[threads] = scriptResult{stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
		strings.Join(nodes, ",") + `]}}}}}` + "\n"}
	return cmdr
}

func readLedger(t *testing.T) *config.RunState {
	t.Helper()
	rs, found, serr := config.ReadRunState(reqBundle)
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	return rs
}

func ledgerVerdict(t *testing.T, rs *config.RunState, group, check, head string) (config.CheckObservation, bool) {
	t.Helper()
	for i := range slices.Backward(rs.Outcomes) {
		if rs.Outcomes[i].Group == group && rs.Outcomes[i].Check == check && rs.Outcomes[i].Head == head {
			return rs.Outcomes[i], true
		}
	}
	return config.CheckObservation{}, false
}

func runApproval(t *testing.T, cmdr *scriptCommander, force bool) (*core.PostResponse, *core.RefusalError) {
	t.Helper()
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, Force: force, ReviewFile: reqBundle + "/review.json",
	}, &warnings, &human)
	return Run(context.Background(), req)
}

// TestRunApprovalRequiresEveryGroupCheck pins the per-group conjunction: two
// groups sharing one profile label are still two identities, the failing
// group is the only one named, and the passing group's evidence never stands
// in for the unmet check.
//
//nolint:revive // legs assert the refusal, the payload, and the ledger inline
func TestRunApprovalRequiresEveryGroupCheck(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 2, false)
	resp, ref := runApproval(t, cmdr, false)
	if resp != nil || ref == nil {
		t.Fatalf("run = %v, %v; want a refusal", resp, ref)
	}
	if ref.Code != CodeRequirementUnmet {
		t.Fatalf("refusal code = %q, want %q (%s)", ref.Code, CodeRequirementUnmet, ref.Message)
	}
	for _, want := range []string{config.CheckNoLiveThreads, reqGroupB, reqProfile, "2"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q does not name %q", ref.Message, want)
		}
	}
	if strings.Contains(ref.Message, reqGroupA) {
		t.Errorf("refusal %q blames the passing group", ref.Message)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %v on a refused approval", posted)
	}
	assertLedgerVerdicts(t, readLedger(t), []ledgerWant{
		{reqGroupA, config.CheckNoOpenAboveBlock, true},
		{reqGroupA, config.CheckHeadCurrent, true},
		{reqGroupB, config.CheckNoOpenAboveBlock, true},
		{reqGroupB, config.CheckNoLiveThreads, false},
	})
}

// ledgerWant is one expected (group, check, passed) ledger row.
type ledgerWant struct {
	group string
	check string
	pass  bool
}

// assertLedgerVerdicts proves each expectation, that no other group gained a
// no-live-threads row, and that every row carries an attributable group.
//
//nolint:revive // one helper carries the three independent ledger expectations
func assertLedgerVerdicts(t *testing.T, ledger *config.RunState, wants []ledgerWant) {
	t.Helper()
	for _, want := range wants {
		obs, found := ledgerVerdict(t, ledger, want.group, want.check, reqHeadSHA)
		if !found {
			t.Errorf("ledger has no %s observation for %s", want.check, want.group)
			continue
		}
		if obs.Passed != want.pass {
			t.Errorf("%s/%s passed = %v, want %v", want.group, want.check, obs.Passed, want.pass)
		}
	}
	if _, found := ledgerVerdict(t, ledger, reqGroupA, config.CheckNoLiveThreads, reqHeadSHA); found {
		t.Error("the group that never selected no-live-threads got an observation for it")
	}
	for i := range ledger.Outcomes {
		if ledger.Outcomes[i].Group == "" || ledger.Outcomes[i].Group == "not-a-group" {
			t.Errorf("ledger carries an unattributable observation: %+v", ledger.Outcomes[i])
		}
	}
}

// TestRunFirstApprovalPassesFreshHead pins the satisfiable first approval: a
// captured head equal to the live host head passes every selected check with
// no prior post, and the ledger records each verdict against the reviewed
// head.
//
//nolint:revive // legs assert the event, the payload, and the ledger inline
func TestRunFirstApprovalPassesFreshHead(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 0, false)
	resp, ref := runApproval(t, cmdr, false)
	if ref != nil {
		t.Fatalf("Run refusal: %v (%s)", ref, ref.Message)
	}
	if resp.Data.Event != "APPROVE" {
		t.Fatalf("event = %q, want APPROVE", resp.Data.Event)
	}
	posted := postedPayloads(t, cmdr)
	if len(posted) != 1 || posted[0]["event"] != "APPROVE" {
		t.Fatalf("posted = %v, want one APPROVE review", posted)
	}
	ledger := readLedger(t)
	for _, scope := range config.CheckScopes(ledger.Resolved.Plan, &ledger.Root) {
		status := config.ObserveScopes([]config.CheckScope{scope}, ledger.Outcomes, reqHeadSHA)
		for _, check := range scope.Checks {
			if !status[0].Checks[check] {
				t.Errorf("group %s has no passing %s evidence after a passing approval", scope.ID, check)
			}
		}
	}
}

// TestRunStaleHeadRefusesEvenWithForce pins the head-current requirement: a
// moved live head forbids the approval no matter what --force says.
func TestRunStaleHeadRefusesEvenWithForce(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	live := reqMovedSHA
	cmdr := requirementCommander(t, &live, 0, false)
	resp, ref := runApproval(t, cmdr, true)
	if resp != nil || ref == nil || ref.Code != CodeRequirementUnmet {
		t.Fatalf("run = %v, %v; want %q", resp, ref, CodeRequirementUnmet)
	}
	for _, want := range []string{config.CheckHeadCurrent, reqGroupA, readcommon.ShortSHA(reqMovedSHA)} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q does not name %q", ref.Message, want)
		}
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %v under --force with a stale head", posted)
	}
}

// TestRunUnreachableHeadRefuses pins the missing-evidence rule: an
// unreadable live head is not a pass, so the approval refuses.
func TestRunUnreachableHeadRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	cmdr := requirementCommander(t, nil, 0, false)
	_, ref := runApproval(t, cmdr, false)
	if ref == nil || ref.Code != CodeRequirementUnmet || !strings.Contains(ref.Message, "lookup failed") {
		t.Fatalf("refusal = %v, want %q naming the failed lookup", ref, CodeRequirementUnmet)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %v with no head evidence", posted)
	}
}

// TestRunUnreachableThreadsRefuses pins the same rule for the live thread
// read, and proves zero is the bar rather than the stored-open comparison.
func TestRunUnreachableThreadsRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 0, true)
	_, ref := runApproval(t, cmdr, false)
	if ref == nil || ref.Code != CodeRequirementUnmet || !strings.Contains(ref.Message, config.CheckNoLiveThreads) {
		t.Fatalf("refusal = %v, want %q naming %q", ref, CodeRequirementUnmet, config.CheckNoLiveThreads)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %v with no thread evidence", posted)
	}
}

// TestRunEmptyGroupObservationNeverSatisfies pins the identity rule: a
// pre-existing pass recorded with no group, or with a group the plan does not
// hold, never satisfies a required check.
func TestRunEmptyGroupObservationNeverSatisfies(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, []config.CheckObservation{
		{Check: config.CheckHeadCurrent, Group: "", Head: reqHeadSHA, At: "2026-11-06T01:00:00Z", Passed: true},
		{Check: config.CheckHeadCurrent, Group: "not-a-group", Head: reqHeadSHA, At: "2026-11-06T01:00:00Z", Passed: true},
	})
	cmdr := requirementCommander(t, nil, 0, false)
	_, ref := runApproval(t, cmdr, false)
	if ref == nil || ref.Code != CodeRequirementUnmet {
		t.Fatalf("refusal = %v, want %q despite the unattributed pass", ref, CodeRequirementUnmet)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %v on an unattributed pass", posted)
	}
}

// TestRunOtherHeadRecordNeverSatisfies pins the head identity: a pass recorded
// for the right group at another head is not evidence for this run.
func TestRunOtherHeadRecordNeverSatisfies(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, []config.CheckObservation{
		{Check: config.CheckNoLiveThreads, Group: reqGroupB, Head: reqMovedSHA, At: "2026-11-06T01:00:00Z", Passed: true},
	})
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 0, true)
	_, ref := runApproval(t, cmdr, false)
	if ref == nil || ref.Code != CodeRequirementUnmet {
		t.Fatalf("refusal = %v, want %q despite the other-head pass", ref, CodeRequirementUnmet)
	}
}

// TestComplianceGateAttributesRenameToNewGroup pins the rename mapping at the
// post gate: a stored finding on the pre-rename path blocks, and the refusal
// names the group the renamed file lives in.
func TestComplianceGateAttributesRenameToNewGroup(t *testing.T) {
	t.Chdir(t.TempDir())
	rs := reqRunState()
	rs.Resolved.Files = []config.FileResolution{{
		Path: "b.txt", OldPath: "old.txt",
		Requirements: rs.Resolved.Files[1].Requirements,
	}}
	rs.Resolved.Plan.Groups[1].Files = []string{"b.txt"}
	st := policyTestState([]any{policyEntry("old.txt", "logic", "critical", "open")}, rs)
	st.flags.Approve, st.flags.Bind = approveTrue, true
	ref := st.complianceGate()
	if ref == nil || ref.Code != CodeRequirementBlocked {
		t.Fatalf("gate = %v, want %q", ref, CodeRequirementBlocked)
	}
	for _, want := range []string{"old.txt", "block-all", reqGroupB, reqProfile} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q does not name %q", ref.Message, want)
		}
	}
	if strings.Contains(ref.Message, reqGroupA) {
		t.Errorf("refusal %q blames the untouched group", ref.Message)
	}
}

// TestRunCommentKeepsUnmetEvidence proves an unmet required check forbids the
// approval without forbidding the comment or losing what the run observed.
func TestRunCommentKeepsUnmetEvidence(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 2, false)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: reqBundle + "/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("comment run refused: %v (%s)", ref, ref.Message)
	}
	if resp.Data.Event != eventComment {
		t.Fatalf("event = %q, want COMMENT", resp.Data.Event)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 1 || posted[0]["event"] != eventComment {
		t.Fatalf("posted = %v, want one COMMENT review", posted)
	}
	assertLedgerVerdicts(t, readLedger(t), []ledgerWant{
		{reqGroupB, config.CheckNoLiveThreads, false},
		{reqGroupA, config.CheckHeadCurrent, true},
	})
}

// TestRunBlockedFindingStillAllowsComment
// proves comments keep blocking findings and record failed compliance.
func TestRunBlockedFindingStillAllowsComment(t *testing.T) {
	t.Chdir(t.TempDir())
	seedRequirementBundle(t, nil)
	review := `{"schema_version":"1","body":"Blocking issue found.","approved":false,"comments":[` +
		`{"path":"a.txt","line":2,"side":"RIGHT","body":"This breaks the invariant.",` +
		`"category":"logic","rigor":"pointed","severity":"critical"}]}`
	if err := writeTestFile(reqBundle+"/review.json", review); err != nil {
		t.Fatal(err)
	}
	live := reqHeadSHA
	cmdr := requirementCommander(t, &live, 0, false)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: reqBundle + "/review.json"}, &warnings, &human)
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("comment run refused: %v (%s)", ref, ref.Message)
	}
	if resp.Data.Event != eventComment {
		t.Fatalf("event = %q, want COMMENT", resp.Data.Event)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 1 || posted[0]["event"] != eventComment {
		t.Fatalf("posted = %v, want one COMMENT review", posted)
	}
	assertLedgerVerdicts(t, readLedger(t), []ledgerWant{
		{reqGroupA, config.CheckNoOpenAboveBlock, false},
		{reqGroupB, config.CheckNoOpenAboveBlock, true},
	})
}
