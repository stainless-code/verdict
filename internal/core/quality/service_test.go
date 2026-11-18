package quality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestRunFixturesLiterals(t *testing.T) {
	root := repoPath()
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data
	if data.SchemaVersion != "1" || data.Mode != "fixtures" || data.Recordings != 7 || data.Scored != 7 {
		t.Errorf("header = %s/%s/%d/%d", data.SchemaVersion, data.Mode, data.Recordings, data.Scored)
	}
	if len(data.Fixtures) != 2 {
		t.Fatalf("fixtures = %d, want 2", len(data.Fixtures))
	}
	fx1 := data.Fixtures[0]
	if fx1.ID != "fx1" || fx1.Runs != 6 || fx1.Scored != 6 || fx1.Known != 5 || fx1.Bonus != 2 {
		t.Errorf("fx1 header = %+v", fx1)
	}
	wantPerRun := []float64{0, 0, 0.2, 1, 0.2, 0.2}
	if len(fx1.Recall.PerRun) != len(wantPerRun) {
		t.Fatalf("fx1 per_run = %v", fx1.Recall.PerRun)
	}
	for i, want := range wantPerRun {
		if fx1.Recall.PerRun[i] != want {
			t.Errorf("fx1 per_run[%d] = %v, want %v", i, fx1.Recall.PerRun[i], want)
		}
	}
	if fx1.Recall.Mean == nil || *fx1.Recall.Mean != 0.2667 {
		t.Errorf("fx1 mean = %v, want 0.2667", fx1.Recall.Mean)
	}
	if fx1.Recall.Union == nil || *fx1.Recall.Union != 1 {
		t.Errorf("fx1 union = %v, want 1", fx1.Recall.Union)
	}
	if got := joinLiteral(fx1.Recall.UnionHits); got != "K1,K2,K3,K4,K5" {
		t.Errorf("fx1 union_hits = %s", got)
	}
	if fx1.BonusUnion.Count != 2 || joinLiteral(fx1.BonusUnion.Hits) != "B1,B2" {
		t.Errorf("fx1 bonus_union = %+v", fx1.BonusUnion)
	}
	if fx1.FalseGreen.Passes != 3 || fx1.FalseGreen.Measured != 5 || fx1.FalseGreen.Unknown != 1 ||
		fx1.FalseGreen.Rate == nil || *fx1.FalseGreen.Rate != 0.6 {
		t.Errorf("fx1 false_green = %+v", fx1.FalseGreen)
	}
	if fx1.Hallucination.Rate != nil || fx1.Hallucination.Status != "stubbed" ||
		fx1.Hallucination.GatingFindings != 0 || fx1.Hallucination.Checked != 0 {
		t.Errorf("fx1 hallucination = %+v", fx1.Hallucination)
	}
	if fx1.ReviewInsufficient != 0 {
		t.Errorf("fx1 review_insufficient = %d, want 0", fx1.ReviewInsufficient)
	}
	fx2 := data.Fixtures[1]
	if fx2.ID != "fx2" || fx2.Runs != 1 || fx2.Known != 6 || fx2.Bonus != 0 {
		t.Errorf("fx2 header = %+v", fx2)
	}
	if len(fx2.Recall.PerRun) != 1 || fx2.Recall.PerRun[0] != 0.1667 ||
		fx2.Recall.Mean == nil || *fx2.Recall.Mean != 0.1667 ||
		fx2.Recall.Union == nil || *fx2.Recall.Union != 0.1667 {
		t.Errorf("fx2 recall = %+v", fx2.Recall)
	}
	if fx2.FalseGreen.Passes != 0 || fx2.FalseGreen.Measured != 1 || fx2.FalseGreen.Unknown != 0 ||
		fx2.FalseGreen.Rate == nil || *fx2.FalseGreen.Rate != 0 {
		t.Errorf("fx2 false_green = %+v", fx2.FalseGreen)
	}
	if data.Calibration.Rows != 7 || !data.Calibration.Reproduced || len(data.Calibration.Mismatches) != 0 {
		t.Errorf("calibration = %+v", data.Calibration)
	}
	if len(data.Diagnostics) != 7 {
		t.Errorf("diagnostics = %d, want 7", len(data.Diagnostics))
	}
	golden.Check(t, "fixtures-human", Human(data))

	rel, err := filepath.Rel(root, filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName))
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(".verdict", "backtest", fixturesDir, reportName)
	if filepath.ToSlash(rel) != filepath.ToSlash(wantPath) {
		t.Errorf("report path = %s, want %s", rel, wantPath)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName))
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

