package report

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestUnknownFlagListsAllReportFlags(t *testing.T) {
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--bogus"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d; want %d", code, cli.ExitUsage)
	}
	got := diag.String()
	for _, flag := range []string{
		"--status", "--kind", "--category", "--output", "--sarif",
		"--include-withheld", "--cost", "--risk", "--max-risk", "--check", "--completeness", "--live", "--json",
	} {
		if !strings.Contains(got, flag) {
			t.Errorf("unknown-flag message %q missing %s", got, flag)
		}
	}
}

// TestRentryKeepsAnEmptyArgument proves the re-enter line a refusal prints
// survives an empty flag value. Dropping it would close the gap the operator
// never typed: `--status` would stand bare, and with `--json` already appended
// the value that follows would move onto the wrong token.
func TestRentryKeepsAnEmptyArgument(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Run("human", func(t *testing.T) {
		assertRentry(t, false, "re-enter: verdict report acme/app 1 --status ''\n")
	})
	t.Run("json", func(t *testing.T) {
		assertRentry(t, true, "re-enter: verdict report acme/app 1 --status '' --json\n")
	})
}

// assertRentry runs one report refusal with an empty --status value and pins
// the re-enter line it prints.
func assertRentry(t *testing.T, jsonMode bool, want string) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, JSONMode: jsonMode,
		Cmd: "report", Repo: "acme/app", Pr: "1", Effective: cli.Defaults(),
		Now: func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}
	code := Run(context.Background(), r, []string{"--status", ""}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d; want %d (a missing bundle refuses)", code, cli.ExitRefused)
	}
	if got := diag.String(); !strings.Contains(got, want) {
		t.Errorf("diag = %q; want it to carry %q", got, want)
	}
}

const testRiskHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func setupRiskCLIBundle(t *testing.T, postJSON string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/local-risk-cli"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"source.json":   `{"head_sha":"` + testRiskHeadSHA + `","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		"findings.json": "[]",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if postJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(postJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportMaxRiskOK(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Isolated helper refactor with tests.","assessed_head":"`+testRiskHeadSHA+`"}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "low"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestReportMaxRiskExceeded(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"high","rationale":"Touches core state machine.","assessed_head":"`+testRiskHeadSHA+`"}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "medium"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out.String(), diag.String())
	}
}

func TestReportMaxRiskUnknown(t *testing.T) {
	setupRiskCLIBundle(t, `{"head":"`+testRiskHeadSHA+`","coverage":{"gaps":0}}`)
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true}
	code := Run(context.Background(), r, []string{"--max-risk", "critical"}, Request{Repo: "acme/app", Pr: "local-risk-cli"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1; out=%s diag=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), `"risk-unknown"`) {
		t.Errorf("json output missing risk-unknown: %s", out.String())
	}
}

func TestReportMaxRiskInvalid(t *testing.T) {
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--max-risk", "bogus"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want 2; diag=%s", code, diag.String())
	}
}

func TestReportMaxRiskMissingStore(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--max-risk", "low"}, Request{Repo: "acme/app", Pr: "missing-bundle"})
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want 3; out=%s diag=%s", code, out.String(), diag.String())
	}
}
