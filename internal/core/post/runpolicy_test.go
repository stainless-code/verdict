package postcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// policyTestState builds a guard-level state with stored entries and an
// optional run policy: no provider, no merge. The bundle dir follows the
// production resolution under the chdir'd sandbox root.
func policyTestState(entries []any, rs *config.RunState) *state {
	var warnings []string
	st := postTestState(".", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		core.PostFlags{Approve: "false"}, &warnings)
	st.merged.Entries = entries
	st.now = "2026-11-06T00:00:00Z"
	st.dir = filepath.Join(".verdict", "state", "7")
	if rs != nil {
		st.policy = &runPolicy{state: rs}
	}
	return st
}

func policyEntry(path, category, severity, status string) *jq.Obj {
	return jq.NewObj("path", path, "category", category, "severity", severity, "status", status)
}

// policyRunState builds a run bundle with one scoped file, one clause,
// and a require_bind root for the gate tests.
func policyRunState() *config.RunState {
	return &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
		Resolved: &config.Resolved{
			Files: []config.FileResolution{
				{
					Path: "pay/core.go",
					Requirements: config.RequirementSet{
						Clauses: []config.ClauseProvenance{
							{
								Clause: core.BlockClause{
									Name:       "pay-block",
									Threshold:  "important",
									Categories: []string{"security", "logic"},
									Paths:      []string{"pay/**"},
								},
								Sources: []string{"scope payments"},
							},
						},
					},
				},
			},
			Plan: config.Plan{Status: config.PendingStatus, Groups: []config.PlanGroup{
				{
					ID: "payments-group", Label: "deep-review", Profile: "deep-review",
					Checks: []string{config.CheckNoOpenAboveBlock}, Files: []string{"pay/core.go"},
				},
			}},
		},
		Root: config.RequirementSet{RequireBind: true, BindSources: []string{"repo"}},
	}
}

// TestComplianceGateBlocksAndForceProof pins the trusted requirement:
// an open above-floor finding on a clause path refuses, and --force
// cannot bypass it (no forced[] entry, no flag consult).
func TestComplianceGateBlocksAndForceProof(t *testing.T) {
	for _, force := range []bool{false, true} {
		st := policyTestState([]any{
			policyEntry("pay/core.go", "security", "critical", "open"),
		}, policyRunState())
		st.flags.Force = force
		st.flags.Approve, st.flags.Bind = approveTrue, true
		ref := st.complianceGate()
		if ref == nil || ref.Code != CodeRequirementBlocked {
			t.Fatalf("force=%v: gate = %v, want requirement-blocked", force, ref)
		}
		if len(st.forced) != 0 {
			t.Fatalf("force=%v: trusted refusal recorded forced[]: %+v", force, st.forced)
		}
	}
}

// TestComplianceGatePasses pins the negative space: deferred findings,
// out-of-scope paths, wrong categories, below-threshold severities, and
// unknown severities never block.
func TestComplianceGatePasses(t *testing.T) {
	entries := []any{
		policyEntry("pay/core.go", "security", "critical", "deferred"),
		policyEntry("pay/core.go", "security", "critical", "addressed"),
		policyEntry("docs/guide.md", "docs", "critical", "open"),
		policyEntry("pay/core.go", "style", "critical", "open"),
		policyEntry("pay/core.go", "security", "informational", "open"),
		policyEntry("pay/core.go", "security", "mystery", "open"),
	}
	st := policyTestState(entries, policyRunState())
	if ref := st.complianceGate(); ref != nil {
		t.Fatalf("gate = %v, want nil", ref)
	}
}

// TestComplianceGateLegacyDegrade pins the no-policy path: bundles
// without a run bundle skip compliance instead of refusing.
func TestComplianceGateLegacyDegrade(t *testing.T) {
	st := policyTestState([]any{
		policyEntry("pay/core.go", "security", "critical", "open"),
	}, nil)
	if ref := st.complianceGate(); ref != nil {
		t.Fatalf("gate without policy = %v, want nil", ref)
	}
}

