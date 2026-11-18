package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
)

func repoPath(parts ...string) string {
	parts = append([]string{"..", "..", ".."}, parts...)
	return filepath.Join(parts...)
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The hand column is copied from calibration.md, which transcribed the hand
// scores in RUN.md. It is never computed from the scorer under test.
func TestCalibrationReproduced(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	runs := calibrationRuns(t, catalog)
	result := Calibrate(catalog, runs)
	if !result.Reproduced {
		t.Errorf("calibration not reproduced")
	}
	want := []calibrationExpectation{
		{"vr-sta440b-bench", "0/5", "0/5", "-", "-", 3, 3},
		{"vr-sta440b-bench2", "0/5", "0/5", "-", "-", 3, 3},
		{"vr-sta440b-bench3", "1/5", "1/5", "K2", "K2", 0, 0},
		{"vr-sta440b-haiku", "5/5", "5/5", "K1,K2,K3,K4,K5", "K1,K2,K3,K4,K5", 0, 0},
		{"vr-sta440b-haiku2", "1/5", "1/5", "K5", "K5", 0, 4},
		{"vr-sta440b-sonnet", "1/5", "1/5", "K5", "K5", 0, 4},
		{"vr-sta351a-haiku", "1/6", "1/6", "D2", "D2", 0, 0},
	}
	assertCalibrationRows(t, result, want)
	golden.Check(t, "fixtures-calibration", CalibrationTable(result))
}

type calibrationExpectation struct {
	recording, hand, harness, handHits, harnessHits string
	praise, nullSeverity                            int
}

func assertCalibrationRows(t *testing.T, result *CalibrationResult, want []calibrationExpectation) {
	t.Helper()
	if len(result.Rows) != len(want) {
		t.Fatalf("calibration rows = %d, want %d", len(result.Rows), len(want))
	}
	for i := range want {
		w := &want[i]
		got := &result.Rows[i]
		if got.Recording != w.recording || got.Hand != w.hand || got.Harness != w.harness ||
			got.HandHits != w.handHits || got.HarnessHits != w.harnessHits ||
			got.Praise != w.praise || got.NullSeverity != w.nullSeverity || got.Status != statusPass {
			t.Errorf("row %d = %+v, want %+v", i, got, w)
		}
	}
}

// The hand column above is copied from calibration.md, which transcribed the
// hand scores in RUN.md. It is never computed from the scorer under test.
func calibrationRuns(t *testing.T, catalog *Catalog) []RunScore {
	t.Helper()
	matcher := NewMatcher(catalog)
	runs := make([]RunScore, 0, len(catalog.Calibration))
	for i := range catalog.Calibration {
		row := &catalog.Calibration[i]
		dir := repoPath("bench", "quality", "recordings", row.Recording)
		meta, ref := LoadRecordingMeta(dir)
		if ref != nil {
			t.Fatalf("load meta %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		findings, ref := LoadArchivedReview(filepath.Join(dir, reviewName))
		if ref != nil {
			t.Fatalf("load review %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		fixture := fixtureFor(t, catalog, row.Fixture)
		runs = append(runs, *Score(matcher, fixture, findings, meta, row.Recording))
	}
	return runs
}

func fixtureFor(t *testing.T, catalog *Catalog, id string) *Fixture {
	t.Helper()
	for i := range catalog.Fixtures {
		if catalog.Fixtures[i].ID == id {
			return &catalog.Fixtures[i]
		}
	}
	t.Fatalf("unknown fixture %s", id)
	return nil
}

func TestCatalogPinsContextBytes(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	for i := range catalog.Fixtures {
		for j := range catalog.Fixtures[i].Context {
			assertContextPin(t, &catalog.Fixtures[i].Context[j])
		}
	}
}

func assertContextPin(t *testing.T, c *ContextFile) {
	t.Helper()
	raw, err := os.ReadFile(repoPath(filepath.FromSlash(c.Path)))
	if err != nil {
		t.Fatalf("read %s: %v", c.Path, err)
	}
	if len(raw) != c.Bytes {
		t.Errorf("%s bytes = %d, pin says %d", c.Path, len(raw), c.Bytes)
	}
	digest := sha256.Sum256(raw)
	if got := hex.EncodeToString(digest[:]); got != c.SHA256 {
		t.Errorf("%s sha256 = %s, pin says %s", c.Path, got, c.SHA256)
	}
}

func TestCatalogRejectsBrokenPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), catalogName)
	writeTestFile(t, path, string(brokenCatalog(t)))
	_, ref := LoadCatalog(path)
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
}

func brokenCatalog(t *testing.T) []byte {
	t.Helper()
	doc := catalogDoc(t)
	fixtures, ok := doc["fixtures"].([]any)
	if !ok || len(fixtures) == 0 {
		t.Fatal("catalog has no fixtures array")
	}
	first, ok := fixtures[0].(map[string]any)
	if !ok {
		t.Fatal("first fixture is not an object")
	}
	contexts, ok := first["context"].([]any)
	if !ok || len(contexts) == 0 {
		t.Fatal("first fixture has no context array")
	}
	entry := firstContextEntry(t, contexts)
	entry["sha256"] = "not-a-digest"
	broken, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return broken
}

func catalogDoc(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(repoPath("bench", "quality", catalogName))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func firstContextEntry(t *testing.T, contexts []any) map[string]any {
	t.Helper()
	entry, ok := contexts[0].(map[string]any)
	if !ok {
		t.Fatal("first context pin is not an object")
	}
	return entry
}

func smallCatalog(t *testing.T) *Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), catalogName)
	writeTestFile(t, path, `{
  "schema_version": "1",
  "fixtures": [{
    "id": "fx", "label": "literal", "repo": "r",
    "base": "1111111111111111111111111111111111111111",
    "head": "2222222222222222222222222222222222222222",
    "context": [],
    "known": [
      {"id": "A", "description": "literal A", "matchers": [["alpha", "beta"]]},
      {"id": "B", "description": "literal B", "matchers": [["gamma"], ["delta", "epsilon"]]}
    ],
    "bonus": []
  }],
  "calibration": [{"recording": "r1", "fixture": "fx", "driver": "literal", "hand": [], "hand_label": "0/2"}],
  "review_signals": {"praise_re": "great", "defect_verb_re": "wrong|lacks"}
}`)
	c, ref := LoadCatalog(path)
	if ref != nil {
		t.Fatalf("load literal catalog: %s: %s", ref.Code, ref.Message)
	}
	return c
}

