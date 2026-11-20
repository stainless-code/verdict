package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		{"synthetic-quotes", "0/3", "0/3", "-", "-", 0, 0},
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
		path := recordingPath(t, row.Fixture, row.Recording)
		meta, ref := LoadRecordingMeta(MetaPath(path))
		if ref != nil {
			t.Fatalf("load meta %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		recording, ref := LoadFixtureRecording(path)
		if ref != nil {
			t.Fatalf("load recording %s: %s: %s", row.Recording, ref.Code, ref.Message)
		}
		fixture := fixtureFor(t, catalog, row.Fixture)
		runs = append(runs, *Score(matcher, fixture, canonicalFindings(recording), meta,
			recording.GatePassed, row.Recording))
	}
	return runs
}

func recordingPath(t *testing.T, fixture, name string) string {
	t.Helper()
	return repoPath("bench", "quality", fixture, "recordings", name+recordingExt)
}

func canonicalFindings(rec *FixtureRecording) []Finding {
	out := make([]Finding, 0, len(rec.Findings))
	for i := range rec.Findings {
		out = append(out, rec.Findings[i].Canonical())
	}
	return out
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

// TestCatalogPinsShippedBytes re-hashes every shipped file the catalog pins:
// the context documents and each fixture's full.diff.
func TestCatalogPinsShippedBytes(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	for i := range catalog.Fixtures {
		for j := range catalog.Fixtures[i].Context {
			assertPinnedFile(t, &catalog.Fixtures[i].Context[j])
		}
		assertPinnedFile(t, &catalog.Fixtures[i].Diff)
	}
}

func assertPinnedFile(t *testing.T, pin *ContextFile) {
	t.Helper()
	raw, err := os.ReadFile(repoPath(filepath.FromSlash(pin.Path)))
	if err != nil {
		t.Fatalf("read %s: %v", pin.Path, err)
	}
	if len(raw) != pin.Bytes {
		t.Errorf("%s bytes = %d, pin says %d", pin.Path, len(raw), pin.Bytes)
	}
	digest := sha256.Sum256(raw)
	if got := hex.EncodeToString(digest[:]); got != pin.SHA256 {
		t.Errorf("%s sha256 = %s, pin says %s", pin.Path, got, pin.SHA256)
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

func TestCatalogRejectsWindowsTraversal(t *testing.T) {
	for _, name := range []string{`..\outside.txt`, `C:\outside.txt`, `\\server\share\outside.txt`} {
		assertCatalogRejectsPath(t, name)
	}
}

func assertCatalogRejectsPath(t *testing.T, name string) {
	t.Helper()
	doc := catalogDoc(t)
	first, ok := doc["fixtures"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatal("first fixture is not an object")
	}
	contexts, ok := first["context"].([]any)
	if !ok || len(contexts) == 0 {
		t.Fatal("first fixture has no context array")
	}
	firstContextEntry(t, contexts)["path"] = name
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), catalogName)
	writeTestFile(t, path, string(raw))
	_, ref := LoadCatalog(path)
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Errorf("path %q: refusal = %+v, want fixtures-invalid", name, ref)
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
    "diff": {"path": "bench/quality/diff/fx.diff", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bytes": 1},
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
	name    string
	finding Finding
	wantA   bool
	wantB   bool
}

func TestMatcherAllOfWithinRuleAnyOfAcrossRules(t *testing.T) {
	c := smallCatalog(t)
	m := NewMatcher(c)
	a, b := c.Fixtures[0].Known[0], c.Fixtures[0].Known[1]
	cases := []matcherCase{
		{"both words in one rule", Finding{Path: "a.go", Claim: "alpha BETA here"}, true, false},
		{"first half only", Finding{Path: "a.go", Claim: "alpha alone"}, false, false},
		{"second half only", Finding{Path: "b.go", Claim: "beta alone"}, false, false},
		{"path participates", Finding{Path: "gamma.go", Claim: "unrelated"}, false, true},
		{"second single rule", Finding{Path: "x.go", Claim: "DELTA epsilon"}, false, true},
		{"second compound half", Finding{Path: "x.go", Claim: "delta only"}, false, false},
		{"neither", Finding{Path: "x.go", Claim: "plain text"}, false, false},
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

type fixtureMatcherCase struct {
	name      string
	fixtureID string
	defectID  string
	finding   Finding
	want      bool
}

func defectFor(t *testing.T, catalog *Catalog, fixtureID, defectID string) *Defect {
	t.Helper()
	fx := fixtureFor(t, catalog, fixtureID)
	for i := range fx.Known {
		if fx.Known[i].ID == defectID {
			return &fx.Known[i]
		}
	}
	for i := range fx.Bonus {
		if fx.Bonus[i].ID == defectID {
			return &fx.Bonus[i]
		}
	}
	t.Fatalf("unknown defect %s/%s", fixtureID, defectID)
	return nil
}

// TestFixtureMatchersTightened pins both genuine positives and observed
// false-positive findings (from the 2026-11-20 snapshot audit) so loose
// regexes cannot inflate recall by crediting unrelated defects.
func TestFixtureMatchersTightened(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	m := NewMatcher(catalog)
	cases := tightenedMatcherCases()
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			d := defectFor(t, catalog, tc.fixtureID, tc.defectID)
			if got := m.Matches(&tc.finding, d.Matchers); got != tc.want {
				t.Errorf("%s/%s match = %v, want %v (path=%q)",
					tc.fixtureID, tc.defectID, got, tc.want, tc.finding.Path)
			}
		})
	}
}

func tightenedMatcherCases() []fixtureMatcherCase {
	return []fixtureMatcherCase{
		{
			name: "fx2_D1_positive_promptArtifact_reviewFile", fixtureID: "fx2", defectID: "D1", want: true,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "argsFor hands compose the result of st.promptArtifact(dir), and promptArtifact still joins reviewFile, so drainDirective tells the agent to write review.json while the launcher asserts drain.replies.json",
			},
		},
		{
			name: "fx2_D1_negative_manual_namespace", fixtureID: "fx2", defectID: "D1", want: false,
			finding: Finding{
				Path:  "docs/manual.md",
				Claim: "These new drain paragraphs name drain.json and drain.replies.json, but the normative Bundle namespace section never adds either to its core/script artifact list, and its \"Agents write only review.json\" sentence still excludes drain.replies.json",
			},
		},
		{
			name: "fx2_D1_negative_drain_artifact_hint", fixtureID: "fx2", defectID: "D1", want: false,
			finding: Finding{
				Path:  "internal/core/run/drain.go",
				Claim: "Reusing execute also reuses run's review-artifact hints: no-review-artifact says review.json instead of drain.replies.json",
			},
		},
		{
			name: "fx2_D2_positive_next_suggests_post", fixtureID: "fx2", defectID: "D2", want: true,
			finding: Finding{
				Path:  "internal/drain/verb.go",
				Claim: "next keys only off fl.agent != \"\", so drain <repo> <pr> --agent <name> on zero pending threads still answers \"next\": \"post the drafts with verdict drain acme/app 7 --post\"",
			},
		},
		{
			name: "fx2_D2_negative_refusal_code_mentions_post", fixtureID: "fx2", defectID: "D2", want: false,
			finding: Finding{
				Path:  "internal/core/refusalcodes.go",
				Claim: "Spec §7 requires four new codes; only three shipped. drain-incomplete is deferred because only drain --post (PR B, out of scope here) emits it",
			},
		},
		{
			name: "fx2_D3_positive_validateFields_missing_required", fixtureID: "fx2", defectID: "D3", want: true,
			finding: Finding{
				Path:  "internal/core/drain/drafts.go",
				Claim: "validateFields only rejects unknown keys and never checks for missing required keys",
			},
		},
		{
			name: "fx2_D3_negative_validateFields_quadratic", fixtureID: "fx2", defectID: "D3", want: false,
			finding: Finding{
				Path:  "internal/core/drain/drafts.go",
				Claim: "validateFields re-unmarshals the whole replies array on every entry purely to reject unknown keys",
			},
		},
		{
			name: "fx2_D4_positive_schema_additionalProperties", fixtureID: "fx2", defectID: "D4", want: true,
			finding: Finding{
				Path:  "skills/verdict/schemas/drain.schema.json",
				Claim: "drain.schema.json $defs entries set additionalProperties: true instead of closing the object",
			},
		},
		{
			name: "fx2_D4_negative_overlay_defs_open_threads", fixtureID: "fx2", defectID: "D4", want: false,
			finding: Finding{
				Path:  "internal/schemagen/overlay.json",
				Claim: "The published $defs.skip description omits no-draft when open threads are skipped",
			},
		},
		{
			name: "fx2_D5_positive_drain_plan_gh_stub_wildcard", fixtureID: "fx2", defectID: "D5", want: true,
			finding: Finding{
				Path:  "evals/cases/drain-plan.sh",
				Claim: "evals/cases/drain-plan.sh gh stub matches *api graphql* instead of exact argv",
			},
		},
		{
			name: "fx2_D5_negative_drain_test_stub_agents", fixtureID: "fx2", defectID: "D5", want: false,
			finding: Finding{
				Path:  "internal/core/run/drain_test.go",
				Claim: "assertDrainDirective never asserts the prompt names drain.replies.json; the stub agents in evals/cases/drain-plan.sh and drain-refusals.sh also hardcode the reply path",
			},
		},
		{
			name: "fx2_D5_negative_service_mentions_stub_agent", fixtureID: "fx2", defectID: "D5", want: false,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "promptArtifact hardcodes reviewFile; the stub agent in evals/cases/drain-plan.sh writes drain.replies.json directly",
			},
		},
		{
			name: "fx2_D6_positive_drain_refusals_gh_stub_wildcard", fixtureID: "fx2", defectID: "D6", want: true,
			finding: Finding{
				Path:  "evals/cases/drain-refusals.sh",
				Claim: "evals/cases/drain-refusals.sh gh stub matches *api graphql* wildcard instead of checking exact argv",
			},
		},
		{
			name: "fx2_D6_negative_drain_test_stub_agents", fixtureID: "fx2", defectID: "D6", want: false,
			finding: Finding{
				Path:  "internal/core/run/drain_test.go",
				Claim: "assertDrainDirective never asserts the prompt names drain.replies.json; the stub agents in evals/cases/drain-plan.sh and drain-refusals.sh also hardcode the reply path",
			},
		},
		{
			name: "fx1_B1_positive_repairLoop_omits_probe", fixtureID: "fx1", defectID: "B1", want: true,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "repairLoop builds its own launcher literal without Probe, so launcher.json omits the probe receipt under --rounds",
			},
		},
		{
			name: "fx1_B1_negative_probe_args_redaction", fixtureID: "fx1", defectID: "B1", want: false,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "probe args bypass displayArgv redaction and are cloned verbatim into launcher.json",
			},
		},
		{
			name: "fx1_B2_positive_parent_cancel_mislabeled", fixtureID: "fx1", defectID: "B2", want: true,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "A parent-context cancel during the probe triggers pctx.Err() != nil and refuses agent-mismatch timed out",
			},
		},
		{
			name: "fx1_B2_negative_replayStopCodes_mentions_canceled", fixtureID: "fx1", defectID: "B2", want: false,
			finding: Finding{
				Path:  "internal/core/run/service.go",
				Claim: "replayStopCodes still lists only agent-missing, bad-timeout and canceled, so backtest replay keeps walking after agent-mismatch",
			},
		},
		{
			name: "fx4_D3_positive_context_sources_toc_scope", fixtureID: "fx4", defectID: "D3", want: true,
			finding: Finding{
				Path:  "evals/cases/context-sources.sh",
				Claim: "evals/cases/context-sources.sh greps for the source row anywhere in toc.txt rather than inside the ## context section",
			},
		},
		{
			name: "fx4_D3_negative_unrelated_context_sources", fixtureID: "fx4", defectID: "D3", want: false,
			finding: Finding{
				Path:  "evals/cases/context-sources.sh",
				Claim: "context-sources.sh leaves a temporary fixture dir outside the section cleanup scope",
			},
		},
	}
}

func TestFixtureRecordingLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "literal"+recordingExt)
	writeTestFile(t, path, `{
  "schema_version": "1",
  "findings": [
    {"path": "chosen.go", "line": 7, "severity": "important", "text": "chosen claim"},
    {"path": "plain.go", "line": null, "severity": null, "text": "plain claim"}
  ],
  "gate_passed": null
}`)
	rec, ref := LoadFixtureRecording(path)
	if ref != nil {
		t.Fatalf("load: %s: %s", ref.Code, ref.Message)
	}
	if rec.GatePassed != nil {
		t.Errorf("gate_passed = %v, want nil", *rec.GatePassed)
	}
	assertFixtureCanonical(t, rec)
	broken := filepath.Join(dir, "broken"+recordingExt)
	writeTestFile(t, broken, `{"schema_version":"1","findings":[{"file":"x","body":"y"}]}`)
	if _, ref := LoadFixtureRecording(broken); ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
}

func assertFixtureCanonical(t *testing.T, rec *FixtureRecording) {
	t.Helper()
	want := []Finding{
		{Path: "chosen.go", Claim: "chosen claim", Severity: "important", Line: 7},
		{Path: "plain.go", Claim: "plain claim"},
	}
	wantAnchored := []bool{true, false}
	for i, w := range want {
		got := rec.Findings[i].Canonical()
		if got.Path != w.Path || got.Claim != w.Claim || got.Severity != w.Severity || got.Line != w.Line {
			t.Errorf("finding %d = %+v, want %+v", i, got, w)
		}
		if got.Anchored == nil || *got.Anchored != wantAnchored[i] {
			t.Errorf("finding %d anchored = %v, want %v", i, got.Anchored, wantAnchored[i])
		}
	}
}

