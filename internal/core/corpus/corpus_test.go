package corpuscore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestCheckedInCorpusCases validates every checked-in manifest and its fake
// agent outputs: ids match directories, anchors sit inside the diff hunks,
// and the review text stays unique per bundle (the sig join key).
func TestCheckedInCorpusCases(t *testing.T) {
	root := filepath.Join("..", "..", "..", "bench", "corpus")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read corpus root: %v", err)
	}
	seen := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		seen++
		checkCase(t, root, entry.Name())
	}
	if seen < 6 || seen > 10 {
		t.Errorf("checked-in corpus has %d cases, want 6-10", seen)
	}
}

func checkCase(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	c, ref := loadCase(t, filepath.Join(dir, "case.json"))
	if ref != nil {
		t.Fatalf("%s: %v", name, ref)
	}
	if c.ID != name {
		t.Errorf("%s: id = %q, want the directory name", name, c.ID)
	}
	diff, err := os.ReadFile(filepath.Join(dir, "full.diff"))
	if err != nil {
		t.Fatalf("%s: read full.diff: %v", name, err)
	}
	index := hunks.Parse(string(diff))
	agents, err := os.ReadDir(filepath.Join(dir, "agents"))
	if err != nil {
		t.Fatalf("%s: read agents: %v", name, err)
	}
	if len(agents) == 0 {
		t.Errorf("%s: no fake agent outputs", name)
	}
	var entries []any
	for _, agent := range agents {
		entries = append(entries, checkAgent(t, filepath.Join(dir, "agents", agent.Name()), index, c.Kind == "control")...)
	}
	checkTolerance(t, name, c, index)
	checkNamedBugs(t, name, c, entries)
}

// checkNamedBugs holds the corpus to its own rule: a fake output must name
// each ground-truth bug, or the case grades every model wrong.
func checkNamedBugs(t *testing.T, name string, c *core.CorpusCase, entries []any) {
	t.Helper()
	if rep := Evaluate(c, entries); rep.Matched != len(c.Bugs) {
		t.Errorf("%s: the fake outputs match %d of %d ground-truth bugs; add the words a reviewer would use to the bug's keywords",
			name, rep.Matched, len(c.Bugs))
	}
}

// checkTolerance holds the window narrower than the hunk that shows the bug. A
// tolerance as wide as its hunk grades any anchor in it as the same defect,
// which is the ±3-on-a-5-line-hunk collapse the corpus is meant to prevent.
// Every alternate anchor follows the same rule as the primary line.
func checkTolerance(t *testing.T, name string, c *core.CorpusCase, index []hunks.Hunk) {
	t.Helper()
	tol := Tolerance(c)
	for _, bug := range c.Bugs {
		checkBugAnchors(t, name, &bug, tol, index)
	}
}

// checkBugAnchors holds every anchor of one bug to the window rule.
func checkBugAnchors(t *testing.T, name string, bug *core.CorpusBug, tol int, index []hunks.Hunk) {
	t.Helper()
	for _, line := range bugAnchors(bug) {
		checkOneAnchor(t, name, bug, line, tol, index)
	}
}

// checkOneAnchor holds one anchor inside its hunk and short of the far end.
func checkOneAnchor(t *testing.T, name string, bug *core.CorpusBug, line, tol int, index []hunks.Hunk) {
	t.Helper()
	reach, ok := hunkReach(index, bug.Path, line)
	if !ok {
		t.Errorf("%s: bug %s:%d anchor %d is outside the reviewed hunks", name, bug.Path, bug.Line, line)
		return
	}
	if tol >= reach {
		t.Errorf("%s: bug %s:%d anchor %d is %d line(s) from its hunk's far end and line_tolerance is %d; keep the window short of the far end",
			name, bug.Path, bug.Line, line, reach, tol)
	}
}

// bugAnchors lists the primary line first, then each alternate anchor.
func bugAnchors(b *core.CorpusBug) []int {
	return append([]int{b.Line}, b.AlsoLines...)
}

// hunkReach is the distance from line to the farther end of the new-side hunk
// that declares it: a window this wide covers every line of the hunk.
func hunkReach(index []hunks.Hunk, path string, line int) (int, bool) {
	for _, h := range index {
		if h.Side == "NEW" && h.File == path && h.Start <= line && line <= h.End {
			return max(line-h.Start, h.End-line), true
		}
	}
	return 0, false
}

