package schemagen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	backtestcore "github.com/stainless-code/verdict/internal/core/backtest"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRuntimeBacktestScoreEnvelopeValidates proves the score envelope's data
// block validates against the published backtest profile, and that
// report.json is that same object: one shape, one schema.
func TestRuntimeBacktestScoreEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	artifact := loadSchemaDocument(t, "backtest.schema.json")
	root := t.TempDir()
	writeScoreRun(t, root)
	resp, ref := backtestcore.RunScore(context.Background(), &backtestcore.ScoreRequest{
		Runtime: fixedRuntime(), RepoRoot: root, Run: "s1",
	})
	if ref != nil {
		t.Fatalf("score refusal: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data.Wire()
	assertWireFields(t, data, map[string]any{
		"schema_version":      "1",
		"run":                 "s1",
		"mode":                "score",
		"prs":                 json.Number("1"),
		"not_replayed":        json.Number("0"),
		"bugs":                json.Number("1"),
		"reported":            json.Number("1"),
		"matched":             json.Number("1"),
		"unclassified":        json.Number("0"),
		"catch_rate":          json.Number("1"),
		"off_target_per_pr":   json.Number("0"),
		"cost_per_review_usd": json.Number("0.25"),
	})
	spend := wireSubMap(t, data, "spend")
	assertWireFields(t, spend, map[string]any{
		"usd": json.Number("0.25"), "priced_reviews": json.Number("1"), "unpriced_reviews": json.Number("0"),
	})
	signals := wireList(t, data, "signals")
	if len(signals) != 2 {
		t.Fatalf("signals = %v, want fix and revert rows", signals)
	}
	assertEnvelope(t, doc, "backtest", "", resp.At, resp.Next, resp.Warnings, data)
	validateWire(t, artifact, "report.json", artifact, decodeWire(t, data))
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "backtest", "s1", "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	if string(raw) != jq.Pretty(resp.Data) {
		t.Errorf("report.json differs from the envelope data:\n%s\n---\n%s", raw, jq.Pretty(resp.Data))
	}
}

// writeScoreRun plants one replayed PR whose review names the mined bug.
func writeScoreRun(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".verdict", "backtest", "s1", "mine.json"),
		`{"schema_version":"1","method":"szz-blame-v1","candidates":[`+
			`{"head":"1111111111111111111111111111111111111111","base":"2222222222222222222222222222222222222222",`+
			`"landed_by":"3333333333333333333333333333333333333333","pr":2,"reconstruction":"merge-parent",`+
			`"head_source":"merge-time","merged_at":"2024-01-03T00:00:00Z","head_at":"2024-01-02T00:00:00Z",`+
			`"fixes":["4444444444444444444444444444444444444444"],"bugs":[{"path":"main.go","line":6,`+
			`"kind":"missing-error-check","signal":"fix","fix":"4444444444444444444444444444444444444444"}]}],`+
			`"skipped":[]}`)
	writeFixture(t, filepath.Join(root, ".verdict", "backtest", "s1", "prs", "001", "replay.json"),
		`{"schema_version":"1","run":"s1","head":"1111111111111111111111111111111111111111",`+
			`"base":"2222222222222222222222222222222222222222","agent":"claude","status":"ok",`+
			`"bundle":"local-bt-1-s1","at":"2024-02-01T12:00:00Z","pr":2,"cost_usd":0.25,"n":1,`+
			`"pass_through":[]}`)
	writeFixture(t, filepath.Join(root, ".verdict", "state", "local-bt-1-s1", "findings.json"),
		`[{"kind":"inline","path":"main.go","side":"RIGHT","line":6,"anchored":true,`+
			`"body":"The removal is unchecked.","suggestion":null,"rigor":"walked",`+
			`"severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status":"open"}]`)
}
