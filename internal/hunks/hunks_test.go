package hunks_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

const twoFileDiff = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
diff --git a/gone.txt b/gone.txt
index 3333333..4444444 100644
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-del one
-del two
`

// TestParse checks the diff parser against a two-file fixture.
func TestParse(t *testing.T) {
	got := hunks.Parse(twoFileDiff)
	want := []hunks.Hunk{
		{File: "notes.txt", Side: "OLD", Start: 1, End: 3},
		{File: "notes.txt", Side: "NEW", Start: 1, End: 3},
		{File: "gone.txt", Side: "OLD", Start: 1, End: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Parse()[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestKeepBiasedPartition checks keep-biased anchor partitioning.
func TestKeepBiasedPartition(t *testing.T) {
	hs := hunks.Parse(twoFileDiff)
	tests := []partitionCase{
		{"in-new", "notes.txt", "NEW", 1, 3, true, true},
		{"left-outside", "notes.txt", "OLD", 4, 4, true, false},
		{"empty-range", "gone.txt", "NEW", 1, 1, true, false},
		{"unknown-path", "nope.txt", "NEW", 1, 1, false, false},
		{"stripped-prefix", "a/notes.txt", "NEW", 2, 2, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()
			checkPartition(t, hs, tt)
		})
	}
}

// partitionCase is one keep-biased partition expectation.
type partitionCase struct {
	name       string
	file, side string
	start, end int
	indexed    bool
	inside     bool
}

// checkPartition checks one keep-biased partition case.
func checkPartition(t *testing.T, hs []hunks.Hunk, tt partitionCase) {
	t.Helper()
	if got := hunks.Indexed(hs, tt.file); got != tt.indexed {
		t.Errorf("Indexed(%q) = %v; want %v", tt.file, got, tt.indexed)
	}
	if got := hunks.Contains(hs, tt.file, tt.side, tt.start, tt.end); got != tt.inside {
		t.Errorf("Contains(%q %s %d-%d) = %v; want %v", tt.file, tt.side, tt.start, tt.end, got, tt.inside)
	}
}

// TestFormatRoundTrip checks hunk formatting survives a parse cycle.
func TestFormatRoundTrip(t *testing.T) {
	hs := hunks.Parse(twoFileDiff)
	tsv := hunks.Format(hs)
	rows := 0
	for _, ln := range splitLines(tsv) {
		if ln != "" {
			rows++
		}
	}
	if rows != 3 {
		t.Errorf("Format() has %d rows; want 3", rows)
	}
	if got := hunks.Format(nil); got != "" {
		t.Errorf("Format(nil) = %q; want empty", got)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