//nolint:govet // the field order mirrors the JSON fixture, not memory layout
type agentOutput struct {
	Model  string   `json:"model"`
	Panel  []string `json:"panel"`
	Review struct {
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Body string `json:"body"`
		} `json:"comments"`
	} `json:"review"`
}

// checkAgent validates one fake output and returns the findings it reports.
func checkAgent(t *testing.T, path string, index []hunks.Hunk, control bool) []any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var a agentOutput
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if a.Model == "" || len(a.Panel) == 0 {
		t.Errorf("%s: needs model and panel", path)
	}
	if len(a.Review.Comments) == 0 && !control {
		t.Errorf("%s: no comments", path)
	}
	bodies := map[string]bool{}
	var entries []any
	for _, c := range a.Review.Comments {
		checkComment(t, path, c.Path, c.Line, c.Body, bodies, index)
		entries = append(entries, jq.NewObj("path", c.Path, "line", c.Line, "body", c.Body))
	}
	return entries
}

func checkComment(t *testing.T, agent, path string, line int, body string, bodies map[string]bool, index []hunks.Hunk) {
	t.Helper()
	if path == "" || line < 1 || strings.TrimSpace(body) == "" {
		t.Errorf("%s: comment needs path, line, body", agent)
	}
	if bodies[body] {
		t.Errorf("%s: duplicate comment body %q (the sig would dedupe)", agent, body)
	}
	bodies[body] = true
	if !hunks.Contains(index, path, "NEW", line, line) {
		t.Errorf("%s: %s:%d is outside the reviewed hunks", agent, path, line)
	}
}

func loadCase(t *testing.T, path string) (*core.CorpusCase, *core.RefusalError) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "corpus.json"), raw, 0o600); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return Load(dir)
}

// TestEvaluateMatching pins the matching rule: same path, a line inside the
// window, and text that names the bug's defect.
//
//nolint:revive // the table is the rule matrix
func TestEvaluateMatching(t *testing.T) {
	tol := 1
	c := &core.CorpusCase{
		LineTolerance: &tol,
		Bugs: []core.CorpusBug{
			{Path: "a.go", Line: 10, Kind: "off-by-one", Severity: "critical"},
			{Path: "a.go", Line: 30, Kind: "nil-deref", Severity: "important", Keywords: []string{"nil pointer"}},
		},
	}
	tests := []struct {
		name     string
		entries  []any
		reasons  []string
		reported int
		matched  int
		fp       int
		missed   int
	}{
		{
			name:     "exact hits name the defect",
			entries:  []any{entry("a.go", 10, "off by one: the loop bound runs past the slice"), entry("a.go", 30, "nil pointer: parts is dereferenced")},
			reported: 2, matched: 2,
		},
		{
			name: "record's moved section is not the reviewer's finding",
			entries: []any{
				jq.NewObj("heading", core.MovedSectionHeading, "anchored", "body", "body", core.MovedSectionHeading),
				entry("a.go", 10, "off-by-one"),
			},
			reported: 1, matched: 1, missed: 1,
		},
		{
			name:     "window boundary in, one line past out",
			entries:  []any{entry("a.go", 11, "off by one"), entry("a.go", 32, "nil pointer")},
			reported: 2, matched: 1, fp: 1, missed: 1,
			reasons: []string{ReasonOffLine},
		},
		{
			name:     "right line, wrong defect",
			entries:  []any{entry("a.go", 10, "Consider renaming this variable")},
			reported: 1, fp: 1, missed: 2,
			reasons: []string{ReasonUnnamed},
		},
		{
			name:     "right defect, wrong path",
			entries:  []any{entry("b.go", 10, "off-by-one")},
			reported: 1, fp: 1, missed: 2,
			reasons: []string{ReasonOffLine},
		},
		{
			name:     "right defect, wrong line",
			entries:  []any{entry("a.go", 20, "off by one")},
			reported: 1, fp: 1, missed: 2,
			reasons: []string{ReasonOffLine},
		},
		{
			name:     "record moved an exact anchor to the body",
			entries:  []any{jq.NewObj("path", "a.go", "line", 10, "body", "off by one", "anchored", false)},
			reported: 1, fp: 1, missed: 2,
			reasons: []string{ReasonMovedToBody},
		},
		{
			name:     "kind alone names a bug that also has keywords",
			entries:  []any{entry("a.go", 10, "off-by-one"), entry("a.go", 30, "nil-deref")},
			reported: 2, matched: 2,
		},
		{
			name:     "two findings on one bug count once",
			entries:  []any{entry("a.go", 10, "off-by-one"), entry("a.go", 11, "off-by-one")},
			reported: 2, matched: 1, fp: 1, missed: 1,
			reasons: []string{ReasonAlreadyMatched},
		},
		{
			name:     "wrong path, absent line, null entry",
			entries:  []any{entry("b.go", 10, "race"), jq.NewObj("path", "a.go"), nil},
			reported: 3, fp: 3, missed: 2,
			reasons: []string{ReasonOffLine, ReasonUnanchored, ReasonUnanchored},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Evaluate(c, tc.entries)
			if rep.Reported != tc.reported || rep.Matched != tc.matched ||
				rep.FalsePositives != tc.fp || len(rep.Missed) != tc.missed {
				t.Fatalf("got reported=%d matched=%d fp=%d missed=%d, want %d/%d/%d/%d",
					rep.Reported, rep.Matched, rep.FalsePositives, len(rep.Missed),
					tc.reported, tc.matched, tc.fp, tc.missed)
			}
			if got := fpReasons(rep); !equalStrings(got, tc.reasons) {
				t.Fatalf("false-positive reasons = %v, want %v", got, tc.reasons)
			}
			if len(rep.Outcomes) != tc.reported {
				t.Fatalf("graded %d outcomes, reported %d", len(rep.Outcomes), rep.Reported)
			}
		})
	}
}

