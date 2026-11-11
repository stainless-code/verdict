package backtest

import "testing"

// oddPathRepo lands a merged PR that adds a path a glob would miss and a path
// a leading colon would read as pathspec magic, then fixes both lines.
func oddPathRepo(t *testing.T) (r *repo, base, feature, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("app/[id].tsx", "export const id = 1\n")
	r.write(":colon.txt", "alpha\n")
	feature = r.commit("feat: add the odd paths")
	r.switchMain()
	r.merge("feature", "Merge pull request #11 from acme/feature")
	r.write("app/[id].tsx", "export const id = 2\n")
	r.write(":colon.txt", "beta\n")
	fix = r.commit("fix: bump the values")
	return r, base, feature, fix
}

// TestMineLiteralPathspecs proves blame and grep read a glob-shaped path and a
// colon-leading path as file names, not as git pathspecs: both deleted lines
// anchor at the PR head.
func TestMineLiteralPathspecs(t *testing.T) {
	r, base, feature, fix := oddPathRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	assertCandidate(t, &data.Mine.Candidates[0], &Candidate{
		Head:           "4be836e618d96bb89cf193f11aaf83933a25df0d",
		Base:           "d1f092651db36fccbc9def73b254d956c94dc313",
		LandedBy:       "7e52368cbaabe7827cd03a8d952729ea46090c17",
		PR:             new(11),
		Reconstruction: "first-parent-merge",
		HeadSource:     "merge-time",
		MergedAt:       "2024-01-03T00:00:00Z",
		HeadAt:         "2024-01-02T00:00:00Z",
		Fixes:          []string{"654560ae429430b5cb1765e0f7ce480e9bbbaa06"},
		Bugs: []Bug{
			{Path: ":colon.txt", Line: 1, Kind: "unclassified", Fix: "654560ae429430b5cb1765e0f7ce480e9bbbaa06"},
			{Path: "app/[id].tsx", Line: 1, Kind: "unclassified", Fix: "654560ae429430b5cb1765e0f7ce480e9bbbaa06"},
		},
	})
	if feature == "" || fix == "" || data.Since.SHA != base {
		t.Errorf("fixture ids = %s %s %s; want non-empty", base, feature, fix)
	}
}
