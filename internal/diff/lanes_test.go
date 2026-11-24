package diff

import (
	"os"
	"reflect"
	"testing"
)

func osWriteFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o600)
}

func changedWord(spans []WordSpan, text string) bool {
	for i := range spans {
		if spans[i].Text == text && spans[i].Changed {
			return true
		}
	}
	return false
}

func checkUnchanged(t *testing.T, spans []WordSpan, text string) {
	t.Helper()
	for i := range spans {
		if spans[i].Text == text && spans[i].Changed {
			t.Error("unchanged word marked changed")
		}
	}
}

func TestWordSpansHighlightsChanged(t *testing.T) {
	oldS, newS := WordSpans("hello world", "hello there")
	if len(oldS) == 0 || len(newS) == 0 {
		t.Fatal("empty spans")
	}
	checkUnchanged(t, oldS, "hello")
	if !changedWord(oldS, "world") || !changedWord(newS, "there") {
		t.Fatalf("word pass missed change old=%v new=%v", oldS, newS)
	}
}

func reconstructWithHeuristic(edits []Edit) []string {
	var got []string
	for i := range edits {
		if edits[i].Kind == EditEqual || edits[i].Kind == EditInsert {
			got = append(got, edits[i].Lines...)
		}
	}
	return got
}

func TestIndentationHeuristicSlides(t *testing.T) {
	// A blank-line insertion is ambiguous: it may sit before or after the
	// existing blank. The indent heuristic slides it before the existing
	// blank so the hunk aligns to the blank boundary (git's behavior).
	a := []string{"}", "", "func b() {}"}
	b := []string{"}", "", "", "func b() {}"}
	withHeuristic := LinesWithHeuristic(a, b, true)
	without := LinesWithHeuristic(a, b, false)
	if reflect.DeepEqual(withHeuristic, without) {
		t.Fatalf("heuristic did not change the script with=%v without=%v", withHeuristic, without)
	}
	// The heuristic position (insert before the existing blank) differs
	// from the raw position (insert after it).
	if insertIndex(withHeuristic) == insertIndex(without) {
		t.Fatalf("heuristic did not move the insert with=%v without=%v", withHeuristic, without)
	}
	// The heuristic result must still reconstruct.
	if !reflect.DeepEqual(reconstructWithHeuristic(withHeuristic), b) {
		t.Fatalf("heuristic reconstruction want %q", b)
	}
}

func insertIndex(edits []Edit) int {
	pos := 0
	for i := range edits {
		if edits[i].Kind == EditInsert {
			return pos
		}
		pos += len(edits[i].Lines)
	}
	return -1
}

type laneCase struct { //nolint:govet // the case table mirrors the production struct
	name string
	file FileDiff
	want Lane
}

func TestClassifyLanes(t *testing.T) {
	cases := []laneCase{
		{"pure rename", FileDiff{OldPath: "a.go", NewPath: "b.go", IsRename: true}, LaneRenamed},
		{"generated marker", FileDiff{NewPath: "x.go", Generated: true}, LaneGenerated},
		{"lockfile", FileDiff{NewPath: "go.sum"}, LaneLockfiles},
		{"schema sql", FileDiff{NewPath: "migrations/001.sql"}, LaneSchema},
		{"test go", FileDiff{NewPath: "foo_test.go"}, LaneTests},
		{"docs md", FileDiff{NewPath: "docs/guide.md"}, LaneDocs},
		{"config yaml", FileDiff{NewPath: ".github/workflows/ci.yaml"}, LaneConfig},
		{"source go", FileDiff{NewPath: "internal/x.go"}, LaneSource},
	}
	for i := range cases {
		if got := Classify(&cases[i].file); got != cases[i].want {
			t.Errorf("%s: got %s want %s", cases[i].name, got, cases[i].want)
		}
	}
}

func TestClassifyWhitespaceOnly(t *testing.T) {
	f := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x := 1"},
		{Kind: KindAdd, Text: "x:=1"},
	}}}}
	if got := Classify(&f); got != LaneWhitespace {
		t.Fatalf("whitespace-only got %s", got)
	}
}

func TestGroupByLaneOrder(t *testing.T) {
	files := []FileDiff{
		{NewPath: "docs/a.md"},
		{NewPath: "migrations/001.sql"},
		{NewPath: "foo_test.go"},
	}
	got := GroupByLane(files)
	if len(got) != 3 {
		t.Fatalf("lanes = %v", got)
	}
	if got[0].Lane != LaneSchema {
		t.Fatalf("first lane = %s want schema", got[0].Lane)
	}
	if got[1].Lane != LaneTests {
		t.Fatalf("second lane = %s want tests", got[1].Lane)
	}
	if got[2].Lane != LaneDocs {
		t.Fatalf("third lane = %s want docs", got[2].Lane)
	}
}

func TestPairWordSpansBounded(t *testing.T) {
	lines := []Line{
		{Kind: KindDel, Text: "hello world"},
		{Kind: KindAdd, Text: "hello there"},
	}
	oldM, newM := PairWordSpans(lines)
	if len(oldM) != 1 || len(newM) != 1 {
		t.Fatalf("paired spans = %d/%d", len(oldM), len(newM))
	}
}