type matcherCase struct {
	finding Finding
	name    string
	wantA   bool
	wantB   bool
}

func TestMatcherAllOfWithinRuleAnyOfAcrossRules(t *testing.T) {
	c := smallCatalog(t)
	m := NewMatcher(c)
	a, b := c.Fixtures[0].Known[0], c.Fixtures[0].Known[1]
	cases := []matcherCase{
		{Finding{Path: "a.go", Claim: "alpha BETA here"}, "both words in one rule", true, false},
		{Finding{Path: "a.go", Claim: "alpha alone"}, "first half only", false, false},
		{Finding{Path: "b.go", Claim: "beta alone"}, "second half only", false, false},
		{Finding{Path: "gamma.go", Claim: "unrelated"}, "path participates", false, true},
		{Finding{Path: "x.go", Claim: "DELTA epsilon"}, "second single rule", false, true},
		{Finding{Path: "x.go", Claim: "delta only"}, "second compound half", false, false},
		{Finding{Path: "x.go", Claim: "plain text"}, "neither", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkMatcherCase(t, m, &a, &b, &tc) })
	}
}

func checkMatcherCase(t *testing.T, m *Matcher, a, b *Defect, tc *matcherCase) {
	t.Helper()
	if got := m.Matches(&tc.finding, a.Matchers); got != tc.wantA {
		t.Errorf("A = %v, want %v", got, tc.wantA)
	}
	if got := m.Matches(&tc.finding, b.Matchers); got != tc.wantB {
		t.Errorf("B = %v, want %v", got, tc.wantB)
	}
}

// The post-K5 audit found six findings that legitimately name two related
// defects. Each literal below must hit both; the tightened K5 must not hit a
// probe-receipt finding that only says a match is clashing.
type crossCase struct {
	claim string
	ids   []string
}

