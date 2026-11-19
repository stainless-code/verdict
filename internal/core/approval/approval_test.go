package approval

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestNoConfigDefaultThreshold pins the operator decision: with no policy,
// important and critical findings withhold approval and informational ones do
// not. Each row is a literal severity, never a value derived from the code
// under test.
func TestNoConfigDefaultThreshold(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		approved bool
	}{
		{"critical withholds", "critical", false},
		{"important withholds", "important", false},
		{"informational approves", "informational", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkDefaultThreshold(t, tc.severity, tc.approved) })
	}
}

// checkDefaultThreshold proves one severity's default decision and the named
// tier that decided it.
func checkDefaultThreshold(t *testing.T, severity string, approved bool) {
	t.Helper()
	got := Evaluate([]any{openFinding("sig-1", severity, "unchecked")}, "", "", nil, false, nil)
	if got.Approved != approved {
		t.Fatalf("approved = %v, want %v (receipt %+v)", got.Approved, approved, got)
	}
	if got.Tier != DefaultApprovalTier {
		t.Fatalf("tier = %q, want the named default %q", got.Tier, DefaultApprovalTier)
	}
}

// openFinding builds one stored open finding with the given sig, severity,
// and quote standing.
func openFinding(sig, severity, quote string) *jq.Obj {
	return jq.NewObj("sig", sig, "status", "open", "severity", severity, "path", "notes.txt",
		"line", 2, "verification", jq.NewObj("quote", quote))
}

func TestUnverifiedDoesNotWithhold(t *testing.T) {
	for _, sev := range []string{"important", "critical"} {
		got := Evaluate([]any{openFinding("sig-unv", sev, "unverified")}, "", "", nil, false, nil)
		if !got.Approved || got.Insufficient || len(got.GatingSigs) != 0 {
			t.Fatalf("unverified %s finding gated: %+v", sev, got)
		}
	}
}

func TestEligibleFindingsDropsUnverified(t *testing.T) {
	entries := []any{
		openFinding("sig-ver", "important", "verified"),
		openFinding("sig-unv", "critical", "unverified"),
		openFinding("sig-leg", "informational", "unchecked"),
	}
	got := EligibleFindings(entries)
	if len(got) != 2 {
		t.Fatalf("len(EligibleFindings) = %d, want 2", len(got))
	}
	wantSigs := []string{"sig-ver", "sig-leg"}
	gotSigs := make([]string, len(got))
	for i, item := range got {
		obj, ok := item.(*jq.Obj)
		if !ok {
			t.Fatalf("entry %d has type %T, want *jq.Obj", i, item)
		}
		v, _ := obj.Get("sig")
		gotSigs[i] = jq.R(v)
	}
	if !slices.Equal(gotSigs, wantSigs) {
		t.Fatalf("EligibleFindings sigs = %v, want %v", gotSigs, wantSigs)
	}
}

func TestUnverifiedBlockClauseNeverGatesAndIsInsufficient(t *testing.T) {
	entries := []any{openFinding("sig-unv", "informational", "unverified")}
	comp := &config.ReadCompliance{
		HasPolicy: true,
		Blockers: []config.BlockingFinding{{
			Clause:  "security-block",
			Finding: config.FindingView{Sig: "sig-unv", Path: "notes.txt", Severity: "informational", Status: "open"},
		}},
	}
	got := Evaluate(entries, "", "", comp, false, nil)
	if got.Approved {
		t.Fatalf("dropping unverified block-clause finding manufactured green: %+v", got)
	}
	if !got.Insufficient {
		t.Fatalf("got.Insufficient = false, want true (%+v)", got)
	}
	if len(got.GatingSigs) != 0 {
		t.Fatalf("got.GatingSigs = %v, want empty (unverified sig must never gate)", got.GatingSigs)
	}
	if len(got.Clauses) != 0 {
		t.Fatalf("got.Clauses = %v, want empty", got.Clauses)
	}
	const wantReason = "review-insufficient: unverified finding named by block clause"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}
}

func TestVerifiedTierFindingGates(t *testing.T) {
	got := Evaluate([]any{openFinding("sig-ver", "important", "verified")}, "", "", nil, false, nil)
	if got.Approved || got.Insufficient {
		t.Fatalf("verified important finding = %+v, want Approved=false Insufficient=false", got)
	}
	if !slices.Equal(got.GatingSigs, []string{"sig-ver"}) {
		t.Fatalf("got.GatingSigs = %v, want [sig-ver]", got.GatingSigs)
	}
	if got.Reason != "open findings at-or-above important" {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, "open findings at-or-above important")
	}
}

func TestVerifiedBlockClauseGatesDropsUnverifiedSibling(t *testing.T) {
	entries := []any{
		openFinding("sig-ver", "informational", "verified"),
		openFinding("sig-unv", "informational", "unverified"),
	}
	comp := &config.ReadCompliance{
		HasPolicy: true,
		Blockers: []config.BlockingFinding{
			{Clause: "auth-sec", Finding: config.FindingView{Sig: "sig-ver", Status: "open"}},
			{Clause: "unv-clause", Finding: config.FindingView{Sig: "sig-unv", Status: "open"}},
		},
	}
	got := Evaluate(entries, "", "", comp, false, nil)
	if got.Approved || got.Insufficient {
		t.Fatalf("mixed block clauses = %+v, want Approved=false Insufficient=false", got)
	}
	if !slices.Equal(got.GatingSigs, []string{"sig-ver"}) {
		t.Fatalf("got.GatingSigs = %v, want [sig-ver]", got.GatingSigs)
	}
	if !slices.Equal(got.Clauses, []string{"auth-sec"}) {
		t.Fatalf("got.Clauses = %v, want [auth-sec]", got.Clauses)
	}
}

func TestInsufficientEvidenceWithholdsWithReason(t *testing.T) {
	ev := &core.EvidenceReceipt{
		Head:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DiffSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Gaps:         []string{"hunk notes.txt\tNEW\t1\t3 has no matched receipt", "second gap"},
		Insufficient: true,
	}
	got := Evaluate([]any{openFinding("sig-unv", "critical", "unverified")}, "", "", nil, false, ev)
	if got.Approved {
		t.Fatalf("insufficient evidence approved: %+v", got)
	}
	if !got.Insufficient {
		t.Fatalf("got.Insufficient = false, want true")
	}
	if len(got.GatingSigs) != 0 {
		t.Fatalf("got.GatingSigs = %v, want empty (unverified sig must not pollute GatingSigs)", got.GatingSigs)
	}
	const wantReason = "review-insufficient: hunk notes.txt\tNEW\t1\t3 has no matched receipt"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}

	okEv := &core.EvidenceReceipt{Insufficient: false}
	clean := Evaluate(nil, "", "", nil, false, okEv)
	if !clean.Approved || clean.Insufficient || clean.Reason != "no gating findings" {
		t.Fatalf("sufficient clean review = %+v, want Approved=true Insufficient=false", clean)
	}
}

func TestViolatedSpecCheckWithholds(t *testing.T) {
	if got := Evaluate(nil, "", "", nil, true, nil); got.Approved {
		t.Fatalf("violated spec check approved: %+v", got)
	}
}
