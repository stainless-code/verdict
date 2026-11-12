package indexcore

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The fixture corpus this file grades: one small repository per language under
// testdata/corpus, the hand-labeled edges checked in beside each, and the
// floors pinned from the last measurement.
const (
	corpusRoot   = "testdata/corpus"
	corpusLabels = "expected.json"
	corpusFloors = corpusRoot + "/floors.golden"
	// corpusSuffix marks a fixture source the repository's own gates cannot
	// build: the Go fixture is a module of its own, and the pre-commit hook
	// vets the directory of every staged .go file. The seeder strips it.
	corpusSuffix  = ".txt"
	corpusGo      = "go"
	floorUnpinned = "n/a"
	minNameWidth  = 5
)

// corpusLanguages are the fixture repositories, in scorecard order.
var corpusLanguages = []string{corpusGo, "ts", "py"}

var updateFloors = flag.Bool("update", false, "re-pin the index accuracy floors")

// fixtureCorpus is one checked-in repository's hand labels.
type fixtureCorpus struct {
	Language string       `json:"language"`
	Queries  []corpusCase `json:"queries"`
	Coverage corpusCover  `json:"coverage"`
}

// corpusCover is the coverage the fixture's build must report; the skipped
// count is the bail-out total, which only Go can raise.
type corpusCover struct {
	Files   int `json:"files"`
	Skipped int `json:"skipped"`
}

// corpusCase is one query and the edges a correct index returns for it.
type corpusCase struct {
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	Symbol     string       `json:"symbol"`
	Path       string       `json:"path"`
	At         string       `json:"at"`
	Callers    []callerEdge `json:"callers"`
	Tests      []testEdge   `json:"tests"`
	Hidden     int          `json:"hidden"`
	Unresolved int          `json:"unresolved"`
	Depth      int          `json:"depth"`
}

// callerEdge is one hand-labeled caller site: where it is, what it does, and
// how exactly the index can know it. A same-package Go site is exact; a
// cross-package site and every scanned-language site is lexical.
type callerEdge struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Resolution string `json:"resolution"`
	Line       int    `json:"line"`
}

// testEdge is one hand-labeled candidate test, its mechanism, and the import
// hops the walk must report.
type testEdge struct {
	Path  string `json:"path"`
	Via   string `json:"via"`
	Depth int    `json:"depth"`
}

// tally is one query's confusion matrix: the edges that matched, the rows
// reported, and the edges labeled.
type tally struct {
	tp       int
	reported int
	expected int
}

// add folds one query's tally into a pooled one.
func (t *tally) add(other tally) {
	t.tp += other.tp
	t.reported += other.reported
	t.expected += other.expected
}

// scoreCard is one printed row: a query or a pool, and its tally.
type scoreCard struct {
	Name  string
	Tally tally
}

// caseScore is one query's score in both scopes, and whether it queried
// callers.
type caseScore struct {
	Name    string
	All     tally
	Cross   tally
	callers bool
}

// rate is one precision or recall figure, unset when the denominator is zero.
type rate struct {
	value float64
	set   bool
}

// TestCorpusAccuracy scores the index against the hand-labeled fixture corpus
// and fails when any rate drops below its pinned floor. The scorecard prints
// only measured numbers over fixtures: it is not a real-world rate.
func TestCorpusAccuracy(t *testing.T) {
	t.Parallel()
	var rows []scoreCard
	for _, lang := range corpusLanguages {
		rows = append(rows, runCorpus(t, lang)...)
	}
	t.Logf("index accuracy scorecard (fixture corpus, not a real-world rate):\n%s", renderScorecard(rows))
	assertFloors(t, rows)
	assertDocsScorecard(t, rows)
}

// runCorpus seeds one labeled repository into a scratch git repository, builds
// it, and scores every query plus the language's pooled rows.
func runCorpus(t *testing.T, lang string) []scoreCard {
	t.Helper()
	dir := filepath.Join(corpusRoot, lang)
	c := loadCorpus(t, dir)
	f := newFixture(t)
	files := seedCorpus(t, f, dir)
	f.commit("corpus")
	built := f.build()
	assertCoverage(t, lang, built.Data, c.Coverage)
	scores := make([]caseScore, 0, len(c.Queries))
	for i := range c.Queries {
		q := &c.Queries[i]
		assertLabelledPaths(t, q, files)
		scores = append(scores, assess(t, f, q))
	}
	return languageRows(lang, scores)
}

