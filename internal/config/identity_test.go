package config

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestGroupIdentityIsUnambiguous pins the identity encoding: sorted path
// membership and normalized work hash into the full SHA-256, and no
// delimiter in a path, category, or label can alias two different groups.
func TestGroupIdentityIsUnambiguous(t *testing.T) {
	grouped := groupIdentity("work", []string{"a,b", "c"})
	split := groupIdentity("work", []string{"a", "b,c"})
	if grouped == split {
		t.Fatalf("path sets [a,b c] and [a b,c] alias on %q", grouped)
	}
	if got := groupIdentity("work", []string{"c", "a,b"}); got != grouped {
		t.Fatalf("path order changed the identity: %q != %q", got, grouped)
	}
	if got := groupIdentity("other", []string{"a,b", "c"}); got == grouped {
		t.Fatal("different work aliases the same identity")
	}
	if got := groupIdentity("a", []string{"b"}); got == groupIdentity("b", []string{"a"}) {
		t.Fatal("swapping work and path bytes aliases the same identity")
	}
	if len(grouped) != 64 {
		t.Fatalf("identity = %q, want the full 64-hex SHA-256", grouped)
	}
}

// TestGroupKeyKeepsMasksDistinct pins the work encoding: lists are counted
// and every item is length-prefixed, path and category masks stay separate
// fields, and a comma inside one mask never reads as two masks.
func TestGroupKeyKeepsMasksDistinct(t *testing.T) {
	key := func(clause core.BlockClause) string {
		file := &FileResolution{Requirements: RequirementSet{
			Clauses: []ClauseProvenance{{Clause: clause, Sources: []string{"repo"}}},
		}}
		return groupKey(file, BuiltinDepth, nil, nil)
	}
	oneCategory := key(core.BlockClause{Name: "c", Threshold: "critical", Categories: []string{"a,b"}})
	twoCategories := key(core.BlockClause{Name: "c", Threshold: "critical", Categories: []string{"a", "b"}})
	if oneCategory == twoCategories {
		t.Fatal("one comma-carrying category aliases two categories")
	}
	samePath := key(core.BlockClause{Name: "c", Threshold: "critical", Paths: []string{"a,b"}})
	if oneCategory == samePath {
		t.Fatal("a category mask aliases a path mask of the same name")
	}
	renamed := key(core.BlockClause{Name: "other", Threshold: "critical", Categories: []string{"a,b"}})
	if oneCategory == renamed {
		t.Fatal("the clause name left the identity")
	}
	threshold := key(core.BlockClause{Name: "c", Threshold: "important", Categories: []string{"a,b"}})
	if oneCategory == threshold {
		t.Fatal("the clause threshold left the identity")
	}
}

// TestCheckScopesCoverRootRequirements pins the whole-run scope: a root
// check no group selects is its own scope, so an empty plan never leaves it
// vacuously satisfied, while a group-covered check adds no second scope.
func TestCheckScopesCoverRootRequirements(t *testing.T) {
	plan := Plan{Groups: []PlanGroup{{ID: "g1", Label: "one", Checks: []string{CheckNoOpenAboveBlock}}}}
	root := RequirementSet{Checks: []string{CheckNoOpenAboveBlock, CheckNoLiveThreads}}
	scopes := CheckScopes(plan, &root)
	if len(scopes) != 2 {
		t.Fatalf("scopes = %+v, want the group and the root scope", scopes)
	}
	if scopes[1].ID != RootScopeID {
		t.Fatalf("root scope id = %q, want %q", scopes[1].ID, RootScopeID)
	}
	if !slices.Equal(scopes[1].Checks, []string{CheckNoLiveThreads}) {
		t.Fatalf("root scope checks = %v, want only the uncovered check", scopes[1].Checks)
	}
	empty := CheckScopes(Plan{}, &RequirementSet{Checks: []string{CheckNoLiveThreads}})
	if len(empty) != 1 || empty[0].ID != RootScopeID {
		t.Fatalf("empty plan scopes = %+v, want the root scope alone", empty)
	}
	if got := CheckScopes(Plan{}, &RequirementSet{}); len(got) != 0 {
		t.Fatalf("no requirements produced scopes %+v", got)
	}
}

