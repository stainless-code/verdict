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

const testPairHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestTrendGroupsByAuthorReviewerPair proves the per-bundle rows carry the
// recorded pair and the rollup groups by it, with a bundle that recorded no
// fingerprint landing under unknown.
func TestTrendGroupsByAuthorReviewerPair(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTrendPairBundle(t, "local-claude", "[]",
		`{"recorded_at":"2026-03-01T00:00:00Z","author":{"agent":"claude","model":"Opus 4.5"},"review_models":["gpt-5.1-codex"]}`)
	writeTrendPairBundle(t, "local-codex",
		`[{"kind":"inline","path":"b.go","side":"RIGHT","line":2,"anchored":true,"body":"Fixed race condition.","rigor":"walked","severity":"important","sig":"2222222222222222222222222222222222222222","sig_version":"1","status":"addressed"}]`,
		`{"recorded_at":"2026-03-02T00:00:00Z","author":{"agent":"codex","model":null},"review_models":["claude-haiku-4-5","claude-opus-4-5"]}`)
	writeTrendPairBundle(t, "local-unknown", "[]", `{"recorded_at":"2026-03-03T00:00:00Z"}`)

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
	assertPairRows(t, data)
	wantPairs := `{"claude/gpt-5.1-codex":{"addressed":0,"bundles":1,"closed":0,"findings":0,"open":0},` +
		`"codex/claude-haiku-4-5":{"addressed":1,"bundles":1,"closed":0,"findings":1,"open":0},` +
		`"codex/claude-opus-4-5":{"addressed":1,"bundles":1,"closed":0,"findings":1,"open":0},` +
		`"unknown/unknown":{"addressed":0,"bundles":1,"closed":0,"findings":0,"open":0}}`
	if got := jq.Compact(data["by_pair"]); got != wantPairs {
		t.Errorf("by_pair = %s\nwant %s", got, wantPairs)
	}
	human := strings.Join(resp.Human, "\n")
	wantLine := "pairs: claude/gpt-5.1-codex=1(0f/0o/0a/0c) codex/claude-haiku-4-5=1(1f/0o/1a/0c) " +
		"codex/claude-opus-4-5=1(1f/0o/1a/0c) unknown/unknown=1(0f/0o/0a/0c)"
	if !strings.Contains(human, wantLine) {
		t.Errorf("human missing %q:\n%s", wantLine, human)
	}
}

// TestTrendCountsADuplicateReviewModelOnce proves a hand-edited record naming
// the same reviewer twice still counts its bundle once for that pair.
func TestTrendCountsADuplicateReviewModelOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTrendPairBundle(t, "local-dup", "[]",
		`{"recorded_at":"2026-03-01T00:00:00Z","author":{"agent":"claude","model":null},"review_models":["gpt-5.1-codex","gpt-5.1-codex"]}`)
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
	want := `{"claude/gpt-5.1-codex":{"addressed":0,"bundles":1,"closed":0,"findings":0,"open":0}}`
	if got := jq.Compact(data["by_pair"]); got != want {
		t.Errorf("by_pair = %s, want %s", got, want)
	}
}

func assertPairRows(t *testing.T, data map[string]any) {
	t.Helper()
	rows, ok := data["per_bundle"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("per_bundle = %v, want 3 rows", data["per_bundle"])
	}
	want := map[string]string{
		"local-claude":  `{"author":"claude","review_models":["gpt-5.1-codex"]}`,
		"local-codex":   `{"author":"codex","review_models":["claude-haiku-4-5","claude-opus-4-5"]}`,
		"local-unknown": `{"author":"unknown","review_models":[]}`,
	}
	for _, raw := range rows {
		assertPairRow(t, raw, want)
	}
}

func assertPairRow(t *testing.T, raw any, want map[string]string) {
	t.Helper()
	r, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("row = %v, want an object", raw)
	}
	name, ok := r["bundle"].(string)
	if !ok {
		t.Fatalf("row bundle = %v, want a name", r["bundle"])
	}
	got := jq.Compact(map[string]any{"author": r["author"], "review_models": r["review_models"]})
	if got != want[name] {
		t.Errorf("%s pair = %s, want %s", name, got, want[name])
	}
}

func writeTrendPairBundle(t *testing.T, name, findings, record string) {
	t.Helper()
	dir := filepath.Join(".verdict/state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"source.json":   `{"head_sha":"` + testPairHead + `","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		"findings.json": findings,
		"record.json":   record,
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
