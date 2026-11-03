package recordcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// The keep-biased anchor partition doctrine lives in the core merge: these
// legs moved here with the engine (slice 16) so post keeps only its
// payload/guard tests.

// anchorTestState builds a merge state over the given hunk rows for
// partition table tests.
func anchorTestState(rows []hunkRow) *state {
	return &state{anchorRows: rows}
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
	rows := []hunkRow{{file: "notes.txt", side: "NEW", start: 1, end: 3}}
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

// checkAnchorPartition classifies one comment and asserts its side.
func checkAnchorPartition(t *testing.T, rows []hunkRow, tc anchorCase) {
	t.Helper()
	st := anchorTestState(rows)
	st.classifyComment(anchorComment(tc.path, tc.line, "", tc.side), rows)
	if tc.kept && len(st.kept) != 1 {
		t.Errorf("kept=%v: kept=%d moved=%d", tc.kept, len(st.kept), len(st.moved))
	}
	if !tc.kept && len(st.moved) != 1 {
		t.Errorf("kept=%v: kept=%d moved=%d", tc.kept, len(st.kept), len(st.moved))
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
	rows := []hunkRow{{file: "notes.txt", side: "NEW", start: 1, end: 3}}
	st := anchorTestState(rows)
	c := anchorComment("notes.txt", "1", "3", "RIGHT")
	st.classifyComment(c, rows)
	if len(st.kept) != 1 {
		t.Errorf("swapped span 3-1 inside 1-3: kept=%d moved=%d", len(st.kept), len(st.moved))
	}
}

// TestAnchorLeftOldSide proves LEFT anchors validate against the OLD side:
// a line inside an OLD hunk keeps, the same line with no OLD hunk moves.
func TestAnchorLeftOldSide(t *testing.T) {
	t.Parallel()
	old := []hunkRow{{file: "notes.txt", side: "OLD", start: 1, end: 3}}
	st := anchorTestState(old)
	st.classifyComment(anchorComment("notes.txt", "2", "", "LEFT"), old)
	if len(st.kept) != 1 {
		t.Errorf("LEFT inside OLD hunk: kept=%d moved=%d", len(st.kept), len(st.moved))
	}
	st2 := anchorTestState(nil)
	st2.classifyComment(anchorComment("notes.txt", "2", "", "LEFT"), nil)
	if len(st2.moved) != 1 {
		t.Errorf("LEFT with unknown path moves to the body: kept=%d moved=%d", len(st2.kept), len(st2.moved))
	}
}
