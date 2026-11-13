package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	ingestcore "github.com/stainless-code/verdict/internal/core/ingest"
)

func setupCLIBundle(t *testing.T, pr string) (repoRoot, dir string) {
	t.Helper()
	repoRoot = t.TempDir()
	dir = filepath.Join(repoRoot, ".verdict", "state", pr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"headRefOid":"cafe123456"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,0 +1,3 @@\n+line1\n+line2\n+line3\n"
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoRoot, dir
}

func TestRun_StdinAndJSONEnvelope(t *testing.T) {
	repoRoot, _ := setupCLIBundle(t, "12")
	var out, diag, human bytes.Buffer
	r := &cli.Runner{
		Cmd:      "ingest",
		JSONMode: true,
		Out:      &out,
		Diag:     &diag,
		Human:    &human,
		Now:      func() time.Time { return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC) },
	}
	sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec"}},"results":[{"ruleId":"G101","level":"error","message":{"text":"Hardcoded secret"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"app.go"},"region":{"startLine":2}}}]}]}]}`
	code := Run(context.Background(), r, []string{"--sarif", "-"}, Request{
		Repo:     "o/r",
		Pr:       "12",
		RepoRoot: repoRoot,
		Stdin:    strings.NewReader(sarif),
	})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; diag=%s", code, diag.String())
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, out.String())
	}
	if env["verb"] != "ingest" || env["error"] != nil {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	data := env["data"].(map[string]any)
	if data["ingested"] != float64(1) || data["added"] != float64(1) || data["anchored"] != float64(1) {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestRun_StdinTooLargeRefused(t *testing.T) {
	repoRoot, _ := setupCLIBundle(t, "13")
	var out, diag, human bytes.Buffer
	r := &cli.Runner{
		Cmd:      "ingest",
		JSONMode: true,
		Out:      &out,
		Diag:     &diag,
		Human:    &human,
		Now:      func() time.Time { return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC) },
	}
	oversize := make([]byte, ingestcore.MaxSARIFBytes+5)
	code := Run(context.Background(), r, []string{"--sarif", "-"}, Request{
		Repo:     "o/r",
		Pr:       "13",
		RepoRoot: repoRoot,
		Stdin:    bytes.NewReader(oversize),
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if !strings.Contains(out.String(), `"sarif-too-large"`) {
		t.Fatalf("expected sarif-too-large refusal, got %s", out.String())
	}
}
