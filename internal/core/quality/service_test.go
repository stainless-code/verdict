package quality

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestRunFixturesLiterals(t *testing.T) {
	root := qualityRepoRoot(t)
	assertTempRepoRoot(t, root)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data
	assertFixturesCatalog(t, data)
	assertCalibrationAndDiagnostics(t, data)
	golden.Check(t, "fixtures-human", Human(data))
	assertReportFile(t, root, data)
}

func assertFixturesCatalog(t *testing.T, data *Report) {
	t.Helper()
	if data.SchemaVersion != "1" || data.Mode != "fixtures" || data.Recordings != 8 || data.Scored != 8 {
		t.Errorf("header = %s/%s/%d/%d", data.SchemaVersion, data.Mode, data.Recordings, data.Scored)
	}
	if len(data.Fixtures) != 5 {
		t.Fatalf("fixtures = %d, want 5", len(data.Fixtures))
	}
	assertFx1Report(t, &data.Fixtures[0])
	assertFx2Report(t, &data.Fixtures[1])
	assertLiveOnlyFixtures(t, data.Fixtures[2:])
}

func assertLiveOnlyFixtures(t *testing.T, fixtures []FixtureReport) {
	t.Helper()
	for i, want := range []struct {
		id    string
		known int
	}{
		{id: "fx3", known: 6},
		{id: "fx4", known: 3},
		{id: "fx5", known: 2},
	} {
		f := &fixtures[i]
		if f.ID != want.id || f.Known != want.known {
			t.Errorf("fixture %d = %+v, want %s with %d known defects", i+3, f, want.id, want.known)
		}
	}
	if fx3, fx5 := fixtures[0], fixtures[2]; fx3.Runs != 0 || fx5.Runs != 0 {
		t.Errorf("fx3/fx5 runs = %d/%d, want 0/0 (live-only fixtures ship no recordings)", fx3.Runs, fx5.Runs)
	}
	assertFx4SyntheticProbe(t, &fixtures[1])
}

// assertFx4SyntheticProbe pins the one synthetic quote probe: two gating
// findings, one matched and one fabricated quote, so the rate is exactly 0.5.
func assertFx4SyntheticProbe(t *testing.T, fx4 *FixtureReport) {
	t.Helper()
	if fx4.Runs != 1 || fx4.Scored != 1 {
		t.Errorf("fx4 = %d runs/%d scored, want the one synthetic probe", fx4.Runs, fx4.Scored)
	}
	h := fx4.Hallucination
	if h.Status != "measured" || h.GatingFindings != 2 || h.Checked != 2 || h.Unverifiable != 0 ||
		h.Rate == nil || *h.Rate != 0.5 {
		t.Errorf("fx4 hallucination = %+v, want measured 2/2/0 at rate 0.5", h)
	}
}

func assertCalibrationAndDiagnostics(t *testing.T, data *Report) {
	t.Helper()
	if data.Calibration.Rows != 8 || !data.Calibration.Reproduced || len(data.Calibration.Mismatches) != 0 {
		t.Errorf("calibration = %+v", data.Calibration)
	}
	if len(data.Diagnostics) != 8 {
		t.Errorf("diagnostics = %d, want 8", len(data.Diagnostics))
	}
}

func assertFx1Report(t *testing.T, fx1 *FixtureReport) {
	t.Helper()
	if fx1.ID != "fx1" || fx1.Runs != 6 || fx1.Scored != 6 || fx1.Known != 5 || fx1.Bonus != 2 {
		t.Errorf("fx1 header = %+v", fx1)
	}
	assertFx1Recall(t, fx1)
	assertFx1Bonuses(t, fx1)
	assertFx1Metrics(t, fx1)
}

func assertFx1Recall(t *testing.T, fx1 *FixtureReport) {
	t.Helper()
	wantPerRun := []float64{0, 0, 0.2, 1, 0.2, 0.2}
	if len(fx1.Recall.PerRun) != len(wantPerRun) {
		t.Fatalf("fx1 per_run = %v", fx1.Recall.PerRun)
	}
	assertPerRunRates(t, fx1.Recall.PerRun, wantPerRun)
	if fx1.Recall.Mean == nil || *fx1.Recall.Mean != 0.2667 {
		t.Errorf("fx1 mean = %v, want 0.2667", fx1.Recall.Mean)
	}
	if fx1.Recall.Union == nil || *fx1.Recall.Union != 1 {
		t.Errorf("fx1 union = %v, want 1", fx1.Recall.Union)
	}
	if got := joinLiteral(fx1.Recall.UnionHits); got != "K1,K2,K3,K4,K5" {
		t.Errorf("fx1 union_hits = %s", got)
	}
}

