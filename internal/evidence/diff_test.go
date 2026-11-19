package evidence_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/hunks"
)

func TestIndexDiffLineMapsAndBoundaries(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/pkg/calc.go b/pkg/calc.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/pkg/calc.go\n" +
		"+++ b/pkg/calc.go\n" +
		"@@ -10,4 +10,4 @@ func add(a, b int) int {\n" +
		" \t// leading context\n" +
		"-\treturn a - b\n" +
		"+\treturn a + b\n" +
		" \t// trailing context\n" +
		" }\n" +
		"@@ -30,2 +30,3 @@ func sub(a, b int) int {\n" +
		" \tif a < b {\n" +
		"+\t\treturn 0\n" +
		" \t}\n"

	idx := evidence.IndexDiff([]byte(diff))
	df := requireFile(t, idx, "pkg/calc.go")
	assertPaths(t, df, "pkg/calc.go", "pkg/calc.go", "pkg/calc.go")
	if df.AbsentAtHead() {
		t.Fatal("AbsentAtHead() = true, want false")
	}

	wantNew := map[int]string{
		10: "\t// leading context",
		11: "\treturn a + b",
		12: "\t// trailing context",
		13: "}",
		30: "\tif a < b {",
		31: "\t\treturn 0",
		32: "\t}",
	}
	wantOld := map[int]string{
		10: "\t// leading context",
		11: "\treturn a - b",
		12: "\t// trailing context",
		13: "}",
		30: "\tif a < b {",
		31: "\t}",
	}
	assertLineMap(t, "NewLines", df.NewLines, wantNew)
	assertLineMap(t, "OldLines", df.OldLines, wantOld)

	if len(df.Hunks) != 2 {
		t.Fatalf("len(Hunks) = %d, want 2", len(df.Hunks))
	}
	assertHunkBounds(t, &df.Hunks[0], 10, 13, 10, 13, "pkg/calc.go\tNEW\t10\t13")
	assertHunkBounds(t, &df.Hunks[1], 30, 31, 30, 32, "pkg/calc.go\tNEW\t30\t32")
}

func TestIndexDiffRenameCQuotedAndNoNewline(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/old/alpha.go b/new/beta.go\n" +
		"similarity index 80%\n" +
		"rename from old/alpha.go\n" +
		"rename to new/beta.go\n" +
		"--- a/old/alpha.go\n" +
		"+++ b/new/beta.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		" package p\n" +
		"-var x = 1\n" +
		"\\ No newline at end of file\n" +
		"+var x = 2\n" +
		"\\ No newline at end of file\n" +
		"diff --git \"a/dir/h\\303\\251llo.go\" \"b/dir/h\\303\\251llo.go\"\n" +
		"--- \"a/dir/h\\303\\251llo.go\"\n" +
		"+++ \"b/dir/h\\303\\251llo.go\"\n" +
		"@@ -5,1 +5,1 @@\n" +
		"-oldValue := 1\n" +
		"+newValue := 2\n"

	idx := evidence.IndexDiff([]byte(diff))

	dfNew := requireFile(t, idx, "new/beta.go")
	dfOld := requireFile(t, idx, "old/alpha.go")
	if dfNew != dfOld {
		t.Fatal("rename lookup returned different sections for old and new paths")
	}
	assertLineMap(t, "rename NewLines", dfNew.NewLines, map[int]string{1: "package p", 2: "var x = 2"})
	assertLineMap(t, "rename OldLines", dfNew.OldLines, map[int]string{1: "package p", 2: "var x = 1"})
	assertHunkBounds(t, &dfNew.Hunks[0], 1, 2, 1, 2, "new/beta.go\tNEW\t1\t2")

	dfQuoted := requireFile(t, idx, "dir/héllo.go")
	assertLineMap(t, "C-quoted NewLines", dfQuoted.NewLines, map[int]string{5: "newValue := 2"})
	assertLineMap(t, "C-quoted OldLines", dfQuoted.OldLines, map[int]string{5: "oldValue := 1"})
	assertHunkBounds(t, &dfQuoted.Hunks[0], 5, 5, 5, 5, "dir/héllo.go\tNEW\t5\t5")

	wantKeys := []string{
		"dir/héllo.go\tNEW\t5\t5",
		"new/beta.go\tNEW\t1\t2",
	}
	if gotKeys := idx.ReceiptKeys(nil); !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("ReceiptKeys = %q, want %q", gotKeys, wantKeys)
	}
}

