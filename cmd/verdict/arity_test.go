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
	positional := []string{
		"fetch", "fetch-local", "record", "adopt", "fix", "post",
		"triage", "report", "next", "show", "verify", "calibrate",
		"ready",
	}
	for _, verb := range positional {
		if !arityUsage(verb, "", "") {
			t.Errorf("arityUsage(%q, missing) = false, want true", verb)
		}
		if !arityUsage(verb, "acme/app", "") {
			t.Errorf("arityUsage(%q, repo only) = false, want true", verb)
		}
		if !arityUsage(verb, "", "1") {
			t.Errorf("arityUsage(%q, pr only) = false, want true", verb)
		}
		if arityUsage(verb, "acme/app", "1") {
			t.Errorf("arityUsage(%q, full) = true, want false", verb)
		}
	}
	for _, verb := range []string{"check-tree", "doctor", "learn", "init", "install"} {
		if arityUsage(verb, "", "") {
			t.Errorf("arityUsage(%q, bare) = true, want false", verb)
		}
	}
	if !arityUsage("trend", "", "") {
		t.Error("arityUsage(trend, missing repo) = false, want true")
	}
	if arityUsage("trend", "acme/app", "") {
		t.Error("arityUsage(trend, repo) = true, want false")
	}
	if !arityUsage("", "", "") {
		t.Error("arityUsage(no verb) = false, want true")
	}
	if !arityUsage("bogus", "", "") {
		t.Error("arityUsage(unknown, missing) = false, want true")
	}
	if arityUsage("bogus", "acme/app", "1") {
		t.Error("arityUsage(unknown, full) = true, want false")
	}
}

// TestSplitCommandRest pins the positional/flag split per verb shape:
// bare verbs take no positionals (every arg is flags), trend takes only
// a repo, every other verb takes repo plus pr.
func TestSplitCommandRest(t *testing.T) {
	t.Parallel()
	cmd, repo, pr, rest := splitCommand([]string{"check-tree", "--expect", "x"})
	if cmd != "check-tree" || repo != "" || pr != "" || len(rest) != 2 {
		t.Fatalf("check-tree split = %q %q %q %q, want verb plus 2 flag args", cmd, repo, pr, rest)
	}
	cmd, repo, pr, rest = splitCommand([]string{"trend", "acme/app", "--overlap"})
	if cmd != "trend" || repo != "acme/app" || pr != "" || len(rest) != 1 {
		t.Fatalf("trend split = %q %q %q %q, want repo plus 1 flag arg", cmd, repo, pr, rest)
	}
	cmd, repo, pr, rest = splitCommand([]string{"report", "acme/app", "1", "--json"})
	if cmd != "report" || repo != "acme/app" || pr != "1" || len(rest) != 1 {
		t.Fatalf("report split = %q %q %q %q, want repo/pr plus 1 flag arg", cmd, repo, pr, rest)
	}
	cmd, repo, pr, rest = splitCommand(nil)
	if cmd != "" || repo != "" || pr != "" {
		t.Fatalf("empty split = %q %q %q, want all empty", cmd, repo, pr)
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
	out, _ := io.ReadAll(stdoutR)
	derr, _ := io.ReadAll(stderrR)
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
	bad := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "verdict")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "settings.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
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