// TestDuplicateOnASharedWindowSaysAlreadyMatched keeps the reason honest when
// one window holds two kinds: a second finding naming the matched bug is a
// duplicate, not a finding that names the wrong defect.
func TestDuplicateOnASharedWindowSaysAlreadyMatched(t *testing.T) {
	c := &core.CorpusCase{Bugs: []core.CorpusBug{
		{Path: "a.go", Line: 10, Kind: "off-by-one", Severity: "critical"},
		{Path: "a.go", Line: 11, Kind: "missing-error-check", Severity: "important"},
	}}
	rep := Evaluate(c, []any{entry("a.go", 10, "off by one"), entry("a.go", 10, "off by one again")})
	if got := fpReasons(rep); !equalStrings(got, []string{ReasonAlreadyMatched}) {
		t.Fatalf("false-positive reasons = %v, want [%s]", got, ReasonAlreadyMatched)
	}
}

// TestZeroToleranceNeedsTheBugLine pins the exact-line mode a case opts into.
func TestZeroToleranceNeedsTheBugLine(t *testing.T) {
	zero := 0
	c := &core.CorpusCase{
		LineTolerance: &zero,
		Bugs:          []core.CorpusBug{{Path: "a.go", Line: 10, Kind: "off-by-one", Severity: "critical"}},
	}
	if rep := Evaluate(c, []any{entry("a.go", 11, "off by one")}); rep.Matched != 0 || rep.FalsePositives != 1 {
		t.Errorf("adjacent line with tolerance 0 = matched %d fp %d, want 0/1", rep.Matched, rep.FalsePositives)
	}
	if rep := Evaluate(c, []any{entry("a.go", 10, "off by one")}); rep.Matched != 1 {
		t.Errorf("bug line with tolerance 0 matched %d, want 1", rep.Matched)
	}
}

// TestEvaluateAlsoLines pins the alternate-anchor rule: each entry matches
// with the same tolerance and defect-naming rule as the primary line, and
// matching several anchors for one bug still counts that bug once with the
// primary line reported.
func TestEvaluateAlsoLines(t *testing.T) {
	c := alsoLinesCase()
	rep := Evaluate(c, []any{entry("files.go", 24, "confine name under root or reject .. segments")})
	checkAlsoMatch(t, rep)
	rep = Evaluate(c, []any{entry("files.go", 25, "confine name under root")})
	if rep.Matched != 1 {
		t.Fatalf("line inside the alternate window matched %d, want 1", rep.Matched)
	}
}

