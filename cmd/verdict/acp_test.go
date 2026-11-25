package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestACPDispatchShape pins the two facts the transport needs: acp is a bare
// verb whose flags pass through, and its table row has no handler (a handler
// would die on not-a-repo before --root was read).
func TestACPDispatchShape(t *testing.T) {
	t.Parallel()
	if spec := verbIndex["acp"]; spec == nil || spec.Run != nil {
		t.Fatal("acp has a table handler; the transport must dispatch before resolveRepoRoot")
	}
	if !bareVerbs["acp"] {
		t.Fatal("acp is not a bareVerbs entry; splitCommand would read --root as a positional")
	}
}

// TestACPUsageNamesEveryMethod pins the usage block to the method surface the
// transport actually answers, so help cannot drift from dispatch.
func TestACPUsageNamesEveryMethod(t *testing.T) {
	t.Parallel()
	text := acpUsageText()
	for _, method := range []string{
		"initialize", "ping", "verdict/bundles/list", "verdict/findings/list",
		"verdict/findings/get", "verdict/findings/copy", "$/cancel_request",
		"--root <dir>", "--check <tier>", "--max-bundles <n>",
	} {
		if !strings.Contains(text, method) {
			t.Errorf("acp usage misses %q:\n%s", method, text)
		}
	}
}

// TestACPHelpIsExitOK proves `verdict acp --help` prints the transport
// contract on stdout with exit 0, before any root probe.
func TestACPHelpIsExitOK(t *testing.T) {
	code, out, derr := runCapture(t, []string{"acp", "--help"})
	if code != cli.ExitOK || derr != "" {
		t.Fatalf("acp --help = exit %d stderr %q", code, derr)
	}
	for _, want := range []string{"usage: verdict acp", "verdict/bundles/list", "no model calls"} {
		if !strings.Contains(out, want) {
			t.Errorf("acp --help misses %q:\n%s", want, out)
		}
	}
}

// TestACPUnknownFlagIsUsage proves an unknown transport flag is a usage
// error, never a silent ignore.
func TestACPUnknownFlagIsUsage(t *testing.T) {
	code, _, derr := runCapture(t, []string{"acp", "--bogus"})
	if code != cli.ExitUsage {
		t.Fatalf("acp --bogus = exit %d, want %d\nstderr: %s", code, cli.ExitUsage, derr)
	}
	if !strings.Contains(derr, "unknown flag '--bogus' for acp") {
		t.Errorf("acp --bogus stderr = %q, want the unknown-flag refusal", derr)
	}
}

// TestACPBadRootIsRefusal proves a --root that is not a directory refuses
// before the server starts.
func TestACPBadRootIsRefusal(t *testing.T) {
	file := writeTempFile(t)
	code, out, derr := runCapture(t, []string{"acp", "--root", file, "--json"})
	if code != cli.ExitRefused {
		t.Fatalf("acp --root <file> = exit %d, want %d\nstdout: %s\nstderr: %s", code, cli.ExitRefused, out, derr)
	}
	if !strings.Contains(out, `"code": "bad-root"`) {
		t.Errorf("stdout: %s\nstderr: %s, want a bad-root envelope", out, derr)
	}
}

// writeTempFile creates one file and returns its path.
func writeTempFile(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "not-a-dir"
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestACPUsageIsInGlobalUsage proves the global block names acp, so the
// verb twin guard sees one spelling everywhere.
func TestACPUsageIsInGlobalUsage(t *testing.T) {
	t.Parallel()
	synopsis, _, _ := strings.Cut(usageText(), "\n")
	if !strings.Contains(synopsis, "|acp") {
		t.Fatalf("synopsis misses acp: %s", synopsis)
	}
}
