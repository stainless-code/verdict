package reportcore

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
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