func TestRecordingMetaProvenance(t *testing.T) {
	path := MetaPath(recordingPath(t, "fx1", "vr-sta440b-bench"))
	meta, ref := LoadRecordingMeta(path)
	if ref != nil {
		t.Fatalf("load meta: %s: %s", ref.Code, ref.Message)
	}
	want := "4d61d57f32da6190ef8e54f41b40da56ab7e34f2ac96eee1c1964ff46fe250bf"
	if meta.SourceSHA256 != want || meta.SourceBundle != "vr-sta440b-bench/bundle" {
		t.Errorf("provenance = %s / %s", meta.SourceBundle, meta.SourceSHA256)
	}
	bad := filepath.Join(t.TempDir(), "bad.meta.json")
	writeTestFile(t, bad, `{"schema_version":"1","fixture":"fx1","source_bundle":"x","source_sha256":"not-a-digest"}`)
	if _, ref := LoadRecordingMeta(bad); ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
}

func TestGatingFromFixtureRecording(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gating"+recordingExt)
	writeTestFile(t, path, `{
  "schema_version": "1",
  "findings": [
    {"path": "a.go", "line": 42, "severity": "important", "text": "anchored claim"},
    {"path": "b.go", "line": 0, "severity": "important", "text": "zero line claim"},
    {"path": "c.go", "line": null, "severity": "important", "text": "no line claim"},
    {"path": "d.go", "line": 9, "severity": "informational", "text": "low severity claim"}
  ],
  "gate_passed": true
}`)
	rec, ref := LoadFixtureRecording(path)
	if ref != nil {
		t.Fatalf("load: %s: %s", ref.Code, ref.Message)
	}
	want := []bool{true, false, false, false}
	for i := range rec.Findings {
		f := rec.Findings[i].Canonical()
		if got := IsGatingFinding(&f); got != want[i] {
			t.Errorf("finding %d (%s) gating = %v, want %v", i, f.Path, got, want[i])
		}
	}
}

