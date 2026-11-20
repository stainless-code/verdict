package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestHumanReproductionWarnings(t *testing.T) {
	for _, verb := range []string{"report", "show"} {
		t.Run(verb, func(t *testing.T) { checkHumanReproductionWarnings(t, verb) })
	}
}

func checkHumanReproductionWarnings(t *testing.T, verb string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := filepath.Join(".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeReproductionFinding(t, dir)
	var out, diag bytes.Buffer
	runner := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Cmd: verb, Effective: cli.Defaults(), Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }}
	runner.Warn("inherited warning")
	req := Request{Repo: "acme/app", Pr: "7"}
	var code int
	if verb == "show" {
		code = RunShow(t.Context(), runner, []string{"--sig", "aaaaaaaa"}, req)
	} else {
		code = Run(t.Context(), runner, nil, req)
	}
	checkHumanWarningOutput(t, code, diag.String())
}

func writeReproductionFinding(t *testing.T, dir string) {
	t.Helper()
	const raw = `[{"kind":"inline","path":"notes.txt","line":2,"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","rigor":"reproduced","status":"open","body":"A finding."}]`
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkHumanWarningOutput(t *testing.T, code int, diag string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, diag)
	}
	if strings.Count(diag, "has no runnable artifact") != 1 || strings.Count(diag, "inherited warning") != 1 {
		t.Fatalf("warnings missing or duplicated: %s", diag)
	}
}
