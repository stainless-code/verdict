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
	matcher := NewMatcher(catalog)
	runs := make([]RunScore, 0, len(catalog.Calibration))
	for _, row := range catalog.Calibration {
		dir := repoPath("bench", "quality", "recordings", row.Recording)
		meta, ref := LoadRecordingMeta(dir)
		if ref != nil {
			t.Fatalf("load meta %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		findings, ref := LoadArchivedReview(filepath.Join(dir, reviewName))
		if ref != nil {
			t.Fatalf("load review %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		var fixture *Fixture
		for i := range catalog.Fixtures {
			if catalog.Fixtures[i].ID == row.Fixture {
				fixture = &catalog.Fixtures[i]
			}
		}
		if fixture == nil {
			t.Fatalf("unknown fixture %s", row.Fixture)
		}
		runs = append(runs, *Score(matcher, fixture, findings, *meta, row.Recording))
	}
	result := Calibrate(catalog, runs)
	if !result.Reproduced {
		t.Errorf("calibration not reproduced")
	}
	want := []struct {
		recording, hand, harness, handHits, harnessHits string
		praise, nullSeverity                            int
	}{
		{"vr-sta440b-bench", "0/5", "0/5", "-", "-", 3, 3},
		{"vr-sta440b-bench2", "0/5", "0/5", "-", "-", 3, 3},
		{"vr-sta440b-bench3", "1/5", "1/5", "K2", "K2", 0, 0},
		{"vr-sta440b-haiku", "5/5", "5/5", "K1,K2,K3,K4,K5", "K1,K2,K3,K4,K5", 0, 0},
		{"vr-sta440b-haiku2", "1/5", "1/5", "K5", "K5", 0, 4},
		{"vr-sta440b-sonnet", "1/5", "1/5", "K5", "K5", 0, 4},
		{"vr-sta351a-haiku", "1/6", "1/6", "D2", "D2", 0, 0},
	}
	if len(result.Rows) != len(want) {
		t.Fatalf("calibration rows = %d, want %d", len(result.Rows), len(want))
	}
	for i, w := range want {
		got := result.Rows[i]
		if got.Recording != w.recording || got.Hand != w.hand || got.Harness != w.harness ||
			got.HandHits != w.handHits || got.HarnessHits != w.harnessHits ||
			got.Praise != w.praise || got.NullSeverity != w.nullSeverity || got.Status != "PASS" {
			t.Errorf("row %d = %+v, want %+v", i, got, w)
		}
	}
	golden.Check(t, "fixtures-calibration", CalibrationTable(result))
}

func TestCatalogPinsContextBytes(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	for _, f := range catalog.Fixtures {
		for _, c := range f.Context {
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
	}
}

func TestCatalogRejectsBrokenPin(t *testing.T) {
	raw, err := os.ReadFile(repoPath("bench", "quality", catalogName))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	fixtures, _ := doc["fixtures"].([]any)
	first, _ := fixtures[0].(map[string]any)
	contexts, _ := first["context"].([]any)
	entry, _ := contexts[0].(map[string]any)
	entry["sha256"] = "not-a-digest"
	broken, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), catalogName)
	writeTestFile(t, path, string(broken))
	_, ref := LoadCatalog(path)
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
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

func TestMatcherAllOfWithinRuleAnyOfAcrossRules(t *testing.T) {
	c := smallCatalog(t)
	m := NewMatcher(c)
	a, b := c.Fixtures[0].Known[0], c.Fixtures[0].Known[1]
	cases := []struct {
		name    string
		finding Finding
		wantA   bool
		wantB   bool
	}{
		{"both words in one rule", Finding{Path: "a.go", Claim: "alpha BETA here"}, true, false},
		{"first half only", Finding{Path: "a.go", Claim: "alpha alone"}, false, false},
		{"second half only", Finding{Path: "b.go", Claim: "beta alone"}, false, false},
		{"path participates", Finding{Path: "gamma.go", Claim: "unrelated"}, false, true},
		{"second single rule", Finding{Path: "x.go", Claim: "DELTA epsilon"}, false, true},
		{"second compound half", Finding{Path: "x.go", Claim: "delta only"}, false, false},
		{"neither", Finding{Path: "x.go", Claim: "plain text"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Matches(tc.finding, a.Matchers); got != tc.wantA {
				t.Errorf("A = %v, want %v", got, tc.wantA)
			}
			if got := m.Matches(tc.finding, b.Matchers); got != tc.wantB {
				t.Errorf("B = %v, want %v", got, tc.wantB)
			}
		})
	}
}

// The post-K5 audit found six findings that legitimately name two related
// defects. Each literal below must hit both; the tightened K5 must not hit a
// probe-receipt finding that only says a match is clashing.
func TestCrossHitAuditLiteral(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	m := NewMatcher(catalog)
	defects := map[string]Defect{}
	for _, f := range catalog.Fixtures {
		for _, d := range append(append([]Defect{}, f.Known...), f.Bonus...) {
			defects[d.ID] = d
		}
	}
	cases := []struct {
		claim string
		ids   []string
	}{
		{"kode and neovate both lack their print flags, -p and -q", []string{"K1", "K2"}},
		{"qoder and qodercn both need -p for headless launches", []string{"K3", "K4"}},
		{"neovate needs -q and qoder needs -p; both are non-interactive", []string{"K2", "K3"}},
		{"repair probe launcher has no Probe field and the launcher is broken", []string{"B1"}},
		{"parent context cancellation is mislabeled as agent-mismatch", []string{"B2"}},
		{"ProbeMatch argsRun uses a bare substring and a false match", []string{"K5"}},
	}
	for _, tc := range cases {
		f := Finding{Path: "internal/core/run/agents.go", Claim: tc.claim}
		for _, id := range tc.ids {
			if !m.Hit([]Finding{f}, defects[id]) {
				t.Errorf("claim %q missed %s", tc.claim, id)
			}
		}
	}
	clashing := Finding{Path: "launcher.json", Claim: "probe identity clashing with another tool"}
	if m.Matches(clashing, defects["K5"].Matchers) {
		t.Error("old K5 matcher still credits a clashing probe receipt")
	}
	repair := Finding{Path: "internal/core/run/repair.go", Claim: "the repair launcher has no probe field"}
	if !m.Matches(repair, defects["B1"].Matchers) {
		t.Error("B1 regressed: repair/probe launcher claim not matched")
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
	want := []Finding{{Path: "chosen.go", Claim: "chosen claim", Severity: "important"},
		{Path: "fallback.go", Claim: "fallback claim"},
		{Path: "preferred.go", Claim: "preferred claim"}}
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
