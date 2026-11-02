package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestArityMatrix pins the front-door arity table (go-port 08, F11): no
// verb, a missing trend repo, or a missing repo/pr on any other
// positional verb is a usage error. Bare verbs never fail the gate.
func TestArityMatrix(t *testing.T) {
	t.Parallel()
	checkPositionalVerbs(t, []string{
		"fetch", "fetch-local", "record", "adopt", "fix", "post",
		"triage", "report", "next", "show", "verify", "calibrate",
		"ready",
	})
	checkBareVerbs(t, []string{"check-tree", "doctor", "learn", "init", "install", "upgrade", "uninstall"})
	checkTrendArity(t)
	checkUnknownArity(t)
}

// checkPositionalVerbs pins the repo/pr requirement matrix.
func checkPositionalVerbs(t *testing.T, verbs []string) {
	t.Helper()
	for _, verb := range verbs {
		checkArity(t, verb, "", "", true)
		checkArity(t, verb, "acme/app", "", true)
		checkArity(t, verb, "", "1", true)
		checkArity(t, verb, "acme/app", "1", false)
	}
}

// checkBareVerbs pins that bare verbs never fail the arity gate.
func checkBareVerbs(t *testing.T, verbs []string) {
	t.Helper()
	for _, verb := range verbs {
		checkArity(t, verb, "", "", false)
	}
}

// checkTrendArity pins trend's repo-only arity.
func checkTrendArity(t *testing.T) {
	t.Helper()
	checkArity(t, "trend", "", "", true)
	checkArity(t, "trend", "acme/app", "", false)
}

// checkUnknownArity pins the no-verb and unknown-verb fallbacks.
func checkUnknownArity(t *testing.T) {
	t.Helper()
	checkArity(t, "", "", "", true)
	checkArity(t, "bogus", "", "", true)
	checkArity(t, "bogus", "acme/app", "1", false)
}

// checkArity asserts one arityUsage result.
func checkArity(t *testing.T, verb, repo, pr string, want bool) {
	t.Helper()
	if got := arityUsage(verb, repo, pr); got != want {
		t.Errorf("arityUsage(%q, %q, %q) = %v, want %v", verb, repo, pr, got, want)
	}
}

// TestSplitCommandRest pins the positional/flag split per verb shape:
// bare verbs take no positionals (every arg is flags), trend takes only
// a repo, every other verb takes repo plus pr.
func TestSplitCommandRest(t *testing.T) {
	t.Parallel()
	checkSplit(t, []string{"check-tree", "--expect", "x"}, "check-tree", "", "", 2)
	checkSplit(t, []string{"trend", "acme/app", "--overlap"}, "trend", "acme/app", "", 1)
	checkSplit(t, []string{"report", "acme/app", "1", "--json"}, "report", "acme/app", "1", 1)
	checkSplit(t, nil, "", "", "", 0)
}

// checkSplit asserts one positional/flag split.
func checkSplit(t *testing.T, args []string, wantCmd, wantRepo, wantPR string, wantRest int) {
	t.Helper()
	cmd, repo, pr, rest := splitCommand(args)
	if cmd != wantCmd || repo != wantRepo || pr != wantPR || len(rest) != wantRest {
		t.Fatalf("split %v = %q %q %q %q, want %q %q %q %d",
			args, cmd, repo, pr, rest, wantCmd, wantRepo, wantPR, wantRest)
	}
}

// TestUnknownCmdMessage pins the shell refusal for a verb the CLI does
// not know, reached only when the arity gate passed.
func TestUnknownCmdMessage(t *testing.T) {
	t.Parallel()
	want := "unknown cmd 'bogus' (want " + verbsHint + ")"
	if got := unknownCmdMessage("bogus"); got != want {
		t.Fatalf("unknownCmdMessage = %q, want %q", got, want)
	}
}

// runCapture runs run() with piped stdio and a hermetic environment,
// returning the exit code plus captured stdout/stderr.
func runCapture(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	code := run(args, stdoutW, stderrW, func(string) string { return "" }, os.ReadFile)
	_ = stdoutW.Close()
	_ = stderrW.Close()
	out, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	derr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out), string(derr)
}

// hermeticEnv points every environment-read setting at empty temp dirs so
// the ambient developer environment never leaks into the run.
func hermeticEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{
		"NO_COLOR", "REVIEW_OUTPUT_FORMAT", "REVIEW_LAYOUT_COMPACT", "REVIEW_COLOR_MODE",
		"REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL", "REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL",
		"REVIEW_PUBLISH_DIAGRAMS_EXPANDED", "REVIEW_PUBLISH_FOOTER",
		"REVIEW_PUBLISH_ATTRIBUTION", "REVIEW_PUBLISH_ATTRIBUTION_TEXT",
		"REVIEW_PUBLISH_COPYRIGHT", "REVIEW_PUBLISH_LOGO", "REVIEW_PUBLISH_LOGO_URL",
		"REVIEW_PUBLISH_OVERVIEW", "REVIEW_MODEL",
		"REVIEW_MAX_BYTES", "REVIEW_BUDGET_USD", "REVIEW_PANEL",
		"FORGEJO_HOST", "FORGEJO_TOKEN",
		"BITBUCKET_TOKEN", "BITBUCKET_USERNAME", "BITBUCKET_PASSWORD",
		"AZURE_DEVOPS_ORG", "AZURE_DEVOPS_EXT_PAT",
		"REVIEW_KB_MIN_JUDGED", "REVIEW_KB_MAX_FP_RATE",
	} {
		t.Setenv(k, "")
	}
}

// TestUsageWinsOverCorruptSettings pins the F11 compound-fault rule: a
// missing positional prints the full usage block with exit 2 without
// reading the (corrupt) settings file first, while a complete invocation
// still fails typed bad-settings.
func TestUsageWinsOverCorruptSettings(t *testing.T) {
	hermeticEnv(t)
	writeCorruptSettings(t)
	code, out, derr := runCapture(t, []string{"report"})
	if code != cli.ExitUsage {
		t.Fatalf("missing pr exit = %d, want %d\nstdout: %s\nstderr: %s", code, cli.ExitUsage, out, derr)
	}
	if derr != usageText() {
		t.Fatalf("missing pr stderr is not the usage block:\n%s", derr)
	}
	code, _, derr = runCapture(t, nil)
	if code != cli.ExitUsage || derr != usageText() {
		t.Fatalf("no verb exit = %d stderr:\n%s", code, derr)
	}
	code, out, derr = runCapture(t, []string{"report", "acme/app", "1", "--json"})
	if code != cli.ExitRefused || !strings.Contains(out, `"code": "bad-settings"`) {
		t.Fatalf("complete invocation exit = %d, want bad-settings envelope:\nstdout: %s\nstderr: %s", code, out, derr)
	}
}

// writeCorruptSettings writes an unparsable settings.json into the
// hermetic config dir.
func writeCorruptSettings(t *testing.T) {
	t.Helper()
	bad := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "verdict")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "settings.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
}