func TestCrossHitAuditLiteral(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	m := NewMatcher(catalog)
	defects := allDefects(catalog)
	cases := []crossCase{
		{"kode and neovate both lack their print flags, -p and -q", []string{"K1", "K2"}},
		{"qoder and qodercn both need -p for headless launches", []string{"K3", "K4"}},
		{"neovate needs -q and qoder needs -p; both are non-interactive", []string{"K2", "K3"}},
		{"repair probe launcher has no Probe field and the launcher is broken", []string{"B1"}},
		{"parent context cancellation is mislabeled as agent-mismatch", []string{"B2"}},
		{"ProbeMatch argsRun uses a bare substring and a false match", []string{"K5"}},
	}
	for _, tc := range cases {
		assertCrossHits(t, m, &defects, &tc)
	}
	assertK5Tightened(t, m, &defects)
}

func allDefects(catalog *Catalog) map[string]Defect {
	defects := map[string]Defect{}
	for i := range catalog.Fixtures {
		for j := range catalog.Fixtures[i].Known {
			d := &catalog.Fixtures[i].Known[j]
			defects[d.ID] = *d
		}
		for j := range catalog.Fixtures[i].Bonus {
			d := &catalog.Fixtures[i].Bonus[j]
			defects[d.ID] = *d
		}
	}
	return defects
}

func assertK5Tightened(t *testing.T, m *Matcher, defects *map[string]Defect) {
	t.Helper()
	clashing := Finding{Path: "launcher.json", Claim: "probe identity clashing with another tool"}
	if m.Matches(&clashing, (*defects)["K5"].Matchers) {
		t.Error("old K5 matcher still credits a clashing probe receipt")
	}
	repair := Finding{Path: "internal/core/run/repair.go", Claim: "the repair launcher has no probe field"}
	if !m.Matches(&repair, (*defects)["B1"].Matchers) {
		t.Error("B1 regressed: repair/probe launcher claim not matched")
	}
}

func assertCrossHits(t *testing.T, m *Matcher, defects *map[string]Defect, tc *crossCase) {
	t.Helper()
	f := Finding{Path: "internal/core/run/agents.go", Claim: tc.claim}
	for _, id := range tc.ids {
		if !m.Hit([]Finding{f}, (*defects)[id]) {
			t.Errorf("claim %q missed %s", tc.claim, id)
		}
	}
}

func TestArchivedReviewAliases(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, reviewName), `{
  "schema_version": "1",
  "comments": [
    {"path": "chosen.go", "body": "chosen claim", "severity": "important"},
    {"file": "fallback.go", "text": "fallback claim"},
    {"path": "preferred.go", "file": "ignored.go", "body": "preferred claim", "text": "ignored claim"}
  ]
}`)
	findings, ref := LoadArchivedReview(filepath.Join(dir, reviewName))
	if ref != nil {
		t.Fatalf("load: %s: %s", ref.Code, ref.Message)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(findings))
	}
	want := []Finding{
		{Path: "chosen.go", Claim: "chosen claim", Severity: "important"},
		{Path: "fallback.go", Claim: "fallback claim"},
		{Path: "preferred.go", Claim: "preferred claim"},
	}
	for i, w := range want {
		if findings[i] != w {
			t.Errorf("finding %d = %+v, want %+v", i, findings[i], w)
		}
	}
}

func TestRecordingMetaGateUnknownIsNotPass(t *testing.T) {
	dir := repoPath("bench", "quality", "recordings", "vr-sta440b-bench")
	meta, ref := LoadRecordingMeta(dir)
	if ref != nil {
		t.Fatalf("load meta: %s: %s", ref.Code, ref.Message)
	}
	if meta.GatePassed != nil {
		t.Errorf("gate_passed = %v, want nil", *meta.GatePassed)
	}
	bad := filepath.Join(t.TempDir(), metaName)
	writeTestFile(t, bad, `{"schema_version":"1","fixture":"fx1","source":"elsewhere.json"}`)
	if _, ref := LoadRecordingMeta(filepath.Dir(bad)); ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
}
