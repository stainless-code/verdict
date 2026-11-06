package postcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// policyTestState builds a guard-level state with stored entries and an
// optional run policy: no bundle, no provider, no merge.
func policyTestState(entries []any, rs *config.RunState) *state {
	var warnings []string
	st := postTestState(".", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		core.PostFlags{Approve: "false"}, &warnings)
	st.merged.Entries = entries
	st.now = "2026-11-06T00:00:00Z"
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
				{Profile: "deep-review", Checks: []string{config.CheckNoOpenAboveBlock}, Files: []string{"pay/core.go"}},
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
	if serr := config.WriteRunState(".", "7", policyRunState()); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	st := policyTestState([]any{
		policyEntry("pay/core.go", "security", "critical", "open"),
	}, policyRunState())
	st.req.Pr = "7"
	if ref := st.complianceGate(); ref == nil {
		t.Fatal("gate passed, want requirement-blocked")
	}
	loaded, found, serr := config.ReadRunState(".", "7")
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	if len(loaded.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one observation", loaded.Outcomes)
	}
	obs := loaded.Outcomes[0]
	if obs.Check != config.CheckNoOpenAboveBlock || obs.Group != "deep-review" || obs.Passed {
		t.Fatalf("observation = %+v, want the failed group check", obs)
	}
}

// TestBindRequiredGate pins the binding rule: require_bind with a
// binding approval and no --bind refuses force-proof; --bind, COMMENT
// events, and legacy runs proceed.
func TestBindRequiredGate(t *testing.T) {
	newBindState := func(approve string, bind, force bool) *state {
		st := policyTestState(nil, policyRunState())
		st.flags.Approve = approve
		st.flags.Bind = bind
		st.flags.Force = force
		return st
	}
	if ref := newBindState("true", false, false).bindRequiredGate(); ref == nil || ref.Code != CodeBindRequired {
		t.Fatalf("approve without bind: gate = %v, want bind-required", ref)
	}
	if ref := newBindState("true", false, true).bindRequiredGate(); ref == nil || ref.Code != CodeBindRequired {
		t.Fatalf("force cannot bypass bind-required: gate = %v", ref)
	}
	stChange := newBindState("false", false, false)
	stChange.flags.RequestChanges = true
	if ref := stChange.bindRequiredGate(); ref == nil || ref.Code != CodeBindRequired {
		t.Fatalf("request-changes without bind: gate = %v, want bind-required", ref)
	}
	if ref := newBindState("true", true, false).bindRequiredGate(); ref != nil {
		t.Fatalf("approve with bind: gate = %v, want nil", ref)
	}
	if ref := newBindState("false", false, false).bindRequiredGate(); ref != nil {
		t.Fatalf("comment without bind: gate = %v, want nil", ref)
	}
	legacy := policyTestState(nil, nil)
	legacy.flags.Approve = "true"
	if ref := legacy.bindRequiredGate(); ref != nil {
		t.Fatalf("legacy approve without bind: gate = %v, want nil (downgrade path)", ref)
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
	kept, gotFloor, excluded := st.floorPayload()
	if gotFloor != "important" || excluded != 1 || len(kept) != 3 {
		t.Fatalf("floor = %q excluded = %d kept = %d, want important/1/3", gotFloor, excluded, len(kept))
	}
	if n, _, _ := postCounts(kept); n != 3 {
		t.Fatalf("payload open count = %d, want 3", n)
	}
	plain := policyTestState(entries, nil)
	if kept, floor, excluded := plain.floorPayload(); len(kept) != 4 || floor != "" || excluded != 0 {
		t.Fatalf("unset floor filters: kept=%d floor=%q excluded=%d", len(kept), floor, excluded)
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
// refuses bad-config instead of degrading silently.
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
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("corrupt bundle: ref = %v, want bad-config", ref)
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
	rs.Resolved.Plan.Groups = []config.PlanGroup{
		{Profile: "deep-review", Checks: []string{config.CheckNoOpenAboveBlock}},
	}
	if serr := config.WriteRunState(".", "7", rs); serr != nil {
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
	req := postTestReq(cmdr, core.PostFlags{Approve: "true", Bind: true, Force: true, ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run approval: %v", ref)
	}
	if resp.Data == nil || resp.Data.Event != "APPROVE" {
		t.Fatalf("event = %+v, want APPROVE", resp.Data)
	}
	raw, err := os.ReadFile(".review/7/post.json")
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	if strings.Contains(string(raw), "bind-required") || strings.Contains(string(raw), "requirement-blocked") || strings.Contains(string(raw), "spend-capped") {
		t.Fatalf("trusted requirement recorded as bypassed:\n%s", raw)
	}
	loaded, found, serr := config.ReadRunState(".", "7")
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	seen := false
	for _, obs := range loaded.Outcomes {
		if obs.Check == config.CheckNoOpenAboveBlock && obs.Passed {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("outcomes = %+v, want the passing compliance observation", loaded.Outcomes)
	}
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
	req := postTestReq(cmdr, core.PostFlags{Approve: "true", Force: true, ReviewFile: ".review/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != CodeBindRequired {
		t.Fatalf("approval without bind under force = %v, want bind-required", ref)
	}
	if _, err := os.Stat(".review/7/post.json"); !os.IsNotExist(err) {
		t.Fatal("refused approval wrote post.json")
	}
}
