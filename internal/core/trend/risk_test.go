package trendcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestTrendRiskBands(t *testing.T) {
	t.Chdir(t.TempDir())
	seedTrendRiskBundles(t)

	var req Request
	req.Runtime = &core.Runtime{Now: func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }}
	req.Repo = "acme/app"
	resp, ref := Run(context.Background(), &req)
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(jq.PrettyOrdered(resp.Data.Wire())), &data); err != nil {
		t.Fatal(err)
	}
	assertTrendRiskBands(t, data)

	human := strings.Join(resp.Human, "\n")
	wantLine := "risk: low=1(0f/0o/0a/0c) medium=1(1f/0o/0a/1c) high=1(1f/0o/1a/0c) critical=1(1f/1o/0a/0c) unknown=1(0f/0o/0a/0c)"
	if !strings.Contains(human, wantLine) {
		t.Errorf("human missing %q:\n%s", wantLine, human)
	}
}

const testTrendRiskHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func seedTrendRiskBundles(t *testing.T) {
	t.Helper()
	writeTrendRiskBundle(t, "local-low", "[]",
		`{"head":"`+testTrendRiskHead+`","posted_at":"2026-03-01T00:00:00Z","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Comment-only change.","assessed_head":"`+testTrendRiskHead+`"}}`)
	writeTrendRiskBundle(t, "local-overridden",
		`[{"kind":"inline","path":"a.go","side":"RIGHT","line":1,"anchored":true,"body":"Critical bug in handler.","rigor":"walked","severity":"critical","sig":"1111111111111111111111111111111111111111","sig_version":"1","status":"open"}]`,
		`{"head":"`+testTrendRiskHead+`","posted_at":"2026-03-02T00:00:00Z","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Claimed low erroneously.","assessed_head":"`+testTrendRiskHead+`"}}`)
	writeTrendRiskBundle(t, "local-high",
		`[{"kind":"inline","path":"b.go","side":"RIGHT","line":2,"anchored":true,"body":"Fixed race condition.","rigor":"walked","severity":"important","sig":"2222222222222222222222222222222222222222","sig_version":"1","status":"addressed"}]`,
		`{"head":"`+testTrendRiskHead+`","posted_at":"2026-03-03T00:00:00Z","coverage":{"gaps":0},"risk":{"level":"high","rationale":"Concurrency state change.","assessed_head":"`+testTrendRiskHead+`"}}`)
	writeTrendRiskBundle(t, "local-unclaimed-medium",
		`[{"kind":"inline","path":"c.go","side":"RIGHT","line":3,"anchored":true,"body":"Deferred cleanup.","rigor":"said","severity":"informational","sig":"3333333333333333333333333333333333333333","sig_version":"1","status":"deferred"}]`,
		"")
	writeTrendRiskBundle(t, "local-unknown", "[]",
		`{"head":"`+testTrendRiskHead+`","posted_at":"2026-03-04T00:00:00Z","coverage":{"gaps":0}}`)
}

func writeTrendRiskBundle(t *testing.T, name, findings, post string) {
	t.Helper()
	dir := filepath.Join(".verdict/state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.json"),
		[]byte(`{"head_sha":"`+testTrendRiskHead+`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
	if post == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(post), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertTrendRiskBands(t *testing.T, data map[string]any) {
	t.Helper()
	byRisk, ok := data["by_risk"].(map[string]any)
	if !ok {
		t.Fatalf("by_risk missing: %#v", data)
	}
	wantBands := map[string]map[string]float64{
		"low":      {"bundles": 1, "findings": 0, "open": 0, "addressed": 0, "closed": 0},
		"medium":   {"bundles": 1, "findings": 1, "open": 0, "addressed": 0, "closed": 1},
		"high":     {"bundles": 1, "findings": 1, "open": 0, "addressed": 1, "closed": 0},
		"critical": {"bundles": 1, "findings": 1, "open": 1, "addressed": 0, "closed": 0},
		"unknown":  {"bundles": 1, "findings": 0, "open": 0, "addressed": 0, "closed": 0},
	}
	for band, want := range wantBands {
		assertTrendBand(t, byRisk, band, want)
	}
}

func assertTrendBand(t *testing.T, byRisk map[string]any, band string, want map[string]float64) {
	t.Helper()
	got, ok := byRisk[band].(map[string]any)
	if !ok {
		t.Fatalf("by_risk[%s] type = %T, want map", band, byRisk[band])
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("by_risk[%s][%s] = %v, want %v", band, k, got[k], w)
		}
	}
}