// TestEvaluateAlsoLinesNeedsTheDefect pins the naming rule on an alternate
// anchor: the right line with the wrong defect is still a false positive.
func TestEvaluateAlsoLinesNeedsTheDefect(t *testing.T) {
	rep := Evaluate(alsoLinesCase(), []any{entry("files.go", 24, "rename this helper for readability")})
	if rep.Matched != 0 {
		t.Fatalf("alternate anchor without the defect matched %d, want 0", rep.Matched)
	}
	if got := fpReasons(rep); !equalStrings(got, []string{ReasonUnnamed}) {
		t.Fatalf("false-positive reasons = %v, want [%s]", got, ReasonUnnamed)
	}
}

// TestEvaluateAlsoLinesCountsOneBugOnce pins the one-match-per-bug rule:
// findings on two anchors of the same bug grade as one hit and one duplicate.
func TestEvaluateAlsoLinesCountsOneBugOnce(t *testing.T) {
	rep := Evaluate(alsoLinesCase(), []any{
		entry("files.go", 28, "join without confining lets ../ escape"),
		entry("files.go", 24, "join without confining lets ../ escape"),
	})
	if rep.Matched != 1 || rep.FalsePositives != 1 {
		t.Fatalf("two anchors on one bug = matched %d fp %d, want 1/1", rep.Matched, rep.FalsePositives)
	}
	if got := fpReasons(rep); !equalStrings(got, []string{ReasonAlreadyMatched}) {
		t.Fatalf("false-positive reasons = %v, want [%s]", got, ReasonAlreadyMatched)
	}
}

// checkAlsoMatch pins the alternate hit: one match reporting the primary line.
func checkAlsoMatch(t *testing.T, rep *Report) {
	t.Helper()
	if rep.Matched != 1 {
		t.Fatalf("alternate anchor matched %d, want 1", rep.Matched)
	}
	if rep.Outcomes[0].BugLine != 28 {
		t.Fatalf("alternate anchor reported bug line %d, want the primary 28", rep.Outcomes[0].BugLine)
	}
}

// alsoLinesCase is the shared single-bug case the alternate-anchor tests grade.
func alsoLinesCase() *core.CorpusCase {
	tol := 1
	return &core.CorpusCase{
		LineTolerance: &tol,
		Bugs: []core.CorpusBug{{
			Path: "files.go", Line: 28, Kind: "path-traversal",
			Severity: "critical", AlsoLines: []int{24},
		}},
	}
}

