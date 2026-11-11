package backtest

import "testing"

// FuzzParseFixHunks proves arbitrary `git show -U0` bytes cannot panic the
// parser, that any deleted line it emits is positionable, and that one path's
// old-line numbers strictly increase in emission order.
func FuzzParseFixHunks(f *testing.F) {
	for _, seed := range []string{
		"",
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n",
		"diff --git a/new.go b/new.go\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1 @@\n+added\n",
		"diff --git a/x.bin b/x.bin\nBinary files a/x.bin and b/x.bin differ\n",
		"@@ -0,0 +1 @@\n-still deleted\n",
		"diff --git a/multi.go b/multi.go\n--- a/multi.go\n+++ b/multi.go\n" +
			"@@ -2 +2 @@\n-old\n+new\n@@ -10 +10 @@\n-older\n+newer\n",
		"diff --git a/crlf.txt b/crlf.txt\n--- a/crlf.txt\n+++ b/crlf.txt\n" +
			"@@ -1 +1 @@\n-old\r\n+new\r\n",
		"diff --git \"a/odd name.go\" \"b/odd name.go\"\n--- \"a/odd name.go\"\n" +
			"+++ \"b/odd name.go\"\n@@ -1 +1 @@\n-old\n+new\n",
		"diff --git a/eof.txt b/eof.txt\n--- a/eof.txt\n+++ b/eof.txt\n" +
			"@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n",
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -5 +5 @@\n-first\n" +
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-second\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, diff string) {
		checkParsedHunks(t, parseFixHunks(diff))
	})
}

// checkParsedHunks asserts the parser's placement invariant: every emitted
// deleted line has a path and a positive line, and each path's lines increase.
func checkParsedHunks(t *testing.T, got fixDiff) {
	t.Helper()
	last := map[string]int{}
	for i, line := range got.Deleted {
		checkDeletedLine(t, i, line, last)
	}
}

// checkDeletedLine checks one emitted line and records its position.
func checkDeletedLine(t *testing.T, i int, line diffLine, last map[string]int) {
	t.Helper()
	if line.Path == "" {
		t.Fatalf("deleted[%d] has an empty path", i)
	}
	if line.Line <= 0 {
		t.Fatalf("deleted[%d] line = %d; want > 0", i, line.Line)
	}
	if prev, ok := last[line.Path]; ok && line.Line <= prev {
		t.Fatalf("deleted[%d] %s:%d does not follow %d; want strictly increasing per path",
			i, line.Path, line.Line, prev)
	}
	last[line.Path] = line.Line
}
