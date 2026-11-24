package diff

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	text := "diff --git a/a.txt b/a.txt\n" +
		"index 123..456 100644\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		" hello\n" +
		"-world\n" +
		"+world!\n" +
		"+extra\n" +
		"diff --git a/old.txt b/new.txt\n" +
		"similarity index 100%\n" +
		"rename from old.txt\n" +
		"rename to new.txt\n" +
		"diff --git a/bin.dat b/bin.dat\n" +
		"Binary files a/bin.dat and b/bin.dat differ\n" +
		"diff --git a/mode.sh b/mode.sh\n" +
		"old mode 100644\n" +
		"new mode 100755\n" +
		"diff --git a/nl.txt b/nl.txt\n" +
		"--- a/nl.txt\n" +
		"+++ b/nl.txt\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+a\n\\ No newline at end of file\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 5 {
		t.Fatalf("files = %d, want 5", len(files))
	}
	if files[0].Additions != 2 || files[0].Deletions != 1 {
		t.Fatalf("additions/deletions = %d/%d", files[0].Additions, files[0].Deletions)
	}
	assertFileFlags(t, files)
}

func assertFileFlags(t *testing.T, files []FileDiff) {
	t.Helper()
	if !files[1].IsRename {
		t.Fatal("rename not detected")
	}
	if !files[2].IsBinary {
		t.Fatal("binary not detected")
	}
	if !files[3].IsModeOnly {
		t.Fatal("mode-only not detected")
	}
}

func TestParseCapsOversizeInput(t *testing.T) {
	big := strings.Repeat("x", MaxDiffBytes+16)
	_, err := Parse(big)
	if err == nil {
		t.Fatal("expected ErrTooLarge")
	}
}

func gitLinesA(t *testing.T) []string {
	t.Helper()
	return []string{
		"package main",
		"func f() {",
		"\tprintln(1)",
		"}",
		"",
		"func g() {",
		"\tprintln(2)",
		"}",
	}
}

func gitLinesB(t *testing.T) []string {
	t.Helper()
	return []string{
		"package main",
		"func f() {",
		"\tprintln(42)",
		"}",
		"",
		"func g() {",
		"\tprintln(2)",
		"}",
	}
}

func writeGitFixtures(t *testing.T, dir string, a, b []string) {
	t.Helper()
	join := func(lines []string) string { return strings.Join(lines, "\n") + "\n" }
	mustWrite(t, filepath.Join(dir, "a.txt"), join(a))
	mustWrite(t, filepath.Join(dir, "old.txt"), join(a))
	mustWrite(t, filepath.Join(dir, "new.txt"), join(b))
}

func runGitHistogramDiff(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "diff", "--no-index", "--diff-algorithm=histogram", "--", "old.txt", "new.txt")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		t.Fatalf("git diff: %v", err)
	}
	return string(out)
}

func reconstruct(edits []Edit) []string {
	var got []string
	for i := range edits {
		if edits[i].Kind == EditEqual || edits[i].Kind == EditInsert {
			got = append(got, edits[i].Lines...)
		}
	}
	return got
}

func countChanges(edits []Edit) int {
	n := 0
	for i := range edits {
		if edits[i].Kind != EditEqual {
			n += len(edits[i].Lines)
		}
	}
	return n
}

func TestDiffLinesHistogramMatchesGit(t *testing.T) { //nolint:gocognit,revive,cyclop // parity corpus needs exhaustive per-case branching
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cases := []struct {
		name string
		a, b []string
	}{
		{"simple", gitLinesA(t), gitLinesB(t)},
		{"inserts", []string{"package main", "", "func f() {}"}, []string{"package main", "", "func f() {}", "", "// hi"}},
		{"deletes", []string{"a", "b", "c", "d"}, []string{"a", "d"}},
		{"replace", []string{"a", "b", "c"}, []string{"a", "x", "c"}},
		{"dupes", []string{"x", "x", "y"}, []string{"x", "y", "x"}},
		{"blankSlide", []string{"}", "", "func b() {}"}, []string{"}", "", "", "func b() {}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git not available")
			}
			dir := t.TempDir()
			writeGitFixtures(t, dir, tc.a, tc.b)
			gitText := runGitHistogramDiff(t, dir)
			if gitText == "" {
				// Empty diff means identical; skip hunk comparison but ensure
				// our DiffLines also reports no changes.
				if len(tc.a) == len(tc.b) && strings.Join(tc.a, "\n") == strings.Join(tc.b, "\n") {
					if countChanges(DiffLines(tc.a, tc.b)) != 0 {
						t.Fatal("identical inputs should have no changes")
					}
					return
				}
				t.Skip("git diff produced no output")
			}
			files, err := Parse(gitText)
			must(t, err)
			if len(files) == 0 {
				t.Fatal("git diff parse empty")
			}
			// Real hunk-boundary parity: deleted/added text per hunk must
			// match our line diff. A delete-all/insert-all replacement
			// (one huge del + one huge add) would produce many more changed
			// lines than git and fail this comparison.
			edits := DiffLines(tc.a, tc.b)
			if strings.Join(reconstruct(edits), "\n") != strings.Join(tc.b, "\n") {
				t.Fatalf("reconstruction want %q", tc.b)
			}
			if countChanges(edits) == 0 {
				t.Fatal("no changes in edit script")
			}
			gitDeletes, gitAdds := gitHunkChangeTexts(files)
			ourDeletes, ourAdds := editChangeTexts(edits)
			if !reflect.DeepEqual(gitDeletes, ourDeletes) || !reflect.DeepEqual(gitAdds, ourAdds) {
				t.Fatalf("hunk parity mismatch:\ngit del %v add %v\nour del %v add %v", gitDeletes, gitAdds, ourDeletes, ourAdds)
			}
			// Mutation proof: delete-all/insert-all must fail the parity
			// check (it would claim whole-file changes).
			trivial := []Edit{
				{Kind: EditDelete, Lines: tc.a},
				{Kind: EditInsert, Lines: tc.b},
			}
			trivialDel, trivialAdd := editChangeTexts(trivial)
			if reflect.DeepEqual(trivialDel, gitDeletes) && reflect.DeepEqual(trivialAdd, gitAdds) {
				t.Fatalf("trivial delete-all matched git for %q; test would not prove minimality", tc.name)
			}
			// Also prove our count is strictly minimal for cases with edits.
			if len(tc.a) > 0 && len(tc.b) > 0 && (len(gitDeletes) < len(tc.a) || len(gitAdds) < len(tc.b)) {
				if countChanges(trivial) <= countChanges(edits) {
					t.Fatalf("trivial count %d <= our %d", countChanges(trivial), countChanges(edits))
				}
			}
		})
	}
}