// loadCorpus reads one repository's labels, refusing a label file that names no
// query, mixes kinds, or lists one edge twice.
func loadCorpus(t *testing.T, dir string) *fixtureCorpus {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, corpusLabels))
	if err != nil {
		t.Fatalf("read corpus labels: %v", err)
	}
	var c fixtureCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s: %v", filepath.Join(dir, corpusLabels), err)
	}
	if len(c.Queries) == 0 {
		t.Fatalf("%s: no labeled queries", dir)
	}
	for i := range c.Queries {
		assertLabelled(t, dir, &c.Queries[i])
	}
	return &c
}

// assertLabelled refuses one query whose labels cannot be scored as written.
func assertLabelled(t *testing.T, dir string, q *corpusCase) {
	t.Helper()
	switch q.Kind {
	case QueryCallers:
		if q.Symbol == "" || q.At == "" || len(q.Callers) == 0 {
			t.Fatalf("%s: query %q wants a symbol, its declaring package, and its edges", dir, q.Name)
		}
	case QueryTestsFor:
		if (q.Path == "") == (q.Symbol == "") || len(q.Tests) == 0 {
			t.Fatalf("%s: query %q wants exactly one of path and symbol, and its rows", dir, q.Name)
		}
	default:
		t.Fatalf("%s: query %q has kind %q; want callers or tests-for", dir, q.Name, q.Kind)
	}
	assertUniqueEdges(t, dir, q)
}

// assertUniqueEdges refuses a query that labels one edge twice: comparison is
// a set, so a repeat would shrink recall without saying so.
func assertUniqueEdges(t *testing.T, dir string, q *corpusCase) {
	t.Helper()
	seen := make(map[string]bool, len(q.Callers)+len(q.Tests))
	for _, e := range q.Callers {
		assertFreshEdge(t, dir, q.Name, edgeKey(e.Path, e.Line, e.Kind, e.Resolution), seen)
	}
	for _, e := range q.Tests {
		assertFreshEdge(t, dir, q.Name, testKey(e.Path, e.Via, e.Depth), seen)
	}
}

// assertFreshEdge records one labeled edge key, refusing a repeat.
func assertFreshEdge(t *testing.T, dir, name, key string, seen map[string]bool) {
	t.Helper()
	if seen[key] {
		t.Fatalf("%s: query %q labels %s twice", dir, name, key)
	}
	seen[key] = true
}

// seedCorpus writes one labeled repository into the fixture, returning the
// paths it holds. The label file itself stays out: it is not part of the code
// under test. A source under corpusSuffix is seeded without it.
func seedCorpus(t *testing.T, f *fixture, dir string) map[string]bool {
	t.Helper()
	s := &seeder{fixture: f, root: dir, files: map[string]bool{}}
	if err := filepath.WalkDir(dir, s.visit); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
	return s.files
}

// seeder copies one corpus directory into a fixture.
type seeder struct {
	fixture *fixture
	files   map[string]bool
	root    string
}

// visit copies one corpus file, skipping directories and the label file.
func (s *seeder) visit(p string, d os.DirEntry, err error) error {
	if err != nil || d.IsDir() || d.Name() == corpusLabels {
		return err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(s.root, p)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.ToSlash(rel), corpusSuffix)
	s.fixture.write(name, string(body))
	s.files[name] = true
	return nil
}

// assertLabelledPaths refuses a label naming a path the fixture does not hold:
// a typo can never match, so it would deflate recall without saying why.
func assertLabelledPaths(t *testing.T, q *corpusCase, files map[string]bool) {
	t.Helper()
	for _, e := range q.Callers {
		assertHeld(t, q.Name, e.Path, files)
	}
	for _, e := range q.Tests {
		assertHeld(t, q.Name, e.Path, files)
	}
}

// assertHeld refuses one labeled path the fixture does not hold.
func assertHeld(t *testing.T, name, p string, files map[string]bool) {
	t.Helper()
	if !files[p] {
		t.Fatalf("%s: labels %s, which the fixture does not hold", name, p)
	}
}

// assertCoverage compares the build's indexed and skipped counts with the
// hand-written expectation.
func assertCoverage(t *testing.T, lang string, d *Data, want corpusCover) {
	t.Helper()
	got := corpusCover{Files: d.Files, Skipped: d.Skipped}
	if got != want {
		t.Fatalf("%s: coverage = %d indexed, %d skipped; want %d indexed, %d skipped",
			lang, got.Files, got.Skipped, want.Files, want.Skipped)
	}
}

// corpusQuery runs one labeled query against the built fixture.
func (f *fixture) corpusQuery(t *testing.T, q *corpusCase) *Data {
	t.Helper()
	req := f.request(q.Kind, nil)
	req.Symbol, req.Path, req.Depth = q.Symbol, q.Path, q.Depth
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("%s: index %s refused: %v", q.Name, q.Kind, ref)
	}
	return resp.Data
}

