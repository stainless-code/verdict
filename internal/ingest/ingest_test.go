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
	"github.com/stainless-code/verdict/internal/readcommon"
)

func setupCLIBundle(t *testing.T, pr string) string {
	t.Helper()
	repoRoot := t.TempDir()
	dir := filepath.Join(repoRoot, ".verdict", "state", pr)
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
	return repoRoot
}

func TestRun_StdinAndJSONEnvelope(t *testing.T) {
	repoRoot := setupCLIBundle(t, "12")
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
	code := run(context.Background(), r, []string{"--sarif", "-"}, &readcommon.Request{
		Repo:     "o/r",
		Pr:       "12",
		RepoRoot: repoRoot,
	}, strings.NewReader(sarif))
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
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not object: %T", env["data"])
	}
	if data["ingested"] != float64(1) || data["added"] != float64(1) || data["anchored"] != float64(1) {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestRun_StdinTooLargeRefused(t *testing.T) {
	repoRoot := setupCLIBundle(t, "13")
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
	code := run(context.Background(), r, []string{"--sarif", "-"}, &readcommon.Request{
		Repo:     "o/r",
		Pr:       "13",
		RepoRoot: repoRoot,
	}, bytes.NewReader(oversize))
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if !strings.Contains(out.String(), `"sarif-too-large"`) {
		t.Fatalf("expected sarif-too-large refusal, got %s", out.String())
	}
}

const fromSARIFDoc = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"opengrep"}},"results":[{"ruleId":"r.one","level":"error","message":{"text":"Anchored issue"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"app.go"},"region":{"startLine":2}}}]}]}]}`

func newIngestRunner() (*cli.Runner, *bytes.Buffer, *bytes.Buffer) {
	var out, diag, human bytes.Buffer
	return &cli.Runner{
		Cmd:      "ingest",
		JSONMode: true,
		Out:      &out,
		Diag:     &diag,
		Human:    &human,
		Now:      func() time.Time { return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC) },
	}, &out, &diag
}

// TestRun_FromSarifReadsFile proves `--from sarif <file>` reads a path and
// publishes the empty skipped array a clean document produces.
func TestRun_FromSarifReadsFile(t *testing.T) {
	repoRoot := setupCLIBundle(t, "21")
	sarifPath := filepath.Join(t.TempDir(), "scan.sarif")
	if err := os.WriteFile(sarifPath, []byte(fromSARIFDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFromIngest(t, repoRoot, "21", []string{"--from", "sarif", sarifPath})
}

// TestRun_FromSarifEqualsReadsStdin proves the `--from=<format>` form and `-`
// reach the same source.
func TestRun_FromSarifEqualsReadsStdin(t *testing.T) {
	assertFromIngest(t, setupCLIBundle(t, "21"), "21", []string{"--from=sarif", "-"})
}

func assertFromIngest(t *testing.T, repoRoot, pr string, args []string) {
	t.Helper()
	r, out, diag := newIngestRunner()
	code := run(context.Background(), r, args, &readcommon.Request{
		Repo:     "o/r",
		Pr:       pr,
		RepoRoot: repoRoot,
	}, strings.NewReader(fromSARIFDoc))
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; diag=%s out=%s", code, diag.String(), out.String())
	}
	if !strings.Contains(out.String(), `"ingested": 1`) {
		t.Fatalf("envelope = %s, want one ingested result", out.String())
	}
	if !strings.Contains(out.String(), `"skipped": []`) {
		t.Fatalf("envelope = %s, want an empty skipped array", out.String())
	}
}

// TestRun_FromRefusals proves the source gate fails closed: an unsupported
// format refuses typed (bad-from); an empty format, a repeated or conflicting
// spelling, a bare path, and an unknown single-dash flag are usage errors
// instead of silently meaning something else.
func TestRun_FromRefusals(t *testing.T) {
	cases := []struct {
		name string
		code string
		diag string
		args []string
		exit int
	}{
		{name: "unsupported format", args: []string{"--from", "yaml", "-"}, code: "bad-from", exit: cli.ExitRefused},
		{name: "empty format", args: []string{"--from", "", "-"}, diag: "--from needs a format", exit: cli.ExitUsage},
		{name: "whitespace format", args: []string{"--from", "   ", "-"}, diag: "--from needs a format", exit: cli.ExitUsage},
		{name: "empty equals format", args: []string{"--from=", "-"}, diag: "--from needs a format", exit: cli.ExitUsage},
		{name: "missing source", args: []string{"--from", "sarif"}, diag: "--from sarif needs <file|->", exit: cli.ExitUsage},
		{name: "duplicate from", args: []string{"--from", "sarif", "-", "--from", "sarif", "-"}, diag: "--from was given twice", exit: cli.ExitUsage},
		{name: "duplicate sarif", args: []string{"--sarif", "-", "--sarif", "-"}, diag: "--sarif was given twice", exit: cli.ExitUsage},
		{name: "from then sarif", args: []string{"--from", "sarif", "--sarif", "scan.sarif"}, diag: "--from and --sarif name one source", exit: cli.ExitUsage},
		{name: "sarif then from", args: []string{"--sarif", "scan.sarif", "--from", "sarif"}, diag: "--from and --sarif name one source", exit: cli.ExitUsage},
		{name: "bare path", args: []string{"scan.sarif"}, diag: "name the source as --from sarif scan.sarif", exit: cli.ExitUsage},
		{name: "bare stdin", args: []string{"-"}, diag: "name the source as --from sarif -", exit: cli.ExitUsage},
		{name: "single dash flag", args: []string{"-v"}, diag: "unknown flag '-v'", exit: cli.ExitUsage},
		{name: "single dash from", args: []string{"-f", "sarif"}, diag: "unknown flag '-f'", exit: cli.ExitUsage},
		{name: "long unknown flag", args: []string{"--nope", "-"}, diag: "unknown flag '--nope'", exit: cli.ExitUsage},
		{name: "two paths", args: []string{"--from", "sarif", "-", "extra.sarif"}, diag: "unexpected argument", exit: cli.ExitUsage},
		{name: "path after sarif", args: []string{"--sarif", "scan.sarif", "extra"}, diag: "unexpected argument", exit: cli.ExitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertIngestRefusal(t, tc.args, tc.code, tc.diag, tc.exit)
		})
	}
}

func assertIngestRefusal(t *testing.T, args []string, code, diag string, exit int) {
	t.Helper()
	r, out, diagnostics := newIngestRunner()
	got := run(context.Background(), r, args, &readcommon.Request{
		Repo:     "o/r",
		Pr:       "22",
		RepoRoot: setupCLIBundle(t, "22"),
	}, strings.NewReader(fromSARIFDoc))
	if got != exit {
		t.Fatalf("exit = %d, want %d; out=%s diag=%s", got, exit, out.String(), diagnostics.String())
	}
	if code != "" && !strings.Contains(out.String(), `"`+code+`"`) {
		t.Fatalf("out = %s, want code %s", out.String(), code)
	}
	if diag != "" && !strings.Contains(diagnostics.String(), diag) {
		t.Fatalf("diag = %q, want %q", diagnostics.String(), diag)
	}
}
