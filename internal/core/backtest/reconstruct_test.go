package backtest

import "testing"

// newTitledFeatureRepo merges a feature branch whose own commit is titled like
// a squash landing, `(#12)`. The title alone must not make that commit a squash
// PR head: it is inside the merged PR, not on the first-parent line.
func newTitledFeatureRepo(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"missing\")\n}\n")
	r.commit("feat: refactor foo (#12)")
	r.switchMain()
	r.merge("feature", "Merge pull request #2 from acme/feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.Remove(\"missing\"); err != nil {\n\t\tpanic(err)\n\t}\n}\n")
	r.commit("fix: guard the removal")
	return r, base
}

// TestMineTitledFeatureCommitIsNotASquashLanding proves a `(#N)` title inside a
// merged PR reconstructs through the merge, not as its own squash PR.
func TestMineTitledFeatureCommitIsNotASquashLanding(t *testing.T) {
	r, base := newTitledFeatureRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "bbc9764b79d471ff224e627d2ece0d944613e9d9",
		Base:           "84b31730499aa237889660981350fda2b3edc91d",
		LandedBy:       "364d2fc1fcb52e3ff0e334953bcc825f0b8ba689",
		PR:             new(2),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"44344ea9b1123ed91014400065adedf44a6eae11"},
		Bugs:           []Bug{{Path: "main.go", Line: 6, Kind: "missing-error-check", Fix: "44344ea9b1123ed91014400065adedf44a6eae11"}},
	})
}
