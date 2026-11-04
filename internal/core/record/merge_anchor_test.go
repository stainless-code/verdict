package recordcore

import (
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// The keep-biased anchor partition doctrine lives in the core merge: these
// legs moved here with the engine (slice 16) so post keeps only its
// payload/guard tests.

// anchorTestState builds an empty merge state for partition table tests;
// the hunk rows reach the partition through classifyComment.
func anchorTestState() *state {
	return &state{}
}

// anchorComment builds a minimal review comment object.
func anchorComment(path, line, start, side string) *jq.Obj {
	m := jq.NewObj(fieldPath, path, fieldLine, line, "side", side, fieldBody, "note body here")
	if start != "" {
		m.Set(fieldStartLine, start)
	}
	return m
}

// TestAnchorPartitionKeepBias proves the keep-biased doctrine: unknown paths
// move to the body with a warning (GitHub would reject the inline anchor);
// non-numeric anchors keep with a warning; only provably outside anchors
// move, and LEFT reads the OLD side.
func TestAnchorPartitionKeepBias(t *testing.T) {
	t.Parallel()
	rows := []hunks.Hunk{{File: "notes.txt", Side: "NEW", Start: 1, End: 3}}
	tests := []anchorCase{
		{"inline", "notes.txt", "2", "RIGHT", true},
		{"unknown-path-moves", "missing.txt", "2", "RIGHT", false},
		{"outside-moves", "notes.txt", "99", "RIGHT", false},
		{"non-numeric-keeps", "notes.txt", "abc", "RIGHT", true},
		{"missing-line-keeps", "notes.txt", "", "RIGHT", true},
		{"left-without-old-moves", "notes.txt", "2", "LEFT", false},
		{"side-defaults-right", "notes.txt", "2", "", true},
		{"prefix-stripped", "a/notes.txt", "2", "RIGHT", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAnchorPartition(t, rows, tc)
		})
	}
}

// checkAnchorPartition classifies one comment and asserts the exact
// partition: a kept anchor stays inline with nothing moved, and a moved
// anchor lands in the body only. Checking both lists catches a duplicate
// (kept and moved) that a one-sided count would miss.
func checkAnchorPartition(t *testing.T, rows []hunks.Hunk, tc anchorCase) {
	t.Helper()
	st := anchorTestState()
	st.classifyComment(anchorComment(tc.path, tc.line, "", tc.side), rows)
	wantKept, wantMoved := 1, 0
	if !tc.kept {
		wantKept, wantMoved = 0, 1
	}
	if len(st.kept) != wantKept || len(st.moved) != wantMoved {
		t.Errorf("kept=%v: kept=%d moved=%d, want kept=%d moved=%d",
			tc.kept, len(st.kept), len(st.moved), wantKept, wantMoved)
	}
}

// anchorCase is one keep-biased partition leg.
type anchorCase struct {
	name string
	path string
	line string
	side string
	kept bool
}

// TestAnchorSwappedSpan proves inverted spans normalize before the hunk
// lookup instead of failing the run.
func TestAnchorSwappedSpan(t *testing.T) {
	t.Parallel()
	rows := []hunks.Hunk{{File: "notes.txt", Side: "NEW", Start: 1, End: 3}}
	st := anchorTestState()
	c := anchorComment("notes.txt", "1", "3", "RIGHT")
	st.classifyComment(c, rows)
	if len(st.kept) != 1 || len(st.moved) != 0 {
		t.Errorf("swapped span 3-1 inside 1-3: kept=%d moved=%d", len(st.kept), len(st.moved))
	}
}

// TestAnchorOverflowKeepsWithWarning proves a digit string that does not fit
// in an int takes the non-numeric anchor path: the comment is kept inline and
// the exact warning is pinned, never a wrapped span. Leading zeros stay
// numeric, and a sign stays non-numeric.
func TestAnchorOverflowKeepsWithWarning(t *testing.T) {
	t.Parallel()
	rows := []hunks.Hunk{{File: "notes.txt", Side: "NEW", Start: 1, End: 3}}
	for _, line := range []string{
		"99999999999999999999",
		"9223372036854775808",
		"-3",
		"+3",
	} {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			checkAnchorWarning(t, rows, line)
		})
	}
	t.Run("leading-zeros", func(t *testing.T) {
		t.Parallel()
		checkAnchorKept(t, rows, "002")
	})
}

