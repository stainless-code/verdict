package backtest

import (
	"fmt"
	"strings"
	"testing"
)

// bigPRFiles and bigPRLine size the bulk branch so the PR diff clears fetch's
// 512,000-byte cap while the fix that later deletes the buggy line stays tiny.
const (
	bigPRFiles = 600
	bigPRLine  = 1024
)

// bigPRRepo builds a merged PR whose diff is over the fetch cap and whose
// branch also introduces the canonical missing-check bug.
func bigPRRepo(t *testing.T) (r *repo, base, feature, merge, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	for i := range bigPRFiles {
		r.write(fmt.Sprintf("bulk/f%03d.txt", i), strings.Repeat("x", bigPRLine)+"\n")
	}
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"missing\")\n}\n")
	feature = r.commit("feat: remove the output")
	r.switchMain()
	merge = r.merge("feature", "Merge pull request #9 from acme/feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.Remove(\"missing\"); err != nil {\n\t\tpanic(err)\n\t}\n}\n")
	fix = r.commit("fix: guard the removal")
	return r, base, feature, merge, fix
}

// TestMineLargePRDiff proves a PR over fetch's diff cap is one skipped row.
func TestMineLargePRDiff(t *testing.T) {
	r, base, feature, _, fix := bigPRRepo(t)
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %+v; want none", data.Mine.Candidates)
	}
	if fix != "9c977b2548f0435ebd2089ebb7efb09c1c0376e6" {
		t.Fatalf("fixture fix = %s; want the literal fix id", fix)
	}
	want := Skipped{Reason: "large-pr-diff", Fix: fix, PR: feature, Detail: "main.go:6"}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}

// TestMineSinceAncestorBeforeLargePR proves the since check answers before the
// PR diff is read: a PR that both landed at --since and clears the cap reads
// ancestor-of-since, never large-pr-diff.
func TestMineSinceAncestorBeforeLargePR(t *testing.T) {
	r, _, feature, merge, fix := bigPRRepo(t)
	data := runMine(t, r, merge)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %+v; want none", data.Mine.Candidates)
	}
	if fix != "9c977b2548f0435ebd2089ebb7efb09c1c0376e6" {
		t.Fatalf("fixture fix = %s; want the literal fix id", fix)
	}
	want := Skipped{Reason: "ancestor-of-since", Fix: fix, PR: feature, Detail: "main.go:6"}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}