func TestIndexDiffAmbiguousPathAndDrift(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/dup.go b/dup.go\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-a\n" +
		"+b\n" +
		"diff --git a/dup.go b/dup.go\n" +
		"@@ -5,1 +5,1 @@\n" +
		"-c\n" +
		"+d\n"

	idx := evidence.IndexDiff([]byte(diff))
	df, reason := idx.File("dup.go")
	if df != nil || reason != "ambiguous-path" {
		t.Fatalf("File(dup.go) = (%v, %q), want (nil, ambiguous-path)", df, reason)
	}
	if idx.HasPath("dup.go") {
		t.Fatal("HasPath(dup.go) = true, want false for ambiguous path")
	}

	matchingSnapshot := []hunks.Hunk{
		{File: "dup.go", Side: "OLD", Start: 1, End: 1},
		{File: "dup.go", Side: "NEW", Start: 1, End: 1},
		{File: "dup.go", Side: "OLD", Start: 5, End: 5},
		{File: "dup.go", Side: "NEW", Start: 5, End: 5},
	}
	if evidence.HasHunkIndexDrift([]byte(diff), matchingSnapshot) {
		t.Fatal("HasHunkIndexDrift = true for matching snapshot, want false")
	}
	staleSnapshot := []hunks.Hunk{
		{File: "dup.go", Side: "OLD", Start: 1, End: 1},
		{File: "dup.go", Side: "NEW", Start: 1, End: 1},
	}
	if !evidence.HasHunkIndexDrift([]byte(diff), staleSnapshot) {
		t.Fatal("HasHunkIndexDrift = false for stale snapshot, want true")
	}
	// The same diff written CRLF is the same diff: its snapshot compares
	// equal once the line endings are normalized.
	if evidence.HasHunkIndexDrift([]byte(crlfDiff(diff)), matchingSnapshot) {
		t.Fatal("HasHunkIndexDrift = true for a CRLF diff with a matching snapshot, want false")
	}
}

func requireFile(t *testing.T, idx *evidence.DiffIndex, path string) *evidence.DiffFile {
	t.Helper()
	df, reason := idx.File(path)
	if reason != "" || df == nil {
		t.Fatalf("File(%q) = (%v, %q), want non-nil and empty reason", path, df, reason)
	}
	return df
}

func assertPaths(t *testing.T, df *evidence.DiffFile, oldPath, newPath, canonical string) {
	t.Helper()
	if df.OldPath != oldPath || df.Path != newPath || df.Canonical != canonical {
		t.Fatalf("paths = (%q, %q, %q), want (%q, %q, %q)", df.OldPath, df.Path, df.Canonical, oldPath, newPath, canonical)
	}
}

func assertHunkBounds(t *testing.T, h *evidence.DiffHunk, oldStart, oldEnd, newStart, newEnd int, wantKey string) {
	t.Helper()
	if h.OldStart != oldStart || h.OldEnd != oldEnd || h.NewStart != newStart || h.NewEnd != newEnd {
		t.Fatalf("hunk bounds = OLD %d..%d NEW %d..%d, want OLD %d..%d NEW %d..%d",
			h.OldStart, h.OldEnd, h.NewStart, h.NewEnd, oldStart, oldEnd, newStart, newEnd)
	}
	if h.Key() != wantKey {
		t.Fatalf("hunk.Key() = %q, want %q", h.Key(), wantKey)
	}
}

func assertLineMap(t *testing.T, label string, got, want map[int]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len = %d (%v), want %d (%v)", label, len(got), got, len(want), want)
	}
	for k, wantV := range want {
		if gotV, ok := got[k]; !ok || gotV != wantV {
			t.Fatalf("%s[%d] = (%q, %v), want %q", label, k, gotV, ok, wantV)
		}
	}
}

// crlfDiff is the LF fixture above with every line ending written CRLF, the
// way a Windows checkout hands the bytes to full.diff.
func crlfDiff(lf string) string {
	return strings.ReplaceAll(lf, "\n", "\r\n")
}

// TestIndexDiffCRLFMatchesLF proves R2: normalizing at the index entry keeps
// the receipt keys and the quotable line text identical to the LF diff, so a
// CRLF bundle is neither unkeyable nor quoted differently.
func TestIndexDiffCRLFMatchesLF(t *testing.T) {
	t.Parallel()
	lf := "diff --git a/pkg/calc.go b/pkg/calc.go\n" +
		"--- a/pkg/calc.go\n" +
		"+++ b/pkg/calc.go\n" +
		"@@ -10,3 +10,3 @@\n" +
		" \t// context\n" +
		"-\treturn a - b\n" +
		"+\treturn a + b\n" +
		" }\n" +
		"diff --git a/notes.txt b/notes.txt\n" +
		"--- a/notes.txt\n" +
		"+++ b/notes.txt\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old line\n" +
		"+new line\n"

	for _, tc := range []struct{ name, diff string }{
		{name: "lf", diff: lf},
		{name: "crlf", diff: crlfDiff(lf)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCRLFIndexMatches(t, evidence.IndexDiff([]byte(tc.diff)))
		})
	}
}

// assertCRLFIndexMatches pins one index against the literals the LF diff
// produces, so a CRLF bundle is graded by the same keys and line text.
func assertCRLFIndexMatches(t *testing.T, idx *evidence.DiffIndex) {
	t.Helper()
	want := []string{"notes.txt\tNEW\t1\t1", "pkg/calc.go\tNEW\t10\t12"}
	if got := idx.ReceiptKeys(nil); !slices.Equal(got, want) {
		t.Fatalf("ReceiptKeys = %q, want %q", got, want)
	}
	df, reason := idx.File("pkg/calc.go")
	if reason != "" {
		t.Fatalf("File(pkg/calc.go) reason = %q, want a match", reason)
	}
	if got := df.NewLines[11]; got != "\treturn a + b" {
		t.Fatalf("NewLines[11] = %q, want the unquoted line text", got)
	}
	assertQuoteOutcome(t, idx, evidence.QuoteRequest{
		Path: "pkg/calc.go", Line: 11, Quote: "\treturn a + b",
	}, "verified", "")
}
