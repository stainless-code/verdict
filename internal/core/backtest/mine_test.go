package backtest

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// assertCandidate compares a candidate field by field against a literal.
//
//nolint:revive // one field-family assertion block reads top to bottom
func assertCandidate(t *testing.T, got, want *Candidate) {
	t.Helper()
	if got.Head != want.Head || got.Base != want.Base || got.LandedBy != want.LandedBy {
		t.Errorf("candidate ids = (%s, %s, %s); want (%s, %s, %s)",
			got.Head, got.Base, got.LandedBy, want.Head, want.Base, want.LandedBy)
	}
	if got.Reconstruction != want.Reconstruction || got.HeadSource != want.HeadSource {
		t.Errorf("candidate shape = (%s, %s); want (%s, %s)",
			got.Reconstruction, got.HeadSource, want.Reconstruction, want.HeadSource)
	}
	if got.MergedAt != want.MergedAt || got.HeadAt != want.HeadAt {
		t.Errorf("candidate dates = (%s, %s); want (%s, %s)",
			got.MergedAt, got.HeadAt, want.MergedAt, want.HeadAt)
	}
	if !equalPR(got.PR, want.PR) {
		t.Errorf("candidate pr = %v; want %v", got.PR, want.PR)
	}
	if !slices.Equal(got.Fixes, want.Fixes) {
		t.Errorf("candidate fixes = %v; want %v", got.Fixes, want.Fixes)
	}
	if !slices.EqualFunc(got.Bugs, want.Bugs, func(a, b Bug) bool { return a == b }) {
		t.Errorf("candidate bugs = %+v; want %+v", got.Bugs, want.Bugs)
	}
}

// equalPR compares two optional PR numbers.
func equalPR(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// TestMineMergedFix is the canonical end-to-end fixture: a merged PR, a later
// fix, one anchored bug, and a byte-identical rerun.
func TestMineMergedFix(t *testing.T) {
	b, fix := newMergedFixRepo(t)
	data := runMine(t, b.r, b.base)
	if data.Run != "29320a6-84b3173" {
		t.Errorf("run = %q; want %q", data.Run, "29320a6-84b3173")
	}
	if data.Since.SHA != b.base || data.Until.SHA != fix {
		t.Errorf("window = %s..%s; want %s..%s", data.Since.SHA, data.Until.SHA, b.base, fix)
	}
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "1d1518cf4adca036e1325e9666d54094dccacfa5",
		Base:           "84b31730499aa237889660981350fda2b3edc91d",
		LandedBy:       "e9c8f1d1d1363d94c4c766eacf3d1d6fc6e09eb8",
		PR:             new(2),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"29320a6e9fac8488fb678921021d68af35843beb"},
		Bugs:           []Bug{{Path: "main.go", Line: 6, Kind: "missing-error-check", Fix: "29320a6e9fac8488fb678921021d68af35843beb"}},
	})
	if data.Mine.Skipped == nil {
		t.Error("skipped is nil; want an empty array")
	}
	first := readMine(t, b.r, data.Run)
	runMine(t, b.r, b.base)
	second := readMine(t, b.r, data.Run)
	if !bytes.Equal(first, second) {
		t.Errorf("mine.json is not byte-identical on rerun:\n%s\n---\n%s", first, second)
	}
}

// readMine reads one run's mine.json.
func readMine(t *testing.T, r *repo, run string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, ".verdict", "backtest", run, "mine.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestMineRevert proves a revert commit seeds the same merged PR.
func TestMineRevert(t *testing.T) {
	b, _ := newRevertRepo(t)
	data := runMine(t, b.r, b.base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "1d1518cf4adca036e1325e9666d54094dccacfa5",
		Base:           "84b31730499aa237889660981350fda2b3edc91d",
		LandedBy:       "e9c8f1d1d1363d94c4c766eacf3d1d6fc6e09eb8",
		PR:             new(2),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"c1aaf5167e48a51abd54314631d8cf8a7fca9df1"},
		Bugs:           []Bug{{Path: "main.go", Line: 6, Kind: "unclassified", Fix: "c1aaf5167e48a51abd54314631d8cf8a7fca9df1"}},
	})
}

// TestMineSquash proves a squash commit is its own PR head.
func TestMineSquash(t *testing.T) {
	r, base, _, _ := newSquashRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "ae2c17e7a32538a5983f96b26da583cfaf266972",
		Base:           "347b586392de9fa956da0fd0d34b2935f7e173d9",
		LandedBy:       "ae2c17e7a32538a5983f96b26da583cfaf266972",
		PR:             new(7),
		Reconstruction: "squash-commit",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-03T00:00:00Z",
		Fixes:          []string{"2467cb3da814ba1b89c1442e1390b6958bbb798a"},
		Bugs:           []Bug{{Path: "a.txt", Line: 1, Kind: "unclassified", Fix: "2467cb3da814ba1b89c1442e1390b6958bbb798a"}},
	})
}

// TestMineDirectPush proves a commit with no containing merge is no-pr.
func TestMineDirectPush(t *testing.T) {
	r, base, _, fix := newDirectRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %d; want 0", len(data.Mine.Candidates))
	}
	want := Skipped{Reason: "no-pr", Fix: fix, Detail: "a.txt:1"}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}

// TestMineUnaligned proves duplicate line text is skipped, not guessed, and
// that each duplicated deleted line keeps its own row.
func TestMineUnaligned(t *testing.T) {
	r, base, feature, fix := newUnalignedRepo(t)
	data := runMine(t, r, base)
	want := []Skipped{
		{Reason: "unaligned", Fix: fix, PR: feature, Detail: "main.go:9"},
		{Reason: "unaligned", Fix: fix, PR: feature, Detail: "main.go:10"},
	}
	if !slices.Equal(data.Mine.Skipped, want) {
		t.Errorf("skipped = %+v; want %+v", data.Mine.Skipped, want)
	}
}