func gitHunkChangeTexts(files []FileDiff) (dels, adds []string) {
	for i := range files {
		d, a := gitFileChangeTexts(&files[i])
		dels = append(dels, d...)
		adds = append(adds, a...)
	}
	return dels, adds
}

func gitFileChangeTexts(f *FileDiff) (dels, adds []string) {
	for i := range f.Hunks {
		for j := range f.Hunks[i].Lines {
			dels, adds = appendHunkLine(&f.Hunks[i].Lines[j], dels, adds)
		}
	}
	return dels, adds
}

func appendHunkLine(l *Line, dels, adds []string) ([]string, []string) {
	if l.Kind == KindDel {
		dels = append(dels, l.Text)
	}
	if l.Kind == KindAdd {
		adds = append(adds, l.Text)
	}
	return dels, adds
}

func editChangeTexts(edits []Edit) (dels, adds []string) {
	for _, e := range edits {
		if e.Kind == EditDelete {
			dels = append(dels, e.Lines...)
		}
		if e.Kind == EditInsert {
			adds = append(adds, e.Lines...)
		}
	}
	return dels, adds
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := osWriteFile(path, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestParseHunkContentNotMetadata(t *testing.T) { //nolint:revive // metadata fixtures need exhaustive branch checks
	// Inside an active hunk, lines that look like metadata must be hunk
	// content: "-- ..." starts with '-' so it is a deletion, "+++ x"
	// starts with '+' so it is an addition, "--- a/fake" is also a
	// deletion. They must not be consumed as ---/+++ headers or index.
	text := "diff --git a/a.txt b/a.txt\n" +
		"index 1..2 100644\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		"--- a/fake\n" +
		"+++ x\n" +
		"-- sql comment\n" +
		"diff --git a/old.txt b/new.txt\n" +
		"similarity index 100%\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2 (metadata inside hunk must not open a file)", len(files))
	}
	// First file: hunk must have consumed the three prefix lines as content.
	f0 := files[0]
	if len(f0.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(f0.Hunks))
	}
	if len(f0.Hunks[0].Lines) != 3 {
		t.Fatalf("hunk lines = %d, want 3 got %v", len(f0.Hunks[0].Lines), f0.Hunks[0].Lines)
	}
	// "--- a/fake" is a deletion (line starting with '-'), "+++ x" an addition.
	if f0.Hunks[0].Lines[0].Kind != KindDel || f0.Hunks[0].Lines[0].Text != "-- a/fake" {
		t.Fatalf("line0 = %+v, want del -- a/fake", f0.Hunks[0].Lines[0])
	}
	if f0.Hunks[0].Lines[1].Kind != KindAdd || f0.Hunks[0].Lines[1].Text != "++ x" {
		t.Fatalf("line1 = %+v, want add ++ x", f0.Hunks[0].Lines[1])
	}
	if f0.Hunks[0].Lines[2].Kind != KindDel || f0.Hunks[0].Lines[2].Text != "- sql comment" {
		t.Fatalf("line2 = %+v, want del - sql comment", f0.Hunks[0].Lines[2])
	}
	// After the hunk is exhausted, the next diff --git must still open a file.
	if files[1].OldPath != "old.txt" || files[1].NewPath != "new.txt" {
		t.Fatalf("second file paths = %q/%q", files[1].OldPath, files[1].NewPath)
	}
}

