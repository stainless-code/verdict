package backtest

import (
	"slices"
	"testing"
)

// newPartialRepo lands one PR that adds a buggy main.go line and a vendored
// file, then a fix that deletes both: the main.go line anchors, and the
// vendored line must still be counted.
func newPartialRepo(t *testing.T) (r *repo, base, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"missing\")\n}\n")
	r.write("vendor/lib/x.go", "package lib\n\nvar X = 1\n")
	r.commit("feat: remove the output and vendor a lib")
	r.switchMain()
	r.merge("feature", "Merge pull request #6 from acme/feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.Remove(\"missing\"); err != nil {\n\t\tpanic(err)\n\t}\n}\n")
	r.write("vendor/lib/x.go", "package lib\n\nvar X = 2\n")
	fix = r.commit("fix: guard the removal")
	return r, base, fix
}

// TestMinePartialAnchorKeepsCandidateAndCountsDrops proves one fix can yield a
// candidate and a skipped row at once: the anchorable line is a bug, and the
// excluded line is counted with its own position.
func TestMinePartialAnchorKeepsCandidateAndCountsDrops(t *testing.T) {
	r, base, fix := newPartialRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %d; want 1", len(data.Mine.Candidates))
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "f6714222c698602eb028730d02d83a148904b7a0",
		Base:           "84b31730499aa237889660981350fda2b3edc91d",
		LandedBy:       "ffe6fa6264b594ae9a20c07ae11b6d7eb9caaf81",
		PR:             new(6),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"fc25e49b6336b4245e2f65f4fb398be30a034317"},
		Bugs:           []Bug{{Path: "main.go", Line: 6, Kind: "missing-error-check", Fix: "fc25e49b6336b4245e2f65f4fb398be30a034317"}},
	})
	if fix != "fc25e49b6336b4245e2f65f4fb398be30a034317" {
		t.Fatalf("fixture fix = %s; want the literal fix id", fix)
	}
	want := []Skipped{{Reason: "vendor", Fix: fix, Detail: "vendor/lib/x.go:3"}}
	if !slices.Equal(data.Mine.Skipped, want) {
		t.Errorf("skipped = %+v; want %+v", data.Mine.Skipped, want)
	}
}
