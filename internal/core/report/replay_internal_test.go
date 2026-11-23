package reportcore

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
)

// TestCandidateOutcomesRekeysByFileSet pins the re-key: the same files under a
// different group digest keep their recorded observation, a file set the
// candidate groups differently keeps the captured id, and the root scope is
// never rewritten.
func TestCandidateOutcomesRekeysByFileSet(t *testing.T) {
	captured := &config.Resolved{Plan: config.Plan{Groups: []config.PlanGroup{
		{ID: "captured-a", Files: []string{"a.go"}},
		{ID: "captured-ab", Files: []string{"b.go", "a.go"}},
	}}}
	candidate := &config.Resolved{Plan: config.Plan{Groups: []config.PlanGroup{
		{ID: "candidate-a", Files: []string{"a.go"}},
		{ID: "candidate-b", Files: []string{"b.go"}},
	}}}
	outcomes := []config.CheckObservation{
		{Group: "captured-a", Check: "head-current", Head: "aaaa", Passed: true},
		{Group: "captured-ab", Check: "head-current", Head: "aaaa", Passed: true},
		{Group: config.RootScopeID, Check: "head-current", Head: "aaaa", Passed: true},
	}
	got := candidateOutcomes(captured, candidate, outcomes)
	groups := make([]string, len(got))
	for i := range got {
		groups[i] = got[i].Group
	}
	want := []string{"candidate-a", "captured-ab", config.RootScopeID}
	if !slices.Equal(groups, want) {
		t.Fatalf("groups = %v; want %v", groups, want)
	}
	if outcomes[0].Group != "captured-a" {
		t.Fatalf("input observation was rewritten: %q", outcomes[0].Group)
	}
}

// assertCandidateCoverage replays one candidate minimum and checks the flag,
// the untouched sufficiency verdict, and the untouched shared receipt.
func assertCandidateCoverage(t *testing.T, receipt *core.EvidenceReceipt, percent, want int, met bool) {
	t.Helper()
	resolved := &config.Resolved{Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{SpecCoverageMin: percent}}}
	got := candidateCoverage(approval.Evidence{Receipt: receipt}, resolved)
	if got.Receipt.Spec.Min != want || got.Receipt.Spec.Met != met {
		t.Fatalf("spec = {min:%d met:%t}; want {min:%d met:%t}", got.Receipt.Spec.Min, got.Receipt.Spec.Met, want, met)
	}
	if got.Receipt.Insufficient {
		t.Fatal("candidate coverage re-decided sufficiency")
	}
	if receipt.Spec.Min != 80 || !receipt.Spec.Met {
		t.Fatalf("shared receipt was rewritten: %+v", receipt.Spec)
	}
}

// TestCandidateCoverageReDecidesOnlyTheFlag pins the narrow half of the
// coverage finding: a candidate minimum re-decides the descriptive min/met
// pair, never the captured sufficiency verdict, and never the shared receipt.
func TestCandidateCoverageReDecidesOnlyTheFlag(t *testing.T) {
	receipt := &core.EvidenceReceipt{
		Spec:         core.EvidenceSpec{Total: 10, Covered: 8, Min: 80, Met: true},
		Insufficient: false,
	}
	for _, tc := range []struct {
		name string
		min  int
		want int
		met  bool
	}{
		{name: "tightened", min: 100, want: 100, met: false},
		{name: "loosened", min: 50, want: 50, met: true},
		{name: "unset falls back to the built-in minimum", min: 0, want: 80, met: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertCandidateCoverage(t, receipt, tc.min, tc.want, tc.met)
		})
	}
	if got := candidateCoverage(approval.Evidence{Missing: "none"}, &config.Resolved{}); got.Missing != "none" || got.Receipt != nil {
		t.Fatalf("absent receipt was rewritten: %+v", got)
	}
}