func TestCalibrateMissingRecordingKeepsRow(t *testing.T) {
	catalog, ref := LoadCatalog(repoPath("bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	catalog.Calibration = catalog.Calibration[:1]
	result := Calibrate(catalog, nil)
	if result.Reproduced || len(result.Rows) != 1 {
		t.Fatalf("reproduced = %v, rows = %d, want false/1", result.Reproduced, len(result.Rows))
	}
	row := result.Rows[0]
	if row.Recording != catalog.Calibration[0].Recording || row.Status != "FAIL" || row.Harness != "n/a" {
		t.Errorf("missing row = %+v", row)
	}
	human := Human(&Report{Calibration: calibrationSummary(result)})
	if !strings.Contains(human, "calibration: 0/1 rows reproduced") {
		t.Errorf("human line disagrees with JSON: %s", human)
	}
}

const minimalCatalog = `{
  "schema_version": "1",
  "review_signals": {"praise_re": "x", "defect_verb_re": "y"},
  "fixtures": [
    {
      "id": "fx-min",
      "label": "minimal",
      "repo": "r/r",
      "base": "0000000000000000000000000000000000000000",
      "head": "1111111111111111111111111111111111111111",
      "context": [],
      "diff": {"path": "bench/quality/diff/fx-min.diff", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bytes": 1},
      "known": [{"id": "K1", "description": "known defect", "matchers": [["x"]]}],
      "bonus": []
    }
  ],
  "calibration": [
    {"recording": "r1", "fixture": "fx-min", "driver": "d", "hand_label": "0/1", "hand": []}
  ]
}`

func TestCatalogRejectsBlankPatternAndSharedDefectID(t *testing.T) {
	cases := []struct {
		name, wantText, catalog string
	}{
		{
			name:     "blank pattern",
			wantText: "rule 0",
			catalog:  strings.Replace(minimalCatalog, `"matchers": [["x"]]`, `"matchers": [["   "]]`, 1),
		},
		{
			name:     "shared defect id",
			wantText: "known and bonus",
			catalog: strings.Replace(minimalCatalog, `"bonus": []`,
				`"bonus": [{"id": "K1", "description": "bonus defect", "matchers": [["y"]]}]`, 1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCatalogRefused(t, tc.catalog, tc.wantText) })
	}
}

func assertCatalogRefused(t *testing.T, catalog, wantText string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), catalogName)
	writeTestFile(t, path, catalog)
	_, ref := LoadCatalog(path)
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
	if !strings.Contains(ref.Message, "K1") || !strings.Contains(ref.Message, wantText) {
		t.Errorf("refusal message = %q, want K1 and %q", ref.Message, wantText)
	}
}

