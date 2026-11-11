package backtest

import (
	"runtime"
	"testing"
)

// globPathRepo lands a merged PR that adds a glob-shaped path, then fixes its
// line.
func globPathRepo(t *testing.T) (r *repo, base string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("app/[id].tsx", "export const id = 1\n")
	r.commit("feat: add the odd path")
	r.switchMain()
	r.merge("feature", "Merge pull request #11 from acme/feature")
	r.write("app/[id].tsx", "export const id = 2\n")
	r.commit("fix: bump the value")
	return r, base
}

// colonPathRepo lands a merged PR that adds a colon-leading path, then fixes
// its line.
func colonPathRepo(t *testing.T) (r *repo, base string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write(":colon.txt", "alpha\n")
	r.commit("feat: add the odd path")
	r.switchMain()
	r.merge("feature", "Merge pull request #11 from acme/feature")
	r.write(":colon.txt", "beta\n")
	r.commit("fix: bump the value")
	return r, base
}

// TestMineLiteralGlobPaths proves blame and grep read a glob-shaped path as a
// file name, not as a glob: the deleted line anchors at the PR head.
func TestMineLiteralGlobPaths(t *testing.T) {
	r, base := globPathRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "9e00ef5412d7e6b9bbbc2b522feeba99122d51d7",
		Base:           "d1f092651db36fccbc9def73b254d956c94dc313",
		LandedBy:       "0f2e8157d178d633943d754b0e6acbf5b9e8cada",
		PR:             new(11),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"90413c3e3c7dd9df9817cfe794467ccc2b6626b6"},
		Bugs: []Bug{
			{Path: "app/[id].tsx", Line: 1, Kind: "unclassified", Fix: "90413c3e3c7dd9df9817cfe794467ccc2b6626b6"},
		},
	})
}

// TestMineLiteralColonPath proves blame and grep read a colon-leading path as
// a file name, not as pathspec magic.
func TestMineLiteralColonPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NTFS forbids ':' in file names; the pathspec-magic case is proven on POSIX")
	}
	r, base := colonPathRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "b4e1005d05ebdbfed1a2e222348a553c4591ec23",
		Base:           "d1f092651db36fccbc9def73b254d956c94dc313",
		LandedBy:       "4ff4566c92787c79aa4c691f783b0cdacb53e7ab",
		PR:             new(11),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"e9a5f1bcb73d222fca5e6cfba4c582bcf345dccf"},
		Bugs: []Bug{
			{Path: ":colon.txt", Line: 1, Kind: "unclassified", Fix: "e9a5f1bcb73d222fca5e6cfba4c582bcf345dccf"},
		},
	})
}