// TestComplianceGateObserves pins the outcome append: the blocking run
// records a failed no-open-above-block observation for the group while
// still refusing.
func TestComplianceGateObserves(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bundle := filepath.Join(".verdict", "state", "7")
	if serr := config.WriteRunState(bundle, policyRunState()); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	st := policyTestState([]any{
		policyEntry("pay/core.go", "security", "critical", "open"),
	}, policyRunState())
	st.req.Pr = "7"
	st.flags.Approve, st.flags.Bind = approveTrue, true
	if ref := st.complianceGate(); ref == nil {
		t.Fatal("gate passed, want requirement-blocked")
	}
	assertBlockingObservation(t, bundle, "payments-group", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
}

// assertBlockingObservation checks the stored outcome names the blocked group
// and the reviewed head, so a refusal always leaves its ledger entry.
func assertBlockingObservation(t *testing.T, bundle, group, head string) {
	t.Helper()
	loaded, found, serr := config.ReadRunState(bundle)
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	if len(loaded.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one observation", loaded.Outcomes)
	}
	obs := loaded.Outcomes[0]
	if obs.Check != config.CheckNoOpenAboveBlock || obs.Group != group || obs.Passed {
		t.Fatalf("observation = %+v, want the failed group check", obs)
	}
	if obs.Head != head {
		t.Fatalf("observation head = %q, want the reviewed head", obs.Head)
	}
}

// TestBindRequiredGate pins the binding rule: require_bind with a
// binding approval and no --bind refuses force-proof; --bind, COMMENT
// events, and legacy runs proceed.
func TestBindRequiredGate(t *testing.T) {
	tests := []struct {
		name    string
		approve string
		want    string
		bind    bool
		force   bool
		changes bool
		legacy  bool
	}{
		{name: "approve without bind", approve: "true", want: CodeBindRequired},
		{name: "force cannot bypass", approve: "true", force: true, want: CodeBindRequired},
		{name: "request-changes without bind", approve: "false", changes: true, want: CodeBindRequired},
		{name: "approve with bind", approve: "true", bind: true},
		{name: "comment without bind", approve: "false"},
		{name: "legacy approve without policy", approve: "true", legacy: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := policyRunState()
			if tc.legacy {
				policy = nil
			}
			st := policyTestState(nil, policy)
			st.flags.Approve, st.flags.Bind, st.flags.Force = tc.approve, tc.bind, tc.force
			st.flags.RequestChanges = tc.changes
			assertBindGate(t, st.bindRequiredGate(), tc.want)
		})
	}
}

// assertBindGate checks the bind requirement outcome against the wanted
// refusal code, where an empty code means the gate passes.
func assertBindGate(t *testing.T, ref *core.RefusalError, want string) {
	t.Helper()
	if want == "" {
		if ref != nil {
			t.Fatalf("gate = %v, want nil", ref)
		}
		return
	}
	if ref == nil || ref.Code != want {
		t.Fatalf("gate = %v, want %s", ref, want)
	}
}

// TestFloorPayload pins the payload floor: below-floor findings leave
// the payload while gates keep the full set; unknown severities stay
// visible and the unset floor publishes everything.
func TestFloorPayload(t *testing.T) {
	entries := []any{
		policyEntry("a.go", "logic", "critical", "open"),
		policyEntry("b.go", "logic", "important", "open"),
		policyEntry("c.go", "style", "informational", "open"),
		policyEntry("d.go", "logic", "mystery", "open"),
	}
	rs := policyRunState()
	floor := "important"
	rs.Resolved.Defaults.Values.PostMinSeverity = &floor
	st := policyTestState(entries, rs)
	kept, gotFloor, withheld := st.floorPayload()
	if gotFloor != "important" || len(withheld) != 1 || len(kept) != 3 {
		t.Fatalf("floor = %q excluded = %d kept = %d, want important/1/3", gotFloor, len(withheld), len(kept))
	}
	if n, _, _ := postCounts(kept); n != 3 {
		t.Fatalf("payload open count = %d, want 3", n)
	}
	plain := policyTestState(entries, nil)
	if kept, floor, withheld := plain.floorPayload(); len(kept) != 4 || floor != "" || len(withheld) != 0 {
		t.Fatalf("unset floor filters: kept=%d floor=%q excluded=%d", len(kept), floor, len(withheld))
	}
}