func assertPerRunRates(t *testing.T, got, want []float64) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fx1 per_run[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func assertFx1Bonuses(t *testing.T, fx1 *FixtureReport) {
	t.Helper()
	if fx1.BonusUnion.Count != 2 || joinLiteral(fx1.BonusUnion.Hits) != "B1,B2" {
		t.Errorf("fx1 bonus_union = %+v", fx1.BonusUnion)
	}
	if fx1.Hallucination.Rate != nil || fx1.Hallucination.Status != "measured" ||
		fx1.Hallucination.GatingFindings != 5 || fx1.Hallucination.Checked != 0 ||
		fx1.Hallucination.Unverifiable != 5 {
		t.Errorf("fx1 hallucination = %+v", fx1.Hallucination)
	}
	if fx1.ReviewInsufficient != 0 {
		t.Errorf("fx1 review_insufficient = %d, want 0", fx1.ReviewInsufficient)
	}
}

func assertFx1Metrics(t *testing.T, fx1 *FixtureReport) {
	t.Helper()
	if fx1.FalseGreen.Passes != 3 || fx1.FalseGreen.Measured != 5 || fx1.FalseGreen.Unknown != 1 ||
		fx1.FalseGreen.Rate == nil || *fx1.FalseGreen.Rate != 0.6 {
		t.Errorf("fx1 false_green = %+v", fx1.FalseGreen)
	}
}

func assertFx2Report(t *testing.T, fx2 *FixtureReport) {
	t.Helper()
	if fx2.ID != "fx2" || fx2.Runs != 1 || fx2.Known != 6 || fx2.Bonus != 0 {
		t.Errorf("fx2 header = %+v", fx2)
	}
	assertFx2Recall(t, fx2)
	assertFx2FalseGreen(t, fx2)
	if fx2.Hallucination.GatingFindings != 1 || fx2.Hallucination.Checked != 0 ||
		fx2.Hallucination.Unverifiable != 1 || fx2.Hallucination.Rate != nil {
		t.Errorf("fx2 hallucination = %+v", fx2.Hallucination)
	}
}

func assertFx2Recall(t *testing.T, fx2 *FixtureReport) {
	t.Helper()
	if len(fx2.Recall.PerRun) != 1 || fx2.Recall.PerRun[0] != 0.1667 {
		t.Errorf("fx2 per_run = %v", fx2.Recall.PerRun)
	}
	if fx2.Recall.Mean == nil || *fx2.Recall.Mean != 0.1667 ||
		fx2.Recall.Union == nil || *fx2.Recall.Union != 0.1667 {
		t.Errorf("fx2 recall = %+v", fx2.Recall)
	}
}

func assertFx2FalseGreen(t *testing.T, fx2 *FixtureReport) {
	t.Helper()
	if fx2.FalseGreen.Passes != 0 || fx2.FalseGreen.Measured != 1 || fx2.FalseGreen.Unknown != 0 ||
		fx2.FalseGreen.Rate == nil || *fx2.FalseGreen.Rate != 0 {
		t.Errorf("fx2 false_green = %+v", fx2.FalseGreen)
	}
}

func assertReportFile(t *testing.T, root string, data *Report) {
	t.Helper()
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(".verdict", "backtest", fixturesDir, reportName)
	if filepath.ToSlash(rel) != filepath.ToSlash(wantPath) {
		t.Errorf("report path = %s, want %s", rel, wantPath)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != jq.PrettyOrdered(data.Wire()) {
		t.Error("report.json differs from the envelope data")
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("report.json is not JSON: %v", err)
	}
}

func joinLiteral(values []string) string { return strings.Join(values, ",") }

func TestGatingFindingPredicate(t *testing.T) {
	trueValue, falseValue := true, false
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{name: "important anchored", f: Finding{Severity: "important", Anchored: &trueValue}, want: true},
		{name: "critical anchored", f: Finding{Severity: "CRITICAL", Anchored: &trueValue}, want: true},
		{name: "important unanchored", f: Finding{Severity: "important", Anchored: &falseValue}, want: false},
		{name: "important missing anchor", f: Finding{Severity: "important"}, want: false},
		{name: "important unverified", f: Finding{Severity: "important", Anchored: &trueValue, Refusal: "unverified"}, want: false},
		{name: "minor anchored", f: Finding{Severity: "minor", Anchored: &trueValue}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGatingFinding(&tc.f); got != tc.want {
				t.Errorf("gating = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestZeroDenominatorRendersNull(t *testing.T) {
	if v := ratePtr(0, 0); v != nil {
		t.Errorf("rate(0/0) = %v, want nil", *v)
	}
	if got := rateText(nil); got != "n/a" {
		t.Errorf("rateText(nil) = %s, want n/a", got)
	}
	if got := rateText(ratePtr(1, 3)); got != "0.3333" {
		t.Errorf("rateText(1/3) = %s, want 0.3333", got)
	}
}

// qualityRepoRoot copies the shipped corpus into a temp dir so no test can
// write report.json into the source checkout.
func qualityRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, repoPath("bench", "quality"), filepath.Join(root, "bench", "quality"))
	return root
}

func copyTree(t *testing.T, source, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, dirPermissions); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		out := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyTree(t, src, out)
			continue
		}
		copyFile(t, src, out)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertTempRepoRoot(t *testing.T, root string) {
	t.Helper()
	if root == repoPath() {
		t.Fatal("qualityRepoRoot must be a temp root, not the source checkout")
	}
}

func copyRecordings(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	for _, fixture := range []string{"fx1", "fx2"} {
		source := repoPath("bench", "quality", fixture, "recordings")
		copyRecordingDir(t, source, filepath.Join(dst, fixture, "recordings"))
	}
	return dst
}

func copyRecordingDir(t *testing.T, source, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, dirPermissions); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(source, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, file.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func rewriteMeta(t *testing.T, root, fixture, recording, field, value string) {
	t.Helper()
	path := MetaPath(filepath.Join(root, fixture, "recordings", recording+recordingExt))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc[field] = json.RawMessage(value)
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInfraErrorExcludedFromDenominators(t *testing.T) {
	root := copyRecordings(t)
	rewriteMeta(t, root, "fx1", "vr-sta440b-haiku", "infra_error", `"agent failed to start"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	assertInfraReport(t, resp.Data)
}

func assertInfraReport(t *testing.T, data *Report) {
	t.Helper()
	if data.Recordings != 7 || data.Scored != 6 {
		t.Errorf("header = %d recordings/%d scored, want 7/6", data.Recordings, data.Scored)
	}
	f := &data.Fixtures[0]
	if f.Runs != 6 || f.Scored != 5 || len(f.Recall.PerRun) != 5 {
		t.Errorf("fx1 = %d runs/%d scored/%d per_run", f.Runs, f.Scored, len(f.Recall.PerRun))
	}
	assertInfraRates(t, f)
}

func assertInfraRates(t *testing.T, f *FixtureReport) {
	t.Helper()
	if f.Recall.Union == nil || *f.Recall.Union != 0.4 || joinLiteral(f.Recall.UnionHits) != "K2,K5" {
		t.Errorf("fx1 union = %v hits %v, want 0.4 K2,K5", f.Recall.Union, f.Recall.UnionHits)
	}
	if f.FalseGreen.Measured != 4 || f.FalseGreen.Passes != 3 || f.FalseGreen.Unknown != 1 {
		t.Errorf("false green = %+v", f.FalseGreen)
	}
}

func TestReviewInsufficientExactCount(t *testing.T) {
	root := copyRecordings(t)
	rewriteMeta(t, root, "fx1", "vr-sta440b-bench", "refusal_code", `"review-insufficient"`)
	rewriteMeta(t, root, "fx1", "vr-sta440b-haiku2", "refusal_code", `"review-insufficient"`)
	rewriteMeta(t, root, "fx2", "vr-sta351a-haiku", "refusal_code", `"missing-findings"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Fixtures[0].ReviewInsufficient != 2 {
		t.Errorf("fx1 review_insufficient = %d, want 2", resp.Data.Fixtures[0].ReviewInsufficient)
	}
	if resp.Data.Fixtures[1].ReviewInsufficient != 0 {
		t.Errorf("fx2 review_insufficient = %d, want 0 (missing-findings is not the same code)",
			resp.Data.Fixtures[1].ReviewInsufficient)
	}
}

func TestFixtureFilterKeepsCalibration(t *testing.T) {
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), FixtureID: "fx2"})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Fixtures) != 1 || resp.Data.Fixtures[0].ID != "fx2" {
		t.Errorf("fixtures = %+v, want just fx2", resp.Data.Fixtures)
	}
	if resp.Data.Calibration.Rows != 8 || !resp.Data.Calibration.Reproduced {
		t.Errorf("calibration = %+v, want 8 rows still reproduced", resp.Data.Calibration)
	}
}

func TestUnknownFixtureRefusesBeforeWrite(t *testing.T) {
	root := qualityRepoRoot(t)
	if _, ref := RunFixtures(context.Background(), &Request{RepoRoot: root}); ref != nil {
		t.Fatalf("valid run: %s: %s", ref.Code, ref.Message)
	}
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: root, FixtureID: "fx1-typo"})
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
	if resp != nil {
		t.Errorf("response = %+v, want nil", resp)
	}
	assertUnknownFixtureMessage(t, ref)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("report.json changed on a refused run")
	}
}

