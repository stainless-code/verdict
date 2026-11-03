package fetchcore

// Verbatim provider-bytes contract: the cafe full.diff join
// preserves patch bytes exactly — a trailing newline survives, a missing
// one is never added. A cross-implementation comparison could never be
// green on Linux (the other side gained one trailing newline under GNU
// tooling), so this unit test pins the Go side directly.
import "testing"

// TestCafeFullDiffVerbatim pins the cafe full.diff join against the
// provider's patch bytes exactly.
func TestCafeFullDiffVerbatim(t *testing.T) {
	t.Parallel()
	two := []any{
		map[string]any{"path": "a.txt", "patch": "@@ -1 +1 @@\n-a\n+b\n"},
		map[string]any{"path": "b.txt", "patch": "@@ -1 +1 @@\n-c\n+d"},
	}
	want := "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-a\n+b\n\n" +
		"diff --git a/b.txt b/b.txt\n@@ -1 +1 @@\n-c\n+d"
	if got := cafeFullDiff(two); got != want {
		t.Fatalf("multi-file join:\n%q\nwant:\n%q", got, want)
	}
	bare := []any{map[string]any{"path": "a.txt", "patch": "@@ -1 +1 @@\n-a\n+b"}}
	if got, want := cafeFullDiff(bare), "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-a\n+b"; got != want {
		t.Fatalf("no-newline patch gains bytes:\n%q\nwant:\n%q", got, want)
	}
}