// TestLoadRunPolicyAbsent pins the read-side contract: no run bundle
// reads nil policy without touching the filesystem.
func TestLoadRunPolicyAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := policyTestState(nil, nil)
	if ref := st.loadRunPolicy(); ref != nil {
		t.Fatalf("loadRunPolicy absent: %v", ref)
	}
	if st.policy != nil {
		t.Fatal("absent bundle set a policy")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".verdict")); !os.IsNotExist(err) {
		t.Fatal("read created state")
	}
}

// TestLoadRunPolicyCorrupt pins the loud path: a malformed bundle
// refuses controls-stale instead of degrading silently.
func TestLoadRunPolicyCorrupt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := policyTestState(nil, nil)
	st.req.Pr = "7"
	path := filepath.Join(".verdict", "state", "7", "run.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := st.loadRunPolicy()
	if ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("corrupt bundle: ref = %v, want controls-stale", ref)
	}
}

// policyCommander scripts the approval path for a non-author reviewer:
// the live-SHA read, a foreign authorship read, the empty thread
// recheck, and the reviews POST.
func policyCommander(t *testing.T) *scriptCommander {
	t.Helper()
	cmdr := postCommander(t)
	cmdr.out["gh\x00api\x00user\x00--jq\x00.login"] = scriptResult{stdout: "teammate\n"}
	return cmdr
}

// policyBundleWithPolicy writes the standard post bundle plus a trusted
// run bundle carrying require_bind and no block clauses: approval is
// governed, compliance is vacuous.
func policyBundleWithPolicy(t *testing.T) {
	t.Helper()
	postBundle(t, postReview)
	rs := policyRunState()
	rs.Resolved.Files = nil
	rs.Resolved.Plan.Groups = []config.PlanGroup{{
		ID: "bundle-group", Label: "deep-review", Profile: "deep-review",
		Checks: []string{config.CheckNoOpenAboveBlock},
	}}
	if serr := config.WriteRunState(filepath.Join(".verdict", "state", "7"), rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
}

// TestApproveBindWithPolicyProvesNoCircularPrerequisite pins the
// approval path under typed policy: an approving bind with --force
// posts without any prior post record (no circular prerequisite),
// recording no trusted requirement, and the no-open-above-block
// observation lands.
func TestApproveBindWithPolicyProvesNoCircularPrerequisite(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	policyBundleWithPolicy(t)
	cmdr := policyCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "true", Bind: true, Force: true, ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run approval: %v", ref)
	}
	if resp.Data == nil || resp.Data.Event != "APPROVE" {
		t.Fatalf("event = %+v, want APPROVE", resp.Data)
	}
	assertNoBypassRecorded(t, ".verdict/state/7/post.json")
	assertPassingCompliance(t, filepath.Join(".verdict", "state", "7"))
}

// assertNoBypassRecorded checks the receipt never claims the trusted policy
// clauses were bypassed.
func assertNoBypassRecorded(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	for _, claim := range []string{"bind-required", "requirement-blocked", "spend-capped"} {
		if strings.Contains(string(raw), claim) {
			t.Fatalf("trusted requirement recorded as bypassed (%s):\n%s", claim, raw)
		}
	}
}