func assertUnknownFixtureMessage(t *testing.T, ref *core.RefusalError) {
	t.Helper()
	if !strings.Contains(ref.Message, "fx1-typo") || !strings.Contains(ref.Message, "fx1") ||
		!strings.Contains(ref.Message, "fx2") {
		t.Errorf("refusal message = %q, want the valid ids", ref.Message)
	}
}

func copyOnlyRecordings(t *testing.T, fixture, name string) string {
	t.Helper()
	root := t.TempDir()
	source := repoPath("bench", "quality", fixture, "recordings")
	dst := filepath.Join(root, fixture, "recordings")
	if err := os.MkdirAll(dst, dirPermissions); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{recordingExt, metaSuffix} {
		raw, err := os.ReadFile(filepath.Join(source, name+suffix))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name+suffix), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRecordingsOverrideKeepsShippedCalibration(t *testing.T) {
	root := copyOnlyRecordings(t, "fx1", "vr-sta440b-bench")
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data
	if data.Recordings != 1 || data.Scored != 1 {
		t.Errorf("header = %d recordings/%d scored, want 1/1", data.Recordings, data.Scored)
	}
	if data.Calibration.Rows != 8 || !data.Calibration.Reproduced {
		t.Errorf("calibration = %+v, want 8 reproduced rows", data.Calibration)
	}
	if !strings.Contains(resp.Human, "calibration: 8/8 rows reproduced") {
		t.Errorf("human calibration line = %q", resp.Human)
	}
	if data.Fixtures[1].Runs != 0 {
		t.Errorf("fx2 runs = %d, want 0 with the override", data.Fixtures[1].Runs)
	}
}

func TestMetaTrailingDataRefused(t *testing.T) {
	root := copyRecordings(t)
	path := MetaPath(filepath.Join(root, "fx1", "recordings", "vr-sta440b-bench"+recordingExt))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n{}"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
	if !strings.Contains(ref.Message, "trailing data") {
		t.Errorf("refusal message = %q", ref.Message)
	}
}

// cancelAfterErrCalls is deterministic: RunFixtures consults Err once at
// entry and once per recording. One override recording plus the eight shipped
// calibration recordings makes the eleventh check the one just before
// writeReport.
type cancelAfterErrCalls struct {
	remaining int
	calls     int
}

func (*cancelAfterErrCalls) Deadline() (time.Time, bool) { return time.Time{}, false }

func (*cancelAfterErrCalls) Done() <-chan struct{} { return nil }

func (*cancelAfterErrCalls) Value(any) any { return nil }

func (c *cancelAfterErrCalls) Err() error {
	c.calls++
	if c.calls > c.remaining {
		return context.Canceled
	}
	return nil
}

func TestCanceledWithoutGatingFindingDoesNotWrite(t *testing.T) {
	recordings := copyOnlyRecordings(t, "fx1", "vr-sta440b-bench")
	root := qualityRepoRoot(t)
	normal, ref := RunFixtures(context.Background(), &Request{RepoRoot: root, RecordingsRoot: recordings})
	if ref != nil {
		t.Fatalf("baseline run: %s: %s", ref.Code, ref.Message)
	}
	if gating := normal.Data.Fixtures[0].Hallucination.GatingFindings; gating != 0 {
		t.Fatalf("baseline gating findings = %d, want 0", gating)
	}
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	writeTestFile(t, path, "sentinel\n")
	ctx := &cancelAfterErrCalls{remaining: 10}
	resp, ref := RunFixtures(ctx, &Request{RepoRoot: root, RecordingsRoot: recordings})
	if ref == nil || ref.Code != "canceled" {
		t.Errorf("refusal = %+v, want canceled", ref)
	}
	if resp != nil {
		t.Errorf("response = %+v, want no report", resp)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "sentinel\n" {
		t.Errorf("report.json = %q, want untouched sentinel", raw)
	}
}

func TestCanceledDuringVerificationDoesNotWrite(t *testing.T) {
	root := qualityRepoRoot(t)
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	writeTestFile(t, path, "sentinel\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifier := quoteVerifierFunc(func(_ context.Context, _ *Finding, _ []byte) QuoteResult {
		cancel()
		return QuoteResult{Status: "matched"}
	})
	resp, ref := RunFixtures(ctx, &Request{RepoRoot: root, Verifier: verifier})
	if ref == nil || ref.Code != "canceled" {
		t.Errorf("refusal = %+v, want canceled", ref)
	}
	if resp != nil {
		t.Errorf("response = %+v, want no report", resp)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "sentinel\n" {
		t.Errorf("report.json = %q, want untouched sentinel", raw)
	}
}

func TestSidecarFixtureMustMatchDirectory(t *testing.T) {
	root := copyRecordings(t)
	rewriteMeta(t, root, "fx1", "vr-sta440b-bench", "fixture", `"fx2"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
	if resp != nil {
		t.Errorf("response = %+v, want no report", resp)
	}
	if !strings.Contains(ref.Message, "fx1") || !strings.Contains(ref.Message, "fx2") {
		t.Errorf("refusal message = %q, want both fixture ids", ref.Message)
	}
}

func TestDuplicateRecordingNameRefused(t *testing.T) {
	root := copyOnlyRecordings(t, "fx1", "vr-sta440b-bench")
	copyNamedRecording(t, "fx1", "vr-sta440b-bench", root, "fx2")
	rewriteMeta(t, root, "fx2", "vr-sta440b-bench", "fixture", `"fx2"`)
	_, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), RecordingsRoot: root})
	if ref == nil || ref.Code != "fixtures-recording-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-recording-invalid", ref)
	}
	if !strings.Contains(ref.Message, "repeated recording name vr-sta440b-bench") {
		t.Errorf("refusal message = %q", ref.Message)
	}
}

func copyNamedRecording(t *testing.T, srcFixture, name, dstRoot, dstFixture string) {
	t.Helper()
	source := repoPath("bench", "quality", srcFixture, "recordings")
	dst := filepath.Join(dstRoot, dstFixture, "recordings")
	if err := os.MkdirAll(dst, dirPermissions); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{recordingExt, metaSuffix} {
		raw, err := os.ReadFile(filepath.Join(source, name+suffix))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name+suffix), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type quoteVerifierFunc func(context.Context, *Finding, []byte) QuoteResult

func (fn quoteVerifierFunc) Verify(ctx context.Context, f *Finding, head []byte) QuoteResult {
	return fn(ctx, f, head)
}

func TestNoQuoteIsUnverifiableAndGetsRealHead(t *testing.T) {
	var heads [][]byte
	verifier := quoteVerifierFunc(func(_ context.Context, _ *Finding, head []byte) QuoteResult {
		heads = append(heads, append([]byte(nil), head...))
		return QuoteResult{Status: "no-quote"}
	})
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), Verifier: verifier})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	h := resp.Data.Fixtures[0].Hallucination
	if h.Checked != 0 || h.Unverifiable != 5 || h.Rate != nil {
		t.Errorf("fx1 hallucination = %+v, want 0 checked / 5 unverifiable / n/a", h)
	}
	if h.Status != "stubbed" {
		t.Errorf("fx1 hallucination status = %q, want stubbed for an injected double", h.Status)
	}
	if !strings.Contains(resp.Human, "0 checked, 5 unverifiable") {
		t.Errorf("human hallucination line = %q", resp.Human)
	}
	assertPinnedDiffHead(t, heads)
}

// assertFixtureHead pins the bytes the verifier is handed: the fixture's
// pinned full.diff, not the context documents it was scored from.
func assertPinnedDiffHead(t *testing.T, heads [][]byte) {
	t.Helper()
	if len(heads) == 0 || len(heads[0]) == 0 {
		t.Fatalf("verifier got %d heads, first length %d; want non-empty fixture head", len(heads), len(heads[0]))
	}
	want := "diff --git a/.changeset/sta-440-agents-2.md b/.changeset/sta-440-agents-2.md\n" +
		"new file mode 100644\n"
	if !bytes.HasPrefix(heads[0], []byte(want)) {
		t.Errorf("head does not start with the pinned fx1 full.diff: %.120q", heads[0])
	}
	if !bytes.Contains(heads[0], []byte("+++ b/internal/core/run/agents.go")) {
		t.Errorf("head does not carry fx1's changed code paths: %.120q", heads[0])
	}
}

func TestOnlyExplicitMismatchCountsInRate(t *testing.T) {
	calls := 0
	verifier := quoteVerifierFunc(func(_ context.Context, _ *Finding, _ []byte) QuoteResult {
		calls++
		if calls == 1 {
			return QuoteResult{Status: "mismatch"}
		}
		return QuoteResult{Status: "matched"}
	})
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), Verifier: verifier})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	h := resp.Data.Fixtures[0].Hallucination
	if h.Checked != 5 || h.Unverifiable != 0 || h.Rate == nil || *h.Rate != 0.2 {
		t.Errorf("fx1 hallucination = %+v, want 5 checked / 0 unverifiable / 0.2", h)
	}
	if h.Status != "stubbed" || rateText(h.Rate) != "0.2000" {
		t.Errorf("fx1 hallucination status/rate = %q/%s, want an injected double reported stubbed",
			h.Status, rateText(h.Rate))
	}
}

// TestPinnedVerifierPointerMeasures proves R12: the pinned verifier measures by
// pointer exactly as it does by value, because both answer from the fixture's
// pinned bytes rather than from a stand-in.
func TestPinnedVerifierPointerMeasures(t *testing.T) {
	byValue, ref := RunFixtures(context.Background(), &Request{
		RepoRoot: qualityRepoRoot(t), FixtureID: "fx4", Verifier: PinnedQuoteVerifier{},
	})
	if ref != nil {
		t.Fatalf("value run: %s: %s", ref.Code, ref.Message)
	}
	byPointer, ref := RunFixtures(context.Background(), &Request{
		RepoRoot: qualityRepoRoot(t), FixtureID: "fx4", Verifier: &PinnedQuoteVerifier{},
	})
	if ref != nil {
		t.Fatalf("pointer run: %s: %s", ref.Code, ref.Message)
	}
	want, got := byValue.Data.Fixtures[0].Hallucination, byPointer.Data.Fixtures[0].Hallucination
	if want.Status != "measured" || got.Status != "measured" {
		t.Fatalf("status = %q by value, %q by pointer, want measured for both", want.Status, got.Status)
	}
	if rateText(want.Rate) != "0.5000" || rateText(got.Rate) != "0.5000" {
		t.Fatalf("rate = %s by value, %s by pointer, want 0.5000 for both",
			rateText(want.Rate), rateText(got.Rate))
	}
}

func TestSyntheticQuoteProbeMeasuresHalf(t *testing.T) {
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: qualityRepoRoot(t), FixtureID: "fx4"})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	h := resp.Data.Fixtures[0].Hallucination
	if h.Rate == nil || *h.Rate != 0.5 {
		t.Fatalf("fx4 hallucination rate = %s, want 0.5 (one matched, one fabricated quote)", rateText(h.Rate))
	}
	want := "  hallucination: 0.5000 (measured; 2 gating findings, 2 checked, 0 unverifiable)  review-insufficient: 0"
	if !strings.Contains(resp.Human, want) {
		t.Errorf("human hallucination line = %q, want %q", resp.Human, want)
	}
}

// TestSyntheticProbeQuotesAcrossTheVerifier pins which of the probe's two
// quotes verifies, so a matcher that answers matched for both cannot leave the
// rate at 0.5 and pass unnoticed.
func TestSyntheticProbeQuotesAcrossTheVerifier(t *testing.T) {
	root := qualityRepoRoot(t)
	catalog, ref := LoadCatalog(filepath.Join(root, "bench", "quality", catalogName))
	if ref != nil {
		t.Fatalf("load catalog: %s: %s", ref.Code, ref.Message)
	}
	heads, ref := loadFixtureHeads(root, catalog)
	if ref != nil {
		t.Fatalf("load heads: %s: %s", ref.Code, ref.Message)
	}
	recording, ref := LoadFixtureRecording(recordingPath(t, "fx4", "synthetic-quotes"))
	if ref != nil {
		t.Fatalf("load recording: %s: %s", ref.Code, ref.Message)
	}
	findings := canonicalFindings(recording)
	if len(findings) != 2 {
		t.Fatalf("probe findings = %d, want 2", len(findings))
	}
	want := []QuoteResult{
		{Status: "matched"},
		{Status: "mismatch", Reason: "text-differs"},
	}
	for i := range want {
		got := PinnedQuoteVerifier{}.Verify(context.Background(), &findings[i], heads["fx4"])
		if got != want[i] {
			t.Errorf("probe finding %d (%s:%d) = %+v, want %+v",
				i, findings[i].Path, findings[i].Line, got, want[i])
		}
	}
}

func TestContextPinMismatchRefusesBeforeScoring(t *testing.T) {
	root := catalogTreeWithRecordings(t)
	contextDir := filepath.Join(root, "bench", "quality", "context", "fx1")
	if err := os.MkdirAll(contextDir, dirPermissions); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(contextDir, "SPEC-2.md"), "changed head\n")
	_, ref := RunFixtures(context.Background(), &Request{RepoRoot: root})
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
	if !strings.Contains(ref.Message, "context byte count") || !strings.Contains(ref.Message, "fx1") {
		t.Errorf("refusal message = %q", ref.Message)
	}
}

// TestDiffPinMismatchRefusesBeforeScoring proves a fixture's pinned full.diff
// is checked exactly like its context pins: the byte count first, then the
// digest, so edited or replaced diff bytes never reach the verifier.
func TestDiffPinMismatchRefusesBeforeScoring(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte) []byte
		want   string
	}{
		{
			name:   "one extra byte",
			mutate: func(raw []byte) []byte { return append(raw, '\n') },
			want:   "diff byte count for fixture fx1 is ",
		},
		{
			name: "one changed byte",
			mutate: func(raw []byte) []byte {
				raw[0] = 'D'
				return raw
			},
			want: "diff sha256 for fixture fx1 is ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := qualityRepoRoot(t)
			rewriteFixtureDiff(t, root, tc.mutate)
			_, ref := RunFixtures(context.Background(), &Request{RepoRoot: root})
			assertDiffPinRefusal(t, ref, tc.want)
		})
	}
}

func rewriteFixtureDiff(t *testing.T, root string, mutate func([]byte) []byte) {
	t.Helper()
	path := filepath.Join(root, "bench", "quality", "context", "fx1", "full.diff")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mutate(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertDiffPinRefusal(t *testing.T, ref *core.RefusalError, want string) {
	t.Helper()
	if ref == nil || ref.Code != "fixtures-invalid" {
		t.Fatalf("refusal = %+v, want fixtures-invalid", ref)
	}
	if !strings.Contains(ref.Message, want) {
		t.Errorf("refusal message = %q, want %q", ref.Message, want)
	}
}

func catalogTreeWithRecordings(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "bench", "quality")
	if err := os.MkdirAll(catalogDir, dirPermissions); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(repoPath("bench", "quality", "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "fixtures.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []string{"fx1", "fx2"} {
		copyRecordingDir(t, repoPath("bench", "quality", fixture, "recordings"),
			filepath.Join(catalogDir, fixture, "recordings"))
	}
	return root
}
