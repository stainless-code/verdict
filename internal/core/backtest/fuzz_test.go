package backtest

import "testing"

// FuzzParseFixHunks proves arbitrary `git show -U0` bytes cannot panic the
// parser, and that any deleted line it emits is positionable.
func FuzzParseFixHunks(f *testing.F) {
	for _, seed := range []string{
		"",
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n",
		"diff --git a/new.go b/new.go\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1 @@\n+added\n",
		"diff --git a/x.bin b/x.bin\nBinary files a/x.bin and b/x.bin differ\n",
		"@@ -0,0 +1 @@\n-still deleted\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, diff string) {
		checkParsedHunks(t, parseFixHunks(diff))
	})
}

// checkParsedHunks asserts the parser's placement invariant.
func checkParsedHunks(t *testing.T, got fixDiff) {
	t.Helper()
	for i, line := range got.Deleted {
		if line.Path == "" {
			t.Fatalf("deleted[%d] has an empty path", i)
		}
		if line.Line <= 0 {
			t.Fatalf("deleted[%d] line = %d; want > 0", i, line.Line)
		}
	}
}
