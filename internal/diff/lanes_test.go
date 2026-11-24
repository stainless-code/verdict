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
	// The production call site must use the heuristic: DiffLines must equal
	// the heuristic script. Flipping linesDiff to false inside DiffLines
	// would make this fail (mutation proof).
	prod := DiffLines(a, b)
	if !reflect.DeepEqual(prod, withHeuristic) {
		t.Fatalf("DiffLines = %v want heuristic %v (was heuristic disabled?)", prod, withHeuristic)
	}
	if reflect.DeepEqual(prod, without) {
		t.Fatalf("DiffLines equals no-heuristic %v; heuristic was disabled in prod", prod)
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
	// Indentation-only edits are whitespace; see TestClassifyWhitespaceIndentation
	// for the collapsed-run rule. This case pins the baseline.
	f := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x := 1"},
		{Kind: KindAdd, Text: "    x := 1"},
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
	// The word pass must have tokenized: "hello" unchanged, world/there changed.
	if !unchangedSpan(oldM[0], "hello") || !changedSpan(oldM[0], "world") {
		t.Fatalf("word pass not tokenizing old=%v", oldM[0])
	}
}

func unchangedSpan(spans []WordSpan, text string) bool {
	for _, s := range spans {
		if s.Text == text {
			return !s.Changed
		}
	}
	return false
}

func changedSpan(spans []WordSpan, text string) bool {
	for _, s := range spans {
		if s.Text == text {
			return s.Changed
		}
	}
	return false
}

func TestPairWordSpansNotAcrossContext(t *testing.T) {
	lines := []Line{
		{Kind: KindDel, Text: "alpha one"},
		{Kind: KindContext, Text: "separator", OldNo: 1, NewNo: 1},
		{Kind: KindAdd, Text: "beta two"},
	}
	oldM, newM := PairWordSpans(lines)
	// Neither line may be paired: a del in one block and an add in the next
	// block are separate. The implementation star-unpairs each side, which
	// yields a single whole-line span (not a token comparison).
	if _, ok := oldM[0]; !ok {
		t.Fatal("del line expected at least an unpaired span")
	}
	if _, ok := newM[2]; !ok {
		t.Fatal("add line expected at least an unpaired span")
	}
	delSpans := oldM[0]
	addSpans := newM[2]
	// Token pairing would split "alpha one" into "alpha"+" "+"one" spans;
	// star-unpaired is a single whole-line span.
	if len(delSpans) != 1 || delSpans[0].Text != "alpha one" {
		t.Fatalf("del spans = %v, want single unpaired span", delSpans)
	}
	if len(addSpans) != 1 || addSpans[0].Text != "beta two" {
		t.Fatalf("add spans = %v, want single unpaired span", addSpans)
	}
	// No pairing means "alpha" is not retained as an unchanged token.
	if unchangedSpan(delSpans, "alpha") {
		t.Fatalf("cross-boundary pairing kept alpha: %v", delSpans)
	}
}

func TestClassifyConfigBasesCaseInsensitive(t *testing.T) {
	cases := []struct {
		path string
		want Lane
	}{
		{"Makefile", LaneConfig},
		{"makefile", LaneConfig},
		{"Dockerfile", LaneConfig},
		{"dockerfile", LaneConfig},
		{"Dockerfile.dev", LaneConfig},
		{"cmd/Makefile", LaneConfig},
		{"cmd\\Makefile", LaneConfig},
		{"DOCKERFILE.PROD", LaneConfig},
	}
	for _, c := range cases {
		f := FileDiff{NewPath: c.path}
		if got := Classify(&f); got != c.want {
			t.Errorf("path %q: got %s want %s", c.path, got, c.want)
		}
	}
}

func TestClassifyWhitespaceTokenMergeStaysSource(t *testing.T) {
	// The old stripSpace deleted all whitespace, so `msg := "a b"` ->
	// `msg := "ab"` landed in whitespace. The collapsed-run fix keeps it
	// in source.
	f := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: `msg := "a b"`},
		{Kind: KindAdd, Text: `msg := "ab"`},
	}}}}
	if got := Classify(&f); got != LaneSource {
		t.Fatalf("token merge got %s want source", got)
	}
}

func TestClassifyWhitespaceIndentation(t *testing.T) {
	// Indentation-only changes stay whitespace.
	f := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x := 1"},
		{Kind: KindAdd, Text: "    x := 1"},
	}}}}
	if got := Classify(&f); got != LaneWhitespace {
		t.Fatalf("indent-only got %s want whitespace", got)
	}
	f2 := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x  :=  1"},
		{Kind: KindAdd, Text: "x := 1"},
	}}}}
	if got := Classify(&f2); got != LaneWhitespace {
		t.Fatalf("multi-space got %s want whitespace", got)
	}
	f3 := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x := 1"},
		{Kind: KindAdd, Text: "x := 1 "},
	}}}}
	if got := Classify(&f3); got != LaneWhitespace {
		t.Fatalf("trailing-space got %s want whitespace", got)
	}
	// But a real token change is source.
	f4 := FileDiff{NewPath: "a.go", Hunks: []Hunk{{Lines: []Line{
		{Kind: KindDel, Text: "x := 1"},
		{Kind: KindAdd, Text: "x := 2"},
	}}}}
	if got := Classify(&f4); got != LaneSource {
		t.Fatalf("real change got %s want source", got)
	}
}

func TestClassifyWindowsPaths(t *testing.T) {
	cases := []struct {
		path string
		want Lane
	}{
		{`internal\foo.go`, LaneSource},
		{`docs\guide.md`, LaneDocs},
		{`migrations\001.sql`, LaneSchema},
		{`foo_test.go`, LaneTests},
		{`.github/workflows/ci.yaml`, LaneConfig},
	}
	for _, c := range cases {
		f := FileDiff{NewPath: c.path}
		if got := Classify(&f); got != c.want {
			t.Errorf("windows path %q: got %s want %s", c.path, got, c.want)
		}
	}
}