// checkAnchorWarning asserts a non-numeric anchor keeps with the exact
// warning, never a wrapped span.
func checkAnchorWarning(t *testing.T, rows []hunks.Hunk, line string) {
	t.Helper()
	st := anchorTestState()
	st.classifyComment(anchorComment("notes.txt", line, "", "RIGHT"), rows)
	if len(st.kept) != 1 || len(st.moved) != 0 {
		t.Fatalf("non-numeric anchor: kept=%d moved=%d", len(st.kept), len(st.moved))
	}
	want := "kept despite non-numeric anchor: notes.txt " + line + "-" + line
	if len(st.warnings) != 1 || st.warnings[0] != want {
		t.Fatalf("warnings = %q; want %q", st.warnings, want)
	}
}

// checkAnchorKept asserts a numeric anchor stays inline with no warning.
func checkAnchorKept(t *testing.T, rows []hunks.Hunk, line string) {
	t.Helper()
	st := anchorTestState()
	st.classifyComment(anchorComment("notes.txt", line, "", "RIGHT"), rows)
	if len(st.kept) != 1 || len(st.moved) != 0 || len(st.warnings) != 0 {
		t.Fatalf("numeric anchor: kept=%d moved=%d warnings=%q", len(st.kept), len(st.moved), st.warnings)
	}
}

// TestAnchorLeftOldSide proves LEFT anchors validate against the OLD side:
// a line inside an OLD hunk keeps, the same line with no OLD hunk moves.
func TestAnchorLeftOldSide(t *testing.T) {
	t.Parallel()
	old := []hunks.Hunk{{File: "notes.txt", Side: "OLD", Start: 1, End: 3}}
	st := anchorTestState()
	st.classifyComment(anchorComment("notes.txt", "2", "", "LEFT"), old)
	if len(st.kept) != 1 || len(st.moved) != 0 {
		t.Errorf("LEFT inside OLD hunk: kept=%d moved=%d", len(st.kept), len(st.moved))
	}
	st2 := anchorTestState()
	st2.classifyComment(anchorComment("notes.txt", "2", "", "LEFT"), nil)
	if len(st2.kept) != 0 || len(st2.moved) != 1 {
		t.Errorf("LEFT with unknown path moves to the body: kept=%d moved=%d", len(st2.kept), len(st2.moved))
	}
}

// TestAnchorClassifyAllocsFlatInHunks proves the merge converts the hunk
// index once per run: classifying one comment allocates the same bytes
// whatever the index size, instead of copying it twice per comment.
func TestAnchorClassifyAllocsFlatInHunks(t *testing.T) {
	t.Parallel()
	small := anchorClassifyBytes(t, 8)
	large := anchorClassifyBytes(t, 512)
	if large > small {
		t.Errorf("classifyComment allocates %d B/op at 512 hunks and %d B/op at 8; the index is still rebuilt per comment",
			large, small)
	}
}

// anchorClassifyBytes measures one comment classification against an index
// of n hunks, the last of which holds the comment.
func anchorClassifyBytes(t *testing.T, n int) uint64 {
	t.Helper()
	hs := make([]hunks.Hunk, 0, n)
	for i := range n - 1 {
		hs = append(hs, hunks.Hunk{File: fmt.Sprintf("f%05d.txt", i), Side: "NEW", Start: 1, End: 9})
	}
	hs = append(hs, hunks.Hunk{File: "notes.txt", Side: "NEW", Start: 1, End: 9})
	m := anchorComment("notes.txt", "2", "", "RIGHT")
	res := testing.Benchmark(func(b *testing.B) {
		b.Helper() // the closure is a measurement body, not a test (thelper)
		b.ReportAllocs()
		for b.Loop() {
			st := &state{}
			st.classifyComment(m, hs)
		}
	})
	return uint64(res.AllocedBytesPerOp())
}
