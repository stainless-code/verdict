package hunks_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// TestSectionsRoundTrip pins the one property chunked bundles rely on: the
// preamble plus every section, in order, is the input byte for byte — including
// a last line with no trailing newline and a preamble before the first header.
// A splitter that drops the newline separator or the leading bytes loses hunks
// silently, and the record-time drift check would then blame the fetch.
//
// Kill: sections.go `strings.SplitAfterSeq(diffText, newline)` -> `Split`, or
// the `if !open` preamble branch removed (leading bytes vanish).
func TestSectionsRoundTrip(t *testing.T) {
	t.Parallel()
	diff := "warning: something\n" +
		"diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/b.go b/b.go\n" +
		"--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-c\n+d"
	tests := []struct {
		name     string
		diff     string
		preamble string
		want     []string
	}{
		{
			name:     "preamble and two sections",
			diff:     diff,
			preamble: "warning: something\n",
			want: []string{
				"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n",
				"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-c\n+d",
			},
		},
		{name: "empty", diff: ""},
		{name: "no header at all", diff: "plain text\n", preamble: "plain text\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertDiffSections(t, tc.diff, tc.preamble, tc.want)
		})
	}
}

// assertDiffSections checks one split against its literal expectation and
// proves the split reassembles the input.
func assertDiffSections(t *testing.T, diff, wantPreamble string, wantSections []string) {
	t.Helper()
	preamble, sections := hunks.Sections(diff)
	if preamble != wantPreamble {
		t.Errorf("preamble = %q, want %q", preamble, wantPreamble)
	}
	if !slices.Equal(sections, wantSections) {
		t.Fatalf("sections = %q, want %q", sections, wantSections)
	}
	if joined := preamble + strings.Join(sections, ""); joined != diff {
		t.Errorf("preamble + sections = %q, want the input %q", joined, diff)
	}
}
