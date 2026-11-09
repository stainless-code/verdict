package hunks_test

import (
	"slices"
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

// TestParseRangeOverflow proves a degenerate @@ range that does not fit in an
// int coerces to zero, never a wrapped negative span, and that awk's no-sign
// coercion still reads a signed range as zero.
func TestParseRangeOverflow(t *testing.T) {
	t.Parallel()
	for _, diff := range []string{
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -99999999999999999999,1 +1,1 @@\n-a\n+b\n",
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ --5,1 +1,1 @@\n-a\n+b\n",
	} {
		t.Run(diff, func(t *testing.T) {
			t.Parallel()
			checkDegenerateRange(t, diff)
		})
	}
}

// checkDegenerateRange asserts one degenerate header coerces to zero.
func checkDegenerateRange(t *testing.T, diff string) {
	t.Helper()
	got := hunks.Parse(diff)
	want := []hunks.Hunk{
		{File: "x", Side: "OLD", Start: 0, End: 0},
		{File: "x", Side: "NEW", Start: 1, End: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse(%q) = %v; want %v", diff, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Parse(%q)[%d] = %+v; want %+v", diff, i, got[i], want[i])
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

// TestFormatRoundTrip checks Format and ParseTSV are inverses. Counting rows
// alone — the earlier version — could not see a swapped side, a duplicated
// start, or a dropped field, all of which keep one row per hunk.
func TestFormatRoundTrip(t *testing.T) {
	hs := hunks.Parse(twoFileDiff)
	if back := hunks.ParseTSV(hunks.Format(hs)); !slices.Equal(back, hs) {
		t.Errorf("ParseTSV(Format(Parse(diff))) = %v; want %v", back, hs)
	}
	if got := hunks.Format(nil); got != "" {
		t.Errorf("Format(nil) = %q; want empty", got)
	}
}

// TestParseHostilePathsCannotForgeTSVRows pins that a path git C-quotes
// because it carries a tab, newline or carriage return never reaches
// hunks.tsv: the restored control byte would shift the field split or open a
// forged row (`x\nNEW\t1\t99999` reads back as two hunks for file "x"). Found
// by FuzzParse. A clean file in the same diff keeps its rows.
//
// Break killed: removing the ContainsAny guard in hunkRows (the "tab", "nl"
// and "cr" rows then produce rows whose fields shift or multiply).
func TestParseHostilePathsCannotForgeTSVRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string // git's C-quoted spelling
	}{
		{"tab", `x\ty`},
		{"nl", `x\n9\tNEW\t1\t99999`},
		{"cr", `x\ry`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diff := "diff --git \"a/" + tc.path + "\" \"b/" + tc.path + "\"\n@@ -1 +1 @@\n-a\n+b\n" +
				"diff --git a/clean b/clean\n@@ -2 +2 @@\n-a\n+b\n"
			if got, want := hunks.Format(hunks.Parse(diff)), "clean\tOLD\t2\t2\nclean\tNEW\t2\t2\n"; got != want {
				t.Fatalf("hunks.tsv = %q, want %q", got, want)
			}
		})
	}
}

// TestParseSpanEndSaturates pins that a span whose end does not fit in an int
// clamps to the largest int instead of wrapping negative (an inverted hunk
// would make every anchor test against that file wrongly "outside").
//
// Break killed: replacing spanEnd's body with `start + count - 1` yields
// End = -9223372036854775801 for the first row.
func TestParseSpanEndSaturates(t *testing.T) {
	t.Parallel()
	const maxInt = 9223372036854775807
	diff := "diff --git a/x b/x\n@@ -1,2 +9223372036854775807,9 @@\n"
	want := []hunks.Hunk{
		{File: "x", Side: "OLD", Start: 1, End: 2},
		{File: "x", Side: "NEW", Start: maxInt, End: maxInt},
	}
	got := hunks.Parse(diff)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
}
