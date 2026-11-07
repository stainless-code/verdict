package config

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// privacyFixture resolves one file under an optional clause/deep profile with
// the given personal exclusions.
func privacyFixture(t *testing.T, files []FileInput, exclusions []string, reqs *core.ConfigRequirements) *RunState {
	t.Helper()
	repo := &core.Config{SchemaVersion: "1", Requirements: reqs}
	personal := &PersonalSettings{Privacy: PersonalPrivacy{Exclusions: exclusions}}
	resolved, ref := Resolve(&Inputs{Repo: repo, Personal: personal, Files: files})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return &RunState{BaseSHA: "bbbb", Trusted: true, Resolved: resolved}
}

// TestPrivacyConflictsReportsClauseRequiredWithheldPath proves an actual
// reviewed path withheld by an exclusion conflicts when a block clause
// requires that path, and the conflict names path and rule without content.
func TestPrivacyConflictsReportsClauseRequiredWithheldPath(t *testing.T) {
	rs := privacyFixture(t,
		[]FileInput{{Path: "secret/keys.go"}},
		[]string{"secret/**"},
		&core.ConfigRequirements{BlockClauses: []core.BlockClause{{
			Name: "secret-review", Threshold: "critical", Paths: []string{"secret/**"},
		}}})
	got := PrivacyConflicts(rs)
	if len(got) != 1 {
		t.Fatalf("conflicts = %+v, want one clause conflict", got)
	}
	if got[0].Path != "secret/keys.go" || got[0].Rule != "secret/**" || got[0].Deep {
		t.Fatalf("conflict = %+v, want path and matching rule only", got[0])
	}
}

// TestPrivacyConflictsIgnoresNonContentChecks proves a head/thread-only
// requirement without a content clause or deep group never conflicts.
func TestPrivacyConflictsIgnoresNonContentChecks(t *testing.T) {
	rs := privacyFixture(t,
		[]FileInput{{Path: "secret/keys.go"}},
		[]string{"secret/**"},
		&core.ConfigRequirements{RequireChecks: []string{"head-current", "no-live-threads"}})
	if got := PrivacyConflicts(rs); len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none for non-content checks", got)
	}
}

// TestPrivacyConflictsBindsRenamedPaths proves the old path is checked too:
// an exclusion on the pre-rename path conflicts for the clause glob.
func TestPrivacyConflictsBindsRenamedPaths(t *testing.T) {
	rs := privacyFixture(t,
		[]FileInput{{Path: "public/keys.go", OldPath: "secret/keys.go"}},
		[]string{"secret/**"},
		&core.ConfigRequirements{BlockClauses: []core.BlockClause{{
			Name: "secret-review", Threshold: "critical", Paths: []string{"secret/**"},
		}}})
	got := PrivacyConflicts(rs)
	if len(got) != 1 || got[0].Path != "secret/keys.go" {
		t.Fatalf("conflicts = %+v, want the withheld old path", got)
	}
}

// TestPrivacyConflictsDeepGroupWithheldPath proves a deep group's reviewed
// path conflicts even without a content clause.
func TestPrivacyConflictsDeepGroupWithheldPath(t *testing.T) {
	deep := "deep"
	repo := &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"deep": {Depth: &deep}},
		Scopes: []core.ConfigScope{{
			Name: "deep-scope", Include: []string{"secret/**"}, Profile: &deep,
		}},
	}
	personal := &PersonalSettings{Privacy: PersonalPrivacy{Exclusions: []string{"secret/**"}}}
	resolved, ref := Resolve(&Inputs{
		Repo: repo, Personal: personal,
		Files: []FileInput{{Path: "secret/keys.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{BaseSHA: "bbbb", Trusted: true, Resolved: resolved}
	got := PrivacyConflicts(rs)
	if len(got) != 1 || !got[0].Deep || got[0].Group == "" {
		t.Fatalf("conflicts = %+v, want one deep-group conflict", got)
	}
}

// TestPrivacyConflictsEmptyWithoutExclusions proves no exclusion means no
// conflict regardless of requirements.
func TestPrivacyConflictsEmptyWithoutExclusions(t *testing.T) {
	rs := privacyFixture(t,
		[]FileInput{{Path: "secret/keys.go"}}, nil,
		&core.ConfigRequirements{BlockClauses: []core.BlockClause{{
			Name: "all", Threshold: "critical",
		}}})
	if got := PrivacyConflicts(rs); len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none without exclusions", got)
	}
}
