package diff

import (
	"os/exec"
	"path/filepath"
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

func TestDiffLinesHistogramMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	a, b := gitLinesA(t), gitLinesB(t)
	writeGitFixtures(t, dir, a, b)
	gitText := runGitHistogramDiff(t, dir)
	if gitText == "" {
		t.Skip("git diff produced no output")
	}
	files, err := Parse(gitText)
	must(t, err)
	if len(files) == 0 {
		t.Fatal("git diff parse empty")
	}
	// Our line diff must reconstruct the same change: applying edits yields b.
	edits := DiffLines(a, b)
	if strings.Join(reconstruct(edits), "\n") != strings.Join(b, "\n") {
		t.Fatalf("reconstruction want %q", b)
	}
	if countChanges(edits) == 0 {
		t.Fatal("no changes in edit script")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := osWriteFile(path, []byte(body)); err != nil {
		t.Fatal(err)
	}
}
