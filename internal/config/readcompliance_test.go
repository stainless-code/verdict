package config

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// readComplianceFixture builds a captured run with one critical security
// clause and the head-current required check.
func readComplianceFixture(t *testing.T, outcomes []CheckObservation) (*RunState, string) {
	t.Helper()
	repo := &core.Config{
		SchemaVersion: "1",
		Requirements: &core.ConfigRequirements{
			BlockClauses: []core.BlockClause{{
				Name: "security-critical", Threshold: "critical", Categories: []string{"security"},
			}},
			RequireChecks: []string{"head-current"},
		},
	}
	resolved, ref := Resolve(&Inputs{Repo: repo, Files: []FileInput{{Path: "notes.txt"}}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
		Root:     RootSet(repo, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		Outcomes: outcomes,
	}
	rs.RequiresEvidence, rs.InputsSHA256 = true, "feedface"
	return rs, resolved.Plan.Groups[0].ID
}

// TestEvaluateReadComplianceBlocksAndPends proves the read evaluation reports
// an open critical finding as blocking and a required check with no matching
// observation as pending, never satisfied.
func TestEvaluateReadComplianceBlocksAndPends(t *testing.T) {
	rs, _ := readComplianceFixture(t, nil)
	got := EvaluateReadCompliance(rs, "aaaaaaa", []FindingView{{
		Path: "notes.txt", Category: "security", Severity: "critical", Status: "open",
	}})
	if !got.HasPolicy {
		t.Fatalf("read compliance = %+v, want a captured policy", got)
	}
	if len(got.Blockers) != 1 || got.Blockers[0].Clause != "security-critical" {
		t.Fatalf("blockers = %+v, want the security-critical clause", got.Blockers)
	}
	if len(got.Pending) != 1 || got.Pending[0].Check != "head-current" {
		t.Fatalf("pending = %+v, want head-current", got.Pending)
	}
	if got.Satisfied() {
		t.Fatal("satisfied with a blocker and a pending check")
	}
}

// TestEvaluateReadComplianceTiesPendingToSavedHead proves standing is bound to
// the saved head: an observation for another head leaves the check pending.
func TestEvaluateReadComplianceTiesPendingToSavedHead(t *testing.T) {
	_, group := readComplianceFixture(t, nil)
	obs := CheckObservation{Check: "head-current", Group: group, Head: "otherhea", Passed: true}
	rs, _ := readComplianceFixture(t, []CheckObservation{obs})
	got := EvaluateReadCompliance(rs, "aaaaaaa", nil)
	if len(got.Pending) != 1 {
		t.Fatalf("pending = %+v, want the observation with another head ignored", got.Pending)
	}
	if got := EvaluateReadCompliance(rs, "otherhea", nil); !got.Satisfied() {
		t.Fatal("matching-head observation did not satisfy the check")
	}
}

// TestEvaluateReadComplianceUnknownWithoutVerifiedCapture proves an
// unverified capture never reads satisfied even with no blockers or pending.
func TestEvaluateReadComplianceUnknownWithoutVerifiedCapture(t *testing.T) {
	rs, _ := readComplianceFixture(t, nil)
	rs.InputsSHA256 = ""
	got := EvaluateReadCompliance(rs, "aaaaaaa", nil)
	if !got.Unverified || got.Satisfied() {
		t.Fatalf("unverified capture = %+v, want unknown and unsatisfied", got)
	}
}

// TestEvaluateReadComplianceAbsentPolicyInventsNothing proves a legacy run
// with no captured requirements reports no policy and stays unsatisfied.
func TestEvaluateReadComplianceAbsentPolicyInventsNothing(t *testing.T) {
	resolved, ref := Resolve(&Inputs{Files: []FileInput{{Path: "notes.txt"}}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{BaseSHA: "b", Trusted: true, Resolved: resolved}
	got := EvaluateReadCompliance(rs, "aaaaaaa", nil)
	if got.HasPolicy || got.Satisfied() {
		t.Fatalf("legacy run = %+v, want no invented policy", got)
	}
}

// TestEvaluateReadComplianceCleanNoOpenPassesWithoutObservation proves the
// no-open-above-block check is decided ephemerally from the full findings: a
// clean capture satisfies it with no prior post or appended outcome.
func TestEvaluateReadComplianceCleanNoOpenPassesWithoutObservation(t *testing.T) {
	repo := &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses:  []core.BlockClause{{Name: "all", Threshold: "critical", Paths: []string{"notes.txt"}}},
		RequireChecks: []string{CheckNoOpenAboveBlock},
	}}
	resolved, ref := Resolve(&Inputs{Repo: repo, Files: []FileInput{{Path: "notes.txt"}}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{
		BaseSHA: "b", Trusted: true, Resolved: resolved,
		Root: RootSet(repo, "b"), RequiresEvidence: true, InputsSHA256: "feedface",
	}
	got := EvaluateReadCompliance(rs, "aaaaaaa", nil)
	if len(got.Pending) != 0 {
		t.Fatalf("pending = %+v, want the clean no-open check decided, not pending", got.Pending)
	}
	if !got.Satisfied() {
		t.Fatalf("read compliance = %+v, want satisfied without an observation", got)
	}
}

// TestEvaluateReadComplianceConflictNeverSatisfied proves a withheld
// required path is conflicted and unsatisfied even with no findings.
func TestEvaluateReadComplianceConflictNeverSatisfied(t *testing.T) {
	repo := &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses: []core.BlockClause{{Name: "secret", Threshold: "critical", Paths: []string{"secret/**"}}},
	}}
	personal := &PersonalSettings{Privacy: PersonalPrivacy{Exclusions: []string{"secret/**"}}}
	resolved, ref := Resolve(&Inputs{
		Repo: repo, Personal: personal,
		Files: []FileInput{{Path: "secret/keys.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{
		BaseSHA: "b", Trusted: true, Resolved: resolved,
		Root: RootSet(repo, "b"), RequiresEvidence: true, InputsSHA256: "feedface",
	}
	got := EvaluateReadCompliance(rs, "aaaaaaa", nil)
	if len(got.Conflicts) != 1 || got.Satisfied() {
		t.Fatalf("read compliance = %+v, want one conflict and unsatisfied with no findings", got)
	}
}