func fpReasons(rep *Report) []string {
	var out []string
	for i := range rep.Outcomes {
		if rep.Outcomes[i].Result == ResultFalsePositive {
			out = append(out, rep.Outcomes[i].Reason)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func entry(path string, line int, body string) *jq.Obj {
	return jq.NewObj("path", path, "line", line, "body", body, "sig", "s")
}

// TestCategoryVocabulary pins the words that name each published category
// against a sentence a reviewer writes for it, and rejects a claim that lands
// on the line but talks about something else.
func TestCategoryVocabulary(t *testing.T) {
	tests := []struct {
		name string
		bug  core.CorpusBug
		text string
		want bool
	}{
		{"auth bypass", core.CorpusBug{Kind: "auth-bypass"}, "The role check is inverted, so any caller bypasses the admin gate.", true},
		{"missing error check", core.CorpusBug{Kind: "missing-error-check"}, "The io.Copy error is discarded, so a short write reads as success.", true},
		{"nil deref", core.CorpusBug{Kind: "nil-deref"}, "The session is dereferenced before the lookup error is checked.", true},
		{"off by one", core.CorpusBug{Kind: "off-by-one"}, "The loop bound runs one past the slice.", true},
		{"index out of range", core.CorpusBug{Kind: "index-out-of-range"}, "Check len(parts) before indexing parts[1].", true},
		{"path traversal", core.CorpusBug{Kind: "path-traversal"}, "Clean and confine name under root before joining.", true},
		{"race", core.CorpusBug{Kind: "race"}, "Wait for the close before returning the conn to the pool.", true},
		{"injection", core.CorpusBug{Kind: "injection"}, "Pass name as a query argument instead of formatting the SQL.", true},
		{"kind text", core.CorpusBug{Kind: "off-by-one"}, "This is an off by one in the bound.", true},
		{"case keyword", core.CorpusBug{Kind: "custom-kind", Keywords: []string{"cache key"}}, "Two requests share a cache key.", true},
		{"wrong defect", core.CorpusBug{Kind: "off-by-one"}, "Consider renaming this loop variable.", false},
		{"glued word", core.CorpusBug{Kind: "race"}, "This brace style is inconsistent.", false},
		{"generic auth word", core.CorpusBug{Kind: "auth-bypass"}, "Rename the admin variable to match its role.", false},
		{"generic error word", core.CorpusBug{Kind: "missing-error-check"}, "Fix the error message spelling.", false},
		{"generic nil word", core.CorpusBug{Kind: "nil-deref"}, "Return nil instead of an empty slice here.", false},
		{"generic index word", core.CorpusBug{Kind: "index-out-of-range"}, "Rename index to i for brevity.", false},
		{"unknown kind", core.CorpusBug{Kind: "custom-kind"}, "The loop reads one past the slice.", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := namesDefect(normalize(tc.text), &tc.bug); got != tc.want {
				t.Errorf("namesDefect(%q, %s) = %v, want %v", tc.text, tc.bug.Kind, got, tc.want)
			}
		})
	}
}

// TestTolerance pins the window default and the manifest's say.
func TestTolerance(t *testing.T) {
	if DefaultLineTolerance != 1 {
		t.Fatalf("DefaultLineTolerance = %d, want 1: an unpinned case must not let an anchor drift across its hunk", DefaultLineTolerance)
	}
	if got := Tolerance(&core.CorpusCase{}); got != DefaultLineTolerance {
		t.Errorf("Tolerance(no pin) = %d, want %d", got, DefaultLineTolerance)
	}
	tight, wide := 0, 4
	if got := Tolerance(&core.CorpusCase{LineTolerance: &tight}); got != 0 {
		t.Errorf("Tolerance(0) = %d, want the exact-line window", got)
	}
	if got := Tolerance(&core.CorpusCase{LineTolerance: &wide}); got != 4 {
		t.Errorf("Tolerance(4) = %d, want 4", got)
	}
}

// TestRate pins four-decimal rounding and the unmeasured null.
func TestRate(t *testing.T) {
	if got, ok := Rate(2, 3); !ok || got != 0.6667 {
		t.Errorf("Rate(2,3) = %v, %v; want 0.6667", got, ok)
	}
	if got, ok := Rate(0, 0); ok || got != 0 {
		t.Errorf("Rate(0,0) = %v, %v; want the unmeasured zero,false", got, ok)
	}
	if got := RateOrNil(0, 0); got != nil {
		t.Errorf("RateOrNil(0,0) = %v, want nil", got)
	}
}

// TestLoadRejects proves the semantic gates the schema cannot express.
//
//nolint:revive // the table is the rejection matrix
func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing schema version", `{"id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":1,"kind":"k","severity":"critical"}]}`},
		{"real without fix commit", `{"schema_version":"1","id":"x","kind":"real","provenance":{"kind":"real","repo":"o/r","pr":1},"bugs":[{"path":"a","line":1,"kind":"k","severity":"critical"}]}`},
		{"seeded without injection", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded"},"bugs":[{"path":"a","line":1,"kind":"k","severity":"critical"}]}`},
		{"no bugs", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[]}`},
		{"unknown field", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":1,"kind":"k","severity":"critical"}],"typo":true}`},
		{"blank keyword", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":1,"kind":"k","severity":"critical","keywords":[" "]}]}`},
		{"also_lines below line 1", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":10,"kind":"k","severity":"critical","also_lines":[0]}]}`},
		{"also_lines repeats the primary line", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":10,"kind":"k","severity":"critical","also_lines":[10]}]}`},
		{"also_lines repeats an entry", `{"schema_version":"1","id":"x","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[{"path":"a","line":10,"kind":"k","severity":"critical","also_lines":[8,8]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "corpus.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ref := Load(dir); ref == nil || ref.Code != codeCorpusInvalid {
				t.Fatalf("Load = %v, want corpus-invalid", ref)
			}
		})
	}
}
