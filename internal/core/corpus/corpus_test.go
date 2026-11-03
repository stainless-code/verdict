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

// checkCase validates one case directory and each of its fake agents.
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
	for _, agent := range agents {
		checkAgent(t, filepath.Join(dir, "agents", agent.Name()), index)
	}
}

// agentOutput mirrors the checked-in fake agent fixture shape.
//
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

// checkAgent validates one fake agent fixture against the case diff.
func checkAgent(t *testing.T, path string, index []hunks.Hunk) {
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
	if len(a.Review.Comments) == 0 {
		t.Errorf("%s: no comments", path)
	}
	bodies := map[string]bool{}
	for _, c := range a.Review.Comments {
		checkComment(t, path, c.Path, c.Line, c.Body, bodies, index)
	}
}

// checkComment validates one fake finding's anchor and uniqueness.
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

// loadCase loads one checked-in case.json through the bundle convention.
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

// TestEvaluateMatching pins the one-to-one, path-plus-window rule.
//
//nolint:revive // the table is the rule matrix
func TestEvaluateMatching(t *testing.T) {
	tol := 3
	c := &core.CorpusCase{
		LineTolerance: &tol,
		Bugs: []core.CorpusBug{
			{Path: "a.go", Line: 10, Kind: "off-by-one", Severity: "critical"},
			{Path: "a.go", Line: 30, Kind: "nil-deref", Severity: "important"},
		},
	}
	tests := []struct {
		name    string
		entries []any
		matched int
		fp      int
		missed  int
	}{
		{
			name:    "exact hits",
			entries: []any{entry("a.go", 10), entry("a.go", 30)},
			matched: 2,
		},
		{
			name:    "window boundary in, one line past out",
			entries: []any{entry("a.go", 13), entry("a.go", 34)},
			matched: 1, fp: 1, missed: 1,
		},
		{
			name:    "duplicate on one bug is a false positive",
			entries: []any{entry("a.go", 10), entry("a.go", 11)},
			matched: 1, fp: 1, missed: 1,
		},
		{
			name:    "wrong path, absent line, null entry",
			entries: []any{entry("b.go", 10), jq.NewObj("path", "a.go"), nil},
			matched: 0, fp: 3, missed: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Evaluate(c, tc.entries)
			if rep.Matched != tc.matched || rep.FalsePositives != tc.fp || len(rep.Missed) != tc.missed {
				t.Fatalf("got matched=%d fp=%d missed=%d, want %d/%d/%d",
					rep.Matched, rep.FalsePositives, len(rep.Missed), tc.matched, tc.fp, tc.missed)
			}
			if rep.Reported != len(tc.entries) {
				t.Fatalf("reported = %d, want %d", rep.Reported, len(tc.entries))
			}
		})
	}
}

// entry builds one minimal findings entry.
func entry(path string, line int) *jq.Obj {
	return jq.NewObj("path", path, "line", line, "sig", "s")
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