// assertPassingCompliance checks the stored ledger carries the passing
// no-open-above-block observation for the approved run.
func assertPassingCompliance(t *testing.T, bundle string) {
	t.Helper()
	loaded, found, serr := config.ReadRunState(bundle)
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	for _, obs := range loaded.Outcomes {
		if obs.Check == config.CheckNoOpenAboveBlock && obs.Passed {
			return
		}
	}
	t.Fatalf("outcomes = %+v, want the passing compliance observation", loaded.Outcomes)
}

// TestApproveWithoutBindRefusesForceProof pins the force boundary: the
// same approval without --bind refuses bind-required even under
// --force, posting nothing.
func TestApproveWithoutBindRefusesForceProof(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	policyBundleWithPolicy(t)
	cmdr := policyCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "true", Force: true, ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != CodeBindRequired {
		t.Fatalf("approval without bind under force = %v, want bind-required", ref)
	}
	if _, err := os.Stat(".verdict/state/7/post.json"); !os.IsNotExist(err) {
		t.Fatal("refused approval wrote post.json")
	}
}

// TestPostFloorFollowsEachFindingsOwnScope pins the real payload seam: a
// scope's stricter post floor withholds its own important finding while the
// same important finding on an ordinary path publishes, and the compliance
// gate still blocks on the full stored set.
func TestPostFloorFollowsEachFindingsOwnScope(t *testing.T) {
	important, critical := "important", "critical"
	rs := &config.RunState{Resolved: &config.Resolved{
		Defaults: config.ResolvedDefaults{
			Values: config.EffectiveDefaults{PostMinSeverity: &important},
		},
		Files: []config.FileResolution{{
			Path: "vendor/lib.go",
			Requirements: config.RequirementSet{Clauses: []config.ClauseProvenance{{
				Clause: core.BlockClause{
					Name: "vendor-block", Threshold: "important",
					Categories: []string{"logic"}, Paths: []string{"vendor/**"},
				},
				Sources: []string{"scope vendored"},
			}}},
			Defaults: config.ResolvedDefaults{
				Values: config.EffectiveDefaults{PostMinSeverity: &critical},
			},
		}},
	}}
	st := policyTestState([]any{
		policyEntry("vendor/lib.go", "logic", "important", "open"),
		policyEntry("app/main.go", "logic", "important", "open"),
	}, rs)

	kept, floor, withheld := st.floorPayload()
	assertScopedFloorPayload(t, kept, floor, withheld)

	blocked := config.CheckCompliance(rs.Resolved, nil, policyFindings(st.merged.Entries))
	if len(blocked) != 1 {
		t.Fatalf("compliance saw %d findings; the gate must read the full set, not the payload", len(blocked))
	}
}

// assertScopedFloorPayload checks the filtered payload, its scoped receipt,
// and the global floor label.
func assertScopedFloorPayload(t *testing.T, kept []any, floor string, withheld []core.FloorScope) {
	t.Helper()
	if len(kept) != 1 || len(withheld) != 1 {
		t.Fatalf("payload = %d entries (%d excluded); want only the ordinary path's", len(kept), len(withheld))
	}
	if scoped := scopedFloors(withheld); len(scoped) != 1 || scoped[0] != (core.FloorScope{Path: "vendor/lib.go", Floor: "critical"}) {
		t.Fatalf("scoped receipt = %v; want vendor/lib.go at critical", scoped)
	}
	entry, ok := kept[0].(*jq.Obj)
	if !ok {
		t.Fatalf("kept payload is not an object: %T", kept[0])
	}
	path, _ := entry.Get("path")
	if jq.StrOf(path) != "app/main.go" {
		t.Fatalf("payload kept %v; want app/main.go from the ordinary path", path)
	}
	if floor != "important" {
		t.Fatalf("floor label = %q; the global default is important", floor)
	}
}