func joinLiteral(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func TestGatingFindingPredicate(t *testing.T) {
	trueValue, falseValue := true, false
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"important anchored", Finding{Severity: "important", Anchored: &trueValue}, true},
		{"critical anchored", Finding{Severity: "CRITICAL", Anchored: &trueValue}, true},
		{"important unanchored", Finding{Severity: "important", Anchored: &falseValue}, false},
		{"important missing anchor", Finding{Severity: "important"}, false},
		{"important unverified", Finding{Severity: "important", Anchored: &trueValue, Refusal: "unverified"}, false},
		{"minor anchored", Finding{Severity: "minor", Anchored: &trueValue}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGatingFinding(tc.f); got != tc.want {
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

func copyRecordings(t *testing.T) string {
	t.Helper()
	source := repoPath("bench", "quality", "recordings")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(dst, entry.Name())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			raw, err := os.ReadFile(filepath.Join(source, entry.Name(), file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, file.Name()), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dst
}

func rewriteMeta(t *testing.T, root, recording, field, value string) {
	t.Helper()
	path := filepath.Join(root, recording, metaName)
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
	rewriteMeta(t, root, "vr-sta440b-haiku", "infra_error", `"agent failed to start"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: repoPath(), RecordingsRoot: root})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Recordings != 7 || resp.Data.Scored != 6 {
		t.Errorf("header = %d recordings/%d scored, want 7/6", resp.Data.Recordings, resp.Data.Scored)
	}
	f := resp.Data.Fixtures[0]
	if f.Runs != 6 || f.Scored != 5 || len(f.Recall.PerRun) != 5 {
		t.Errorf("fx1 = %d runs/%d scored/%d per_run", f.Runs, f.Scored, len(f.Recall.PerRun))
	}
	if f.Recall.Union == nil || *f.Recall.Union != 0.4 || joinLiteral(f.Recall.UnionHits) != "K2,K5" {
		t.Errorf("fx1 union = %v hits %v, want 0.4 K2,K5", f.Recall.Union, f.Recall.UnionHits)
	}
	if f.FalseGreen.Measured != 4 || f.FalseGreen.Passes != 3 || f.FalseGreen.Unknown != 1 {
		t.Errorf("false green = %+v", f.FalseGreen)
	}
}

func TestReviewInsufficientExactCount(t *testing.T) {
	root := copyRecordings(t)
	rewriteMeta(t, root, "vr-sta440b-bench", "refusal_code", `"review-insufficient"`)
	rewriteMeta(t, root, "vr-sta440b-haiku2", "refusal_code", `"review-insufficient"`)
	rewriteMeta(t, root, "vr-sta351a-haiku", "refusal_code", `"missing-findings"`)
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: repoPath(), RecordingsRoot: root})
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
	resp, ref := RunFixtures(context.Background(), &Request{RepoRoot: repoPath(), FixtureID: "fx2"})
	if ref != nil {
		t.Fatalf("run: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Fixtures) != 1 || resp.Data.Fixtures[0].ID != "fx2" {
		t.Errorf("fixtures = %+v, want just fx2", resp.Data.Fixtures)
	}
	if resp.Data.Calibration.Rows != 7 || !resp.Data.Calibration.Reproduced {
		t.Errorf("calibration = %+v, want 7 rows still reproduced", resp.Data.Calibration)
	}
}
