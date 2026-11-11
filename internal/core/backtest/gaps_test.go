package backtest

import (
	"slices"
	"testing"
)

// TestMineMergeSeed proves a merge commit whose subject matches a seed rule is
// counted as merge-seed, never mined as an ordinary fix.
func TestMineMergeSeed(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("a.txt", "two\n")
	r.commit("feat: change the file")
	r.switchMain()
	merge := r.merge("feature", "fix: merge the feature")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %+v; want none", data.Mine.Candidates)
	}
	want := Skipped{Reason: "merge-seed", Fix: merge}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}

// TestMineBlankLine proves a deleted whitespace-only line is its own row: it
// cannot anchor, so it never reaches blame.
func TestMineBlankLine(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("a.txt", "one\n\nbug\n")
	r.commit("feat: add the bug")
	r.switchMain()
	r.merge("feature", "Merge pull request #31 from acme/feature")
	r.write("a.txt", "one\nbug\n")
	fix := r.commit("fix: drop the blank line")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %+v; want none", data.Mine.Candidates)
	}
	want := Skipped{Reason: "blank-line", Fix: fix, Detail: "a.txt:2"}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}

// TestMineRevertOfRevertIsNotASeed proves the revert-of-revert exemption: the
// commit is not a seed, so it contributes neither a candidate nor a skip.
func TestMineRevertOfRevertIsNotASeed(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("feat: base")
	r.write("a.txt", "two\n")
	r.commit(`Revert "Revert "feat: change the file""`)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 || len(data.Mine.Skipped) != 0 {
		t.Errorf("candidates = %+v, skipped = %+v; want neither",
			data.Mine.Candidates, data.Mine.Skipped)
	}
}

// TestMineMultiHunkFix proves one fix commit with two hunks anchors both
// deleted lines at their own old-side positions.
func TestMineMultiHunkFix(t *testing.T) {
	r, base, fix := twoHunkRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	candidate := data.Mine.Candidates[0]
	want := []Bug{
		{Path: "a.txt", Line: 2, Kind: "unclassified", Fix: fix},
		{Path: "a.txt", Line: 9, Kind: "unclassified", Fix: fix},
	}
	if !slices.EqualFunc(candidate.Bugs, want, func(a, b Bug) bool { return a == b }) {
		t.Errorf("bugs = %+v; want %+v", candidate.Bugs, want)
	}
	if !slices.Equal(candidate.Fixes, []string{fix}) {
		t.Errorf("fixes = %v; want [%s]", candidate.Fixes, fix)
	}
	if candidate.PR == nil || *candidate.PR != 21 {
		t.Errorf("pr = %v; want 21", candidate.PR)
	}
}

// TestMineRenameOff proves --no-renames is in force: a pure rename is read as
// a deletion of the old path, whose line anchors at the PR head, not as a
// rename with no hunks.
func TestMineRenameOff(t *testing.T) {
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("old.txt", "keep me\n")
	r.commit("feat: add the file")
	r.switchMain()
	r.merge("feature", "Merge pull request #32 from acme/feature")
	r.git("mv", "old.txt", "new.txt")
	fix := r.commit("fix: rename the file")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	want := []Bug{{Path: "old.txt", Line: 1, Kind: "unclassified", Fix: fix}}
	if !slices.EqualFunc(data.Mine.Candidates[0].Bugs, want, func(a, b Bug) bool { return a == b }) {
		t.Errorf("bugs = %+v; want %+v", data.Mine.Candidates[0].Bugs, want)
	}
}

// TestMineCRLFDeletion proves a CRLF file's deleted line keeps its bytes
// through the parse and still anchors at the PR head.
func TestMineCRLFDeletion(t *testing.T) {
	r := newRepo(t)
	r.write("w.txt", "top\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("w.txt", "top\r\nbug\r\n")
	r.commit("feat: add the windows line")
	r.switchMain()
	r.merge("feature", "Merge pull request #33 from acme/feature")
	r.write("w.txt", "top\r\nfixed\r\n")
	fix := r.commit("fix: repair the windows file")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	want := []Bug{{Path: "w.txt", Line: 2, Kind: "unclassified", Fix: fix}}
	if !slices.EqualFunc(data.Mine.Candidates[0].Bugs, want, func(a, b Bug) bool { return a == b }) {
		t.Errorf("bugs = %+v; want %+v", data.Mine.Candidates[0].Bugs, want)
	}
}

// TestMineDeletedLinePatternChars proves a deleted line whose text starts with
// `-` and carries regex characters is searched literally, not as a pattern.
func TestMineDeletedLinePatternChars(t *testing.T) {
	r := newRepo(t)
	r.write("notes.txt", "alpha\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("notes.txt", "alpha\n- dash [id] (x) .* ^$ \\\n")
	r.commit("feat: add the odd line")
	r.switchMain()
	r.merge("feature", "Merge pull request #34 from acme/feature")
	r.write("notes.txt", "alpha\nbullet\n")
	fix := r.commit("fix: replace the odd line")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 1 {
		t.Fatalf("candidates = %+v; skipped %+v; want one", data.Mine.Candidates, data.Mine.Skipped)
	}
	want := []Bug{{Path: "notes.txt", Line: 2, Kind: "unclassified", Fix: fix}}
	if !slices.EqualFunc(data.Mine.Candidates[0].Bugs, want, func(a, b Bug) bool { return a == b }) {
		t.Errorf("bugs = %+v; want %+v", data.Mine.Candidates[0].Bugs, want)
	}
}