// TestObserveScopesRequiresExactGroupAndHead pins the matching rule: only an
// observation naming the exact scope id, check, and fetched head counts, so
// an empty group, an unknown group, and a record from another head never
// satisfy a requirement.
func TestObserveScopesRequiresExactGroupAndHead(t *testing.T) {
	scopes := []CheckScope{
		{ID: "g1", Label: "one", Checks: []string{CheckHeadCurrent, CheckNoOpenAboveBlock}},
		{ID: "g2", Label: "two", Checks: []string{CheckHeadCurrent}},
	}
	observations := []CheckObservation{
		{Check: CheckHeadCurrent, Group: "g1", Head: "h1", Passed: true},
		{Check: CheckHeadCurrent, Group: "", Head: "h1", Passed: true},
		{Check: CheckHeadCurrent, Group: "not-a-group", Head: "h1", Passed: true},
		{Check: CheckHeadCurrent, Group: "g2", Head: "h0", Passed: true},
	}
	status := ObserveScopes(scopes, observations, "h1")
	if !status[0].Checks[CheckHeadCurrent] {
		t.Fatal("the exact group and head observation did not satisfy g1")
	}
	if status[1].Checks[CheckHeadCurrent] {
		t.Fatal("a record from another head satisfied g2")
	}
	if status[0].Checks[CheckNoOpenAboveBlock] {
		t.Fatal("an unobserved check read as passing")
	}
	pending := PendingChecks(scopes, observations, "h1")
	got := make([]string, 0, len(pending))
	for _, obs := range pending {
		got = append(got, obs.Group+"/"+obs.Check+"/"+obs.Head)
	}
	want := []string{"g1/" + CheckNoOpenAboveBlock + "/h1", "g2/" + CheckHeadCurrent + "/h1"}
	if !slices.Equal(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
}

// TestCheckComplianceRenameResolvesNewPathGroup pins the rename mapping: a
// finding on the pre-rename path resolves to the clauses and the group of
// the file's new path, never to a blank group.
func TestCheckComplianceRenameResolvesNewPathGroup(t *testing.T) {
	clause := ClauseProvenance{
		Clause:  core.BlockClause{Name: "renamed-block", Threshold: "important", Paths: []string{"src/**"}},
		Sources: []string{"scope renamed"},
	}
	res := &Resolved{
		Files: []FileResolution{{
			Path: "src/new.go", OldPath: "src/old.go",
			Requirements: RequirementSet{Clauses: []ClauseProvenance{clause}},
		}},
		Plan: Plan{Groups: []PlanGroup{{
			ID: "group-new", Label: "renamed group", Files: []string{"src/new.go"},
			Checks: []string{CheckNoOpenAboveBlock},
		}}},
	}
	blocked := CheckCompliance(res, nil, []FindingView{
		{Path: "src/old.go", Category: "logic", Severity: "critical", Status: "open"},
	})
	if len(blocked) != 1 {
		t.Fatalf("blocked = %+v, want the renamed-path blocker", blocked)
	}
	if blocked[0].Group != "group-new" || blocked[0].GroupLabel != "renamed group" || !blocked[0].GroupKnown {
		t.Fatalf("blocker group = %+v, want the new-path group identity and label", blocked[0])
	}
}

// TestClauseGlobNeverTreatsFindingPathAsPattern pins the matching direction:
// only the configured glob is a pattern, so a real file whose name carries
// glob bytes never matches a clause it would not otherwise match.
func TestClauseGlobNeverTreatsFindingPathAsPattern(t *testing.T) {
	clause := []ClauseProvenance{{
		Clause:  core.BlockClause{Name: "exact", Threshold: "critical", Paths: []string{"pay/core.go"}},
		Sources: []string{"repo"},
	}}
	if blocked := CheckCompliance(nil, clause, nil); len(blocked) != 0 {
		t.Fatalf("no resolution blocked %+v", blocked)
	}
	res := &Resolved{Files: []FileResolution{{
		Path: "pay/*.go", Requirements: RequirementSet{Clauses: clause},
	}}}
	literal := CheckCompliance(res, clause, []FindingView{
		{Path: "pay/*.go", Category: "logic", Severity: "critical", Status: "open"},
	})
	if len(literal) != 0 {
		t.Fatalf("literal glob bytes in a finding path blocked %+v", literal)
	}
	covered := CheckCompliance(res, clause, []FindingView{
		{Path: "pay/core.go", Category: "logic", Severity: "critical", Status: "open"},
	})
	if len(covered) != 1 {
		t.Fatalf("covered = %+v, want the exact path blocked", covered)
	}
}
