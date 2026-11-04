package hunks_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// TestParseZeroCountSideSkipsRow pins the awk port's zero-count rule: a side
// whose count is 0 contributes no row at all, so a pure addition (a new file,
// `@@ -0,0 +1,3 @@`) indexes 1-3 on the NEW side and nothing on the OLD side.
// A row invented for the empty side would carry End = -1 and make every anchor
// on that file read as outside its hunk.
//
// Kill: hunks.go `if oldCount > 0` -> `>=` (the OLD row reappears as 0..-1).
func TestParseZeroCountSideSkipsRow(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/fresh.txt b/fresh.txt\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/fresh.txt\n" +
		"@@ -0,0 +1,3 @@\n" +
		"+one\n+two\n+three\n"
	got := hunks.Parse(diff)
	want := []hunks.Hunk{{File: "fresh.txt", Side: "NEW", Start: 1, End: 3}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Parse(new file) = %+v; want %+v", got, want)
	}
}

// TestParseHeaderFieldGate pins the field-count gate: a header carrying the
// OLD range as field 2 and the NEW range as field 3 indexes normally even
// without a trailing `@@` context marker, and a header short of the NEW range
// indexes nothing. Both readings are the keep-biased default (an unindexed
// path keeps its anchors inline with a warning).
//
// Kill: hunks.go `len(f) < minHeaderFields` -> `<=` (the 3-field header stops
// indexing); its negation (`>=`) makes the 2-field header index a bogus range.
func TestParseHeaderFieldGate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want []hunks.Hunk
	}{
		{
			name: "three fields index both sides",
			line: "@@ -4,2 +7,3",
			want: []hunks.Hunk{
				{File: "x", Side: "OLD", Start: 4, End: 5},
				{File: "x", Side: "NEW", Start: 7, End: 9},
			},
		},
		{
			name: "two fields index nothing",
			line: "@@ -4,2",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertParseHunks(t, "diff --git a/x b/x\n"+tc.line+"\n-a\n+b\n", tc.want)
		})
	}
}

// assertParseHunks checks Parse's rows against want, in order and in number.
func assertParseHunks(t *testing.T, diff string, want []hunks.Hunk) {
	t.Helper()
	got := hunks.Parse(diff)
	if len(got) != len(want) {
		t.Fatalf("Parse = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Parse[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestParseSpanEndStaysExactBelowSaturation pins the last span that still fits
// in an int: a hunk starting MaxInt-count must end MaxInt-1, not MaxInt. The
// saturated clamp is only for spans that would overflow, so widening it by one
// silently extends a real hunk over the file's last line.
//
// Kill: hunks.go spanEnd's overflow bound (start > [math.MaxInt]-count+1) moved
// one lower (end reads 9223372036854775807 instead of 9223372036854775806).
func TestParseSpanEndStaysExactBelowSaturation(t *testing.T) {
	t.Parallel()
	// 9223372036854775798 = MaxInt-9, so start+count-1 = MaxInt-1 exactly.
	diff := "diff --git a/x b/x\n@@ -1,2 +9223372036854775798,9 @@\n"
	got := hunks.Parse(diff)
	want := hunks.Hunk{File: "x", Side: "NEW", Start: 9223372036854775798, End: 9223372036854775806}
	if len(got) != 2 || got[1] != want {
		t.Fatalf("Parse = %+v; want the NEW row %+v", got, want)
	}
}

// TestFilesCountsCombinedDiffMarks pins the two hunk counters apart on a
// diff3 combined header (`@@@ -1,3 -1,3 +1,3 @@@`, what a conflicted merge
// emits): the overview column counts only `@@ ` headers, while the coverage
// total counts every `@@`-prefixed line. Overwriting the counter instead of
// incrementing hides those marks from the file totals.
//
// Kill: files.go `f.HunkMarks++` in the `@@` case -> `--`.
func TestFilesCountsCombinedDiffMarks(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/m.txt b/m.txt\n" +
		"--- a/m.txt\n" +
		"+++ b/m.txt\n" +
		"@@@ -1,3 -1,3 +1,3 @@@\n" +
		"+merged\n"
	files := hunks.Files(diff)
	if len(files) != 1 {
		t.Fatalf("Files(combined) = %+v; want one section", files)
	}
	if files[0].HunkCount != 0 || files[0].HunkMarks != 1 {
		t.Errorf("counts = %d/%d; want 0 overview headers and 1 hunk mark",
			files[0].HunkCount, files[0].HunkMarks)
	}
	if len(files[0].Hunks) != 0 {
		t.Errorf("combined header produced rows %+v; want none", files[0].Hunks)
	}
}