// assess scores one query and checks the counts it publishes beside its rows.
func assess(t *testing.T, f *fixture, q *corpusCase) caseScore {
	t.Helper()
	d := f.corpusQuery(t, q)
	if q.Kind == QueryTestsFor {
		if d.DepthTruncated != q.Hidden {
			t.Errorf("%s: depth_truncated = %d; want %d", q.Name, d.DepthTruncated, q.Hidden)
		}
		return caseScore{Name: q.Name, All: scoreTests(q, d.TestRows)}
	}
	if d.Unresolved != q.Unresolved {
		t.Errorf("%s: unresolved = %d; want %d", q.Name, d.Unresolved, q.Unresolved)
	}
	return caseScore{
		Name:    q.Name,
		All:     scoreCallers(q, d.Rows, scopeAll),
		Cross:   scoreCallers(q, d.Rows, scopeCrossPackage),
		callers: true,
	}
}

// scope selects the sites a caller score covers.
type scope int

const (
	scopeAll scope = iota
	scopeCrossPackage
)

// keep reports whether one site falls inside the scope, given the declaring
// package directory.
func (s scope) keep(p, at string) bool { return s == scopeAll || path.Dir(p) != at }

// scoreCallers compares the labeled caller sites with the reported rows.
func scoreCallers(q *corpusCase, rows []Row, s scope) tally {
	want := make(map[string]bool, len(q.Callers))
	for _, e := range q.Callers {
		if s.keep(e.Path, q.At) {
			want[edgeKey(e.Path, e.Line, e.Kind, e.Resolution)] = true
		}
	}
	got := make(map[string]bool, len(rows))
	for _, r := range rows {
		if s.keep(r.Path, q.At) {
			got[edgeKey(r.Path, r.Line, r.Kind, r.Resolution)] = true
		}
	}
	return score(want, got)
}

// scoreTests compares the labeled candidate tests with the reported rows.
func scoreTests(q *corpusCase, rows []TestRow) tally {
	want := make(map[string]bool, len(q.Tests))
	for _, e := range q.Tests {
		want[testKey(e.Path, e.Via, e.Depth)] = true
	}
	got := make(map[string]bool, len(rows))
	for _, r := range rows {
		got[testKey(r.Path, r.Via, r.Depth)] = true
	}
	return score(want, got)
}

// score counts a labeled set against a reported set.
func score(want, got map[string]bool) tally {
	out := tally{reported: len(got), expected: len(want)}
	for key := range got {
		if want[key] {
			out.tp++
		}
	}
	return out
}

// edgeKey is one caller site's identity: where it is, what it does, and how
// exactly the index can know it.
func edgeKey(p string, line int, kind, resolution string) string {
	return p + ":" + strconv.Itoa(line) + " " + kind + " " + resolution
}

// testKey is one candidate test's identity: the file, its mechanism, and the
// import hops.
func testKey(p, via string, depth int) string {
	return p + " " + via + " " + strconv.Itoa(depth)
}

// languageRows renders one query row each plus the language's pooled caller,
// cross-package caller, and tests-for rows.
func languageRows(lang string, scores []caseScore) []scoreCard {
	rows := make([]scoreCard, 0, len(scores)+3)
	var all, cross, tests tally
	for _, s := range scores {
		rows = append(rows, scoreCard{Name: lang + "/" + s.Name, Tally: s.All})
		if s.callers {
			all.add(s.All)
			cross.add(s.Cross)
			continue
		}
		tests.add(s.All)
	}
	return append(rows,
		scoreCard{Name: lang + "/callers", Tally: all},
		scoreCard{Name: lang + "/callers-cross-package", Tally: cross},
		scoreCard{Name: lang + "/tests-for", Tally: tests},
	)
}

// renderScorecard renders the measured table.
func renderScorecard(rows []scoreCard) string {
	name := minNameWidth
	for _, r := range rows {
		name = max(name, len(r.Name))
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%-*s %9s %8s %4s %9s %9s\n", name, "query", "precision", "recall", "tp", "reported", "labeled")
	for _, r := range rows {
		fmt.Fprintf(&out, "%-*s %9s %8s %4d %9d %9d\n", name, r.Name,
			ratio(r.Tally.tp, r.Tally.reported), ratio(r.Tally.tp, r.Tally.expected),
			r.Tally.tp, r.Tally.reported, r.Tally.expected)
	}
	return out.String()
}

// ratio renders one rate at four decimals, or the unpinned marker when the
// denominator is zero: nothing was labeled or nothing was reported.
func ratio(tp, denom int) string {
	if denom == 0 {
		return floorUnpinned
	}
	return strconv.FormatFloat(round4(float64(tp)/float64(denom)), 'f', 4, 64)
}

// round4 rounds one rate to the four decimals a filled floor holds.
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// renderFloors renders the scorecard as the pinned floor file: one line per
// row, the measured precision and recall, in scorecard order.
func renderFloors(rows []scoreCard) string {
	var out strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&out, "%s %s %s\n", r.Name,
			ratio(r.Tally.tp, r.Tally.reported), ratio(r.Tally.tp, r.Tally.expected))
	}
	return out.String()
}