// TestPostReceiptRecordsScopedOnlyExclusion pins the receipt truth: with no
// global floor at all, a scope's critical floor still withholds its important
// finding, so the receipt states one exclusion and the path/floor that caused
// it instead of implying nothing was filtered.
func TestPostReceiptRecordsScopedOnlyExclusion(t *testing.T) {
	t.Chdir(t.TempDir())
	critical := "critical"
	rs := &config.RunState{Resolved: &config.Resolved{
		Files: []config.FileResolution{{
			Path: "vendor/lib.go",
			Defaults: config.ResolvedDefaults{
				Values: config.EffectiveDefaults{PostMinSeverity: &critical},
			},
		}},
	}}
	st := policyTestState([]any{
		policyEntry("vendor/lib.go", "logic", "important", "open"),
		policyEntry("app/main.go", "logic", "important", "open"),
	}, rs)
	writeReceipt(t, st)
	raw, err := os.ReadFile(filepath.Join(st.dir, "post.json"))
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var receipt core.Post
	if uerr := json.Unmarshal(raw, &receipt); uerr != nil {
		t.Fatalf("post.json is not a receipt: %v", uerr)
	}
	if receipt.BelowFloorExcluded != 1 {
		t.Fatalf("below_floor_excluded = %d; the scoped floor withheld one finding", receipt.BelowFloorExcluded)
	}
	if len(receipt.PostFloorScoped) != 1 ||
		receipt.PostFloorScoped[0] != (core.FloorScope{Path: "vendor/lib.go", Floor: "critical"}) {
		t.Fatalf("post_floor_scoped = %v; want vendor/lib.go at critical", receipt.PostFloorScoped)
	}
	if receipt.PostMinSeverity != nil {
		t.Fatalf("post_min_severity = %q; the run set no global floor", *receipt.PostMinSeverity)
	}
}

// writeReceipt stages the receipt directory and writes post.json for a state.
func writeReceipt(t *testing.T, st *state) {
	t.Helper()
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		t.Fatalf("stage bundle dir: %v", err)
	}
	if err := writePostReceipt(st, jq.NewObj("id", 1)); err != nil {
		t.Fatalf("writePostReceipt: %v", err)
	}
}

// TestPostReceiptStaysBareWithoutExclusions pins the unchanged receipt: a run
// that filters nothing gains no floor keys at all.
func TestPostReceiptStaysBareWithoutExclusions(t *testing.T) {
	t.Chdir(t.TempDir())
	st := policyTestState([]any{policyEntry("app/main.go", "logic", "important", "open")}, nil)
	writeReceipt(t, st)
	raw, err := os.ReadFile(filepath.Join(st.dir, "post.json"))
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	if strings.Contains(string(raw), "post_floor_scoped") || strings.Contains(string(raw), "below_floor_excluded") {
		t.Fatalf("receipt carries floor keys for an unfiltered run:\n%s", raw)
	}
}

// TestComplianceGateKeepsNonApprovalEvidence
// keeps failed observations for comments and negative verdicts.
func TestComplianceGateKeepsNonApprovalEvidence(t *testing.T) {
	for _, flags := range []core.PostFlags{
		{Approve: approveFalse},
		{Approve: approveFalse, Bind: true, RequestChanges: true},
		{Approve: approveTrue},
		{Approve: approveTrue, Bind: true, RequestChanges: true},
	} {
		st := policyTestState([]any{policyEntry("pay/core.go", "security", "critical", "open")}, policyRunState())
		st.flags = flags
		st.flags.DryRun = true
		if ref := st.complianceGate(); ref != nil {
			t.Fatalf("flags=%+v: gate = %v, want no approval refusal", flags, ref)
		}
		assertFailedCompliance(t, st.policy.state)
	}
}

func assertFailedCompliance(t *testing.T, rs *config.RunState) {
	t.Helper()
	if len(rs.Outcomes) != 1 || rs.Outcomes[0].Passed || rs.Outcomes[0].Check != config.CheckNoOpenAboveBlock {
		t.Fatalf("outcomes = %+v, want a failed no-open observation", rs.Outcomes)
	}
}