func TestParseHunkIndexInsideHunk(t *testing.T) {
	// A line "index ..." inside a hunk starting with space is context, not
	// an outer index header. We emulate by a context line " index 1..2".
	text := "diff --git a/a.txt b/a.txt\n" +
		"index 1..2 100644\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" index 1..2 inside\n" +
		"-old\n" +
		"+new\n" +
		"diff --git a/b.txt b/b.txt\n" +
		"index 3..4 100644\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	// The context line must be in the first file's hunk, not an index.
	if files[0].Index != "1..2 100644" {
		t.Fatalf("index = %q, want outer index", files[0].Index)
	}
	if len(files[0].Hunks) != 1 || len(files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("hunk = %+v", files[0].Hunks)
	}
	if files[1].Index != "3..4 100644" {
		t.Fatalf("second index = %q", files[1].Index)
	}
}

func TestParseDiffOfDiff(t *testing.T) {
	// A diff whose payload is itself a diff: inner "diff --git" lines are
	// prefixed by the outer hunk marker (context/add) and must not open
	// new files.
	text := "diff --git a/outer b/outer\n" +
		"index 1..2 100644\n" +
		"--- a/outer\n" +
		"+++ b/outer\n" +
		"@@ -1,3 +1,3 @@\n" +
		" diff --git a/inner b/inner\n" +
		"-old\n" +
		"+diff --git a/inner b/inner\n" +
		"diff --git a/real b/real\n" +
		"index 9..9 100644\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2, got %+v", len(files), files)
	}
	// The outer file's hunk must hold the three lines, including the inner
	// diff headers as content.
	if len(files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("outer hunk lines = %d", len(files[0].Hunks[0].Lines))
	}
	if files[0].Hunks[0].Lines[0].Kind != KindContext ||
		files[0].Hunks[0].Lines[0].Text != "diff --git a/inner b/inner" {
		t.Fatalf("line0 = %+v", files[0].Hunks[0].Lines[0])
	}
	if files[1].OldPath != "real" {
		t.Fatalf("second file = %q", files[1].OldPath)
	}
}

func TestParseNoNewlineMarkerSingleBackslash(t *testing.T) {
	// The real marker is a single backslash "\ No newline at end of file".
	// The previous raw literal "\\" (two backslashes) never matched it.
	text := "diff --git a/nl.txt b/nl.txt\n" +
		"--- a/nl.txt\n" +
		"+++ b/nl.txt\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+a\n\\ No newline at end of file\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	if len(files[0].Hunks[0].Lines) != 2 {
		t.Fatalf("lines = %d, marker must not become a content line", len(files[0].Hunks[0].Lines))
	}
}

func TestParseHunkRemainTracksCounts(t *testing.T) {
	// While oldRem/newRem are still >0, "--- " and "+++ " are content;
	// after they expire the next hunk header is a header again.
	text := "diff --git a/f b/f\n" +
		"--- a/f\n" +
		"+++ b/f\n" +
		"@@ -1,2 +1,2 @@\n" +
		"--- a/fake\n" +
		"+++ x\n" +
		"@@ -3,1 +3,1 @@\n" +
		"-z\n" +
		"+y\n"
	files, err := Parse(text)
	must(t, err)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	if len(files[0].Hunks) != 2 {
		t.Fatalf("hunks = %d, second @@ must be a header", len(files[0].Hunks))
	}
	if len(files[0].Hunks[0].Lines) != 2 {
		t.Fatalf("first hunk lines = %d", len(files[0].Hunks[0].Lines))
	}
}

func TestHistogramDeterminism(t *testing.T) {
	// histogramAnchor used to iterate a Go map with strict '<', giving
	// randomized tie-breaks (min=2 max=4 across runs). The fix scans a in
	// index order with earliest-position tie-break. Run 64 times to prove
	// one exact script.
	a := []string{"a", "b", "a", "b", "x", "a", "b"}
	b := []string{"a", "b", "a", "b", "y", "a", "b"}
	const runs = 64
	first := DiffLines(a, b)
	firstStr := editsString(first)
	for i := range runs {
		got := DiffLines(a, b)
		if editsString(got) != firstStr {
			t.Fatalf("nondeterministic run %d: got %s want %s", i, editsString(got), firstStr)
		}
	}
	// Pin the exact literal: the deterministic anchor is the earliest
	// lowest-frequency line. For this tied input the anchor coalesces.
	if got := reconstruct(first); !reflect.DeepEqual(got, b) {
		t.Fatalf("reconstruct = %v want %v", got, b)
	}
	if firstStr != "EQ[a,b,a,b] DEL[x] INS[y] EQ[a,b]" {
		t.Fatalf("literal script = %q", firstStr)
	}
}

func editsString(edits []Edit) string {
	var b strings.Builder
	for i, e := range edits {
		if i > 0 {
			b.WriteByte(' ')
		}
		switch e.Kind {
		case EditEqual:
			b.WriteString("EQ")
		case EditInsert:
			b.WriteString("INS")
		case EditDelete:
			b.WriteString("DEL")
		}
		b.WriteString("[")
		b.WriteString(strings.Join(e.Lines, ","))
		b.WriteString("]")
	}
	return b.String()
}