func TestCalibrateRequiresScoredRun(t *testing.T) {
	catalog := &Catalog{
		Fixtures:    []Fixture{{ID: "fx", Known: []Defect{{ID: "K1"}}}},
		Calibration: []CalibrationRow{{Recording: "r1", Fixture: "fx", Hand: []string{}}},
	}
	unscored := RunScore{Recording: "r1", Fixture: "fx", InfraError: "agent failed to start"}
	result := Calibrate(catalog, []RunScore{unscored})
	if result.Reproduced || len(result.Rows) != 1 {
		t.Fatalf("unscored reproduced = %v, rows = %d, want false/1", result.Reproduced, len(result.Rows))
	}
	row := result.Rows[0]
	if row.Status != "FAIL" || !strings.Contains(row.Reason, "agent failed to start") {
		t.Errorf("unscored row = %+v", row)
	}
	if !strings.Contains(CalibrationTable(result), "agent failed to start") {
		t.Error("calibration table does not name the reason")
	}

	scored := RunScore{Recording: "r1", Fixture: "fx", Scored: true}
	result = Calibrate(catalog, []RunScore{scored})
	if !result.Reproduced || result.Rows[0].Status != "PASS" {
		t.Errorf("scored empty-hand row = %+v, reproduced = %v", result.Rows[0], result.Reproduced)
	}
}

func TestHumanRecallDenominatorMatchesRates(t *testing.T) {
	root := copyRecordings(t)
	rewriteMeta(t, root, "fx1", "vr-sta440b-haiku", "infra_error", `"agent failed to start"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data
	if data.Scored != 6 || data.Fixtures[0].Scored != 5 {
		t.Fatalf("header scored = %d, fx1 scored = %d, want 6/5", data.Scored, data.Fixtures[0].Scored)
	}
	line := recallLine(t, Human(data))
	rates := recallRates(line)
	if len(rates) != data.Fixtures[0].Scored {
		t.Errorf("human lists %d rates (%v), JSON scored %d", len(rates), rates, data.Fixtures[0].Scored)
	}
	wantN := "(N=" + strconv.Itoa(data.Fixtures[0].Scored) + ", "
	if !strings.Contains(line, wantN) {
		t.Errorf("human line %q does not say %q", line, wantN)
	}
}

func TestCalibrationRejectsWrongFixtureRun(t *testing.T) {
	catalog := &Catalog{
		Fixtures:    []Fixture{{ID: "fx1", Known: []Defect{{ID: "K1"}}}},
		Calibration: []CalibrationRow{{Recording: "r1", Fixture: "fx1", Hand: []string{}}},
	}
	run := RunScore{Recording: "r1", Fixture: "fx2", Scored: true}
	result := Calibrate(catalog, []RunScore{run})
	if result.Reproduced || len(result.Rows) != 1 {
		t.Fatalf("reproduced = %v, rows = %d, want false/1", result.Reproduced, len(result.Rows))
	}
	row := result.Rows[0]
	if row.Status != "FAIL" || !strings.Contains(row.Reason, "fx2") || !strings.Contains(row.Reason, "fx1") {
		t.Errorf("wrong-fixture row = %+v", row)
	}
}

func recallLine(t *testing.T, human string) string {
	t.Helper()
	for line := range strings.SplitSeq(human, "\n") {
		if strings.Contains(line, "recall per run:") {
			return line
		}
	}
	t.Fatal("human report has no recall line")
	return ""
}

func recallRates(line string) []string {
	rest := strings.SplitN(line, "recall per run: ", 2)[1]
	return strings.Fields(strings.SplitN(rest, "  mean:", 2)[0])
}