// assertFloors compares every measured rate with its pinned floor, refusing a
// row with no floor and a floor with no row. Under -update it re-pins instead.
func assertFloors(t *testing.T, rows []scoreCard) {
	t.Helper()
	rendered := renderFloors(rows)
	if *updateFloors {
		repinFloors(t, rendered)
		return
	}
	raw, err := os.ReadFile(corpusFloors)
	if err != nil {
		t.Fatalf("missing floors %s (run with -update to pin them): %v", corpusFloors, err)
	}
	floors := parseFloors(t, string(raw))
	for _, r := range rows {
		floor, ok := floors[r.Name]
		if !ok {
			t.Errorf("%s: no pinned floor (run with -update to pin it)", r.Name)
			continue
		}
		checkFloor(t, r.Name+"/precision", ratio(r.Tally.tp, r.Tally.reported), floor.precision)
		checkFloor(t, r.Name+"/recall", ratio(r.Tally.tp, r.Tally.expected), floor.recall)
		delete(floors, r.Name)
	}
	stale := make([]string, 0, len(floors))
	for name := range floors {
		stale = append(stale, name)
	}
	slices.Sort(stale)
	for _, name := range stale {
		t.Errorf("%s: pinned floor names no scorecard row (run with -update to re-pin)", name)
	}
}

// floorRow is one line of the pinned floor file.
type floorRow struct {
	precision rate
	recall    rate
}

// parseFloors reads the pinned floors.
func parseFloors(t *testing.T, body string) map[string]floorRow {
	t.Helper()
	floors := map[string]floorRow{}
	for line := range strings.SplitSeq(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			t.Fatalf("floors: %q is not a name and two rates", line)
		}
		floors[fields[0]] = floorRow{precision: parseRate(t, fields[1]), recall: parseRate(t, fields[2])}
	}
	return floors
}

// parseRate reads one pinned rate.
func parseRate(t *testing.T, text string) rate {
	t.Helper()
	if text == floorUnpinned {
		return rate{}
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("floors: %q is not a rate: %v", text, err)
	}
	return rate{value: v, set: true}
}

// checkFloor fails one measured rate that dropped below its pinned floor.
func checkFloor(t *testing.T, name, got string, floor rate) {
	t.Helper()
	if !floor.set {
		return
	}
	if got == floorUnpinned {
		t.Errorf("%s = %s; pinned %s, and the row now reports nothing", name, got, pinnedText(floor))
		return
	}
	v, err := strconv.ParseFloat(got, 64)
	if err != nil {
		t.Fatalf("%s = %q is not a rate: %v", name, got, err)
	}
	if v < floor.value {
		t.Errorf("%s = %s; pinned floor %s", name, got, pinnedText(floor))
	}
}

// pinnedText renders one pinned rate.
func pinnedText(r rate) string { return strconv.FormatFloat(r.value, 'f', 4, 64) }

// repinFloors writes the measured scorecard as the floors and reports every
// line that moved.
func repinFloors(t *testing.T, rendered string) {
	t.Helper()
	old, err := os.ReadFile(corpusFloors)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpusFloors, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, line := range floorDiff(string(old), rendered) {
		t.Logf("floors: %s", line)
	}
	t.Logf("floors: pinned %s", corpusFloors)
}

// floorDiff renders the pinned lines that moved, old then new.
func floorDiff(oldBody, newBody string) []string {
	oldLines := lines(oldBody)
	newLines := lines(newBody)
	oldSet := make(map[string]bool, len(oldLines))
	for _, line := range oldLines {
		oldSet[line] = true
	}
	newSet := make(map[string]bool, len(newLines))
	var out []string
	for _, line := range newLines {
		newSet[line] = true
		if !oldSet[line] {
			out = append(out, "+ "+line)
		}
	}
	for _, line := range oldLines {
		if !newSet[line] {
			out = append(out, "- "+line)
		}
	}
	return out
}

// lines splits a rendered file, dropping the empty tail a trailing newline
// leaves and an absent file's single empty line.
func lines(body string) []string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
