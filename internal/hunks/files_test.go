package hunks_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// TestFiles pins the one-scan section index every diff consumer reads.
func TestFiles(t *testing.T) {
	t.Parallel()
	files := hunks.Files(twoFileDiff)
	if len(files) != 2 {
		t.Fatalf("Files() = %d sections; want 2", len(files))
	}
	t.Run("modified section", func(t *testing.T) {
		t.Parallel()
		checkNotesSection(t, &files[0])
	})
	t.Run("deleted section", func(t *testing.T) {
		t.Parallel()
		checkGoneSection(t, &files[1])
	})
	t.Run("sizes cover every byte", func(t *testing.T) {
		t.Parallel()
		if total := files[0].Size + files[1].Size; total != int64(len(twoFileDiff)) {
			t.Errorf("sizes sum to %d; want %d", total, len(twoFileDiff))
		}
	})
}

// checkNotesSection pins the modified section: paths, stats, hunk counts,
// added lines, span, and toc line.
func checkNotesSection(t *testing.T, first *hunks.File) {
	t.Helper()
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"paths", first.OldPath + "/" + first.Path, "notes.txt/notes.txt"},
		{"stats", fmt.Sprintf("+%d/-%d", first.Additions, first.Deletions), "+1/-1"},
		{"hunk counts", fmt.Sprintf("%d/%d", first.HunkCount, first.HunkMarks), "1/1"},
		{"added lines", strings.Join(first.AddedLines, "|"), "new line"},
		{"span", fmt.Sprintf("%d-%d", first.StartLine, first.EndLine), "1-9"},
		{"toc", first.HeaderIndex(), "1:diff --git a/notes.txt b/notes.txt"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q; want %q", c.name, c.got, c.want)
		}
	}
}

// checkGoneSection pins the deleted section: stats and span.
func checkGoneSection(t *testing.T, second *hunks.File) {
	t.Helper()
	if second.Additions != 0 || second.Deletions != 2 {
		t.Errorf("second stats = +%d/-%d; want +0/-2", second.Additions, second.Deletions)
	}
	if second.StartLine != 10 || second.EndLine != 16 {
		t.Errorf("second span = %d-%d; want 10-16", second.StartLine, second.EndLine)
	}
}

// TestFilesQuotedHeader proves a C-quoted header keeps the decoded path, so
// a quoted file sorts and counts under the name git wrote.
func TestFilesQuotedHeader(t *testing.T) {
	t.Parallel()
	diff := "diff --git \"a/my old.txt\" \"b/my new.txt\"\n+++ b/my new.txt\n@@ -1 +1 @@\n-a\n+b\n"
	files := hunks.Files(diff)
	if len(files) != 1 || files[0].Path != "my new.txt" || files[0].OldPath != "my old.txt" {
		t.Fatalf("Files(quoted) = %+v; want b-side my new.txt", files)
	}
	if files[0].Hunks[0].File != "my new.txt" {
		t.Errorf("hunk rows name %q; want the decoded b-side", files[0].Hunks[0].File)
	}
}

// TestFilesEmpty proves empty and headerless diffs yield no sections.
func TestFilesEmpty(t *testing.T) {
	t.Parallel()
	for _, diff := range []string{"", "@@ -1 +1 @@\n-a\n+b\n", "\n"} {
		if files := hunks.Files(diff); len(files) != 0 {
			t.Errorf("Files(%q) = %+v; want none", diff, files)
		}
	}
}

// TestFilesRenameAttribution proves a `rename to` line renames the section's
// hunk rows, matching the awk index.
func TestFilesRenameAttribution(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/old.go b/new.go\nrename from old.go\nrename to newer.go\n@@ -1 +1 @@\n-a\n+b\n"
	files := hunks.Files(diff)
	if len(files) != 1 || files[0].Path != "new.go" {
		t.Fatalf("Files() = %+v; want the header b-side", files)
	}
	if got := files[0].Hunks[0].File; got != "newer.go" {
		t.Errorf("hunk row file = %q; want the renamed path", got)
	}
}

// TestFilesUnterminatedLine proves an unterminated last line still counts and
// keeps its byte size, matching splitLines plus one newline per line.
func TestFilesUnterminatedLine(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/x b/x\n+a"
	files := hunks.Files(diff)
	if len(files) != 1 || files[0].Additions != 1 {
		t.Fatalf("Files(unterminated) = %+v", files)
	}
	if files[0].Size != int64(len(diff))+1 {
		t.Errorf("size = %d; want %d (each line counts a newline)", files[0].Size, len(diff)+1)
	}
}

// TestParseFlattensTheFileIndex pins Parse's rows on the two-file fixture as
// literal text. An earlier version rebuilt the expectation by calling Files and
// flattening it — which is Parse's own body — so a scanner defect reproduced on
// both sides and the comparison could not fail.
func TestParseFlattensTheFileIndex(t *testing.T) {
	t.Parallel()
	const want = "notes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\ngone.txt\tOLD\t1\t2\n"
	if got := hunks.Format(hunks.Parse(twoFileDiff)); got != want {
		t.Errorf("Parse(twoFileDiff) = %q, want %q", got, want)
	}
}
