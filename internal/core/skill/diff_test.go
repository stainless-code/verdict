package skill

import (
	"strings"
	"testing"
)

// TestUnifiedDiffReplacesOneLine pins the hunk header and body.
func TestUnifiedDiffReplacesOneLine(t *testing.T) {
	t.Parallel()
	got, truncated := unifiedDiff("SKILL.md", []byte("alpha\nbeta\ngamma\n"), []byte("alpha\nBETA\ngamma\n"), DiffCapBytes)
	want := "--- a/SKILL.md\n+++ b/SKILL.md\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	if got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
	if truncated {
		t.Fatal("a one-line replacement reported truncated")
	}
}

// TestUnifiedDiffPureInsertHeader pins the empty-old header convention.
func TestUnifiedDiffPureInsertHeader(t *testing.T) {
	t.Parallel()
	got, _ := unifiedDiff("x", nil, []byte("a\nb\n"), DiffCapBytes)
	want := "--- a/x\n+++ b/x\n@@ -0,0 +1,2 @@\n+a\n+b\n"
	if got != want {
		t.Fatalf("diff =\n%s\nwant\n%s", got, want)
	}
}

// TestUnifiedDiffTruncates pins the cap and its explicit marker.
func TestUnifiedDiffTruncates(t *testing.T) {
	t.Parallel()
	got, truncated := unifiedDiff("x", nil, []byte("a\nb\nc\n"), 20)
	if !truncated {
		t.Fatal("tiny limit did not report truncated")
	}
	if !strings.HasSuffix(got, truncatedMarker) {
		t.Fatalf("truncated diff does not end with the marker: %q", got)
	}
	if strings.Contains(got, "+c\n") {
		t.Fatalf("truncated diff kept a line past the limit: %q", got)
	}
}
