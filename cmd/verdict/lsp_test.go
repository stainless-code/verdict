package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestLSPDispatchShape pins the registries the transport needs: lsp is a
// bare verb whose flags pass through, owns its help, and is not a read verb
// (a read row would die on not-a-repo before --root was read).
func TestLSPDispatchShape(t *testing.T) {
	t.Parallel()
	if _, ok := readVerbs["lsp"]; ok {
		t.Fatal("lsp is a readVerbs entry; the transport must dispatch before resolveRepoRoot")
	}
	if !bareVerbs["lsp"] || !ownHelpVerbs["lsp"] {
		t.Fatalf("lsp bareVerbs=%v ownHelpVerbs=%v, want both", bareVerbs["lsp"], ownHelpVerbs["lsp"])
	}
	synopsis, _, _ := strings.Cut(usageText(), "\n")
	if !strings.Contains(synopsis, "|lsp") {
		t.Fatalf("synopsis misses lsp: %s", synopsis)
	}
}

// TestLSPHelpIsExitOK proves `verdict lsp --help` prints the transport
// contract on stdout with exit 0, before any root probe.
func TestLSPHelpIsExitOK(t *testing.T) {
	code, out, derr := runCapture(t, []string{"lsp", "--help"})
	if code != cli.ExitOK || derr != "" {
		t.Fatalf("lsp --help = exit %d stderr %q", code, derr)
	}
	for _, want := range []string{
		"usage: verdict lsp", "publishDiagnostics", "verdict.refresh", "never on didChange",
		"--root <dir>", "--bundle <name>", "--max-roots <n>", "--interval <duration>", "--no-watch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lsp --help misses %q:\n%s", want, out)
		}
	}
}

// TestLSPArgvRefusals pins every argv-shape refusal as exit 2 with its
// message: no flag is silently ignored and no bound is soft.
func TestLSPArgvRefusals(t *testing.T) {
	dir := t.TempDir()
	tooMany := []string{"lsp"}
	for range 3 {
		tooMany = append(tooMany, "--root", dir)
	}
	tooMany = append(tooMany, "--max-roots", "2")
	for _, tc := range []struct {
		want string
		args []string
	}{
		{"unknown flag '--bogus' for lsp", []string{"lsp", "--bogus"}},
		{"unknown flag '--allow-run' for lsp", []string{"lsp", "--allow-run"}},
		{"unknown flag '--read-only' for lsp", []string{"lsp", "--read-only"}},
		{"--check severe wants", []string{"lsp", "--check", "severe"}},
		{"--max-roots 17 is not a count from 1 to 16", []string{"lsp", "--max-roots", "17"}},
		{"--max-bundles 0 is not a positive count", []string{"lsp", "--max-bundles", "0"}},
		{"--interval wants a duration of at least 100ms", []string{"lsp", "--interval", "10ms"}},
		{"--sweep must be at least --interval", []string{"lsp", "--interval", "2s", "--sweep", "1s"}},
		{"--bundle wants a non-empty bundle name", []string{"lsp", "--bundle", ""}},
		{"3 --root values exceed --max-roots 2", tooMany},
	} {
		code, _, derr := runCapture(t, tc.args)
		if code != cli.ExitUsage || !strings.Contains(derr, tc.want) {
			t.Errorf("%v = exit %d stderr %q, want exit %d and %q", tc.args, code, derr, cli.ExitUsage, tc.want)
		}
	}
}

// TestLSPStartRefusals pins the typed start refusals: a --root that is not a
// directory, and a --bundle no served root carries.
func TestLSPStartRefusals(t *testing.T) {
	file := writeTempFile(t)
	code, out, derr := runCapture(t, []string{"lsp", "--root", file, "--json"})
	if code != cli.ExitRefused || !strings.Contains(out, `"code": "bad-root"`) {
		t.Fatalf("lsp --root <file> = exit %d\nstdout: %s\nstderr: %s", code, out, derr)
	}
	bare := t.TempDir()
	code, out, derr = runCapture(t, []string{"lsp", "--root", bare, "--json"})
	if code != cli.ExitRefused || !strings.Contains(out, `"code": "not-a-repo"`) ||
		!strings.Contains(out, "is not inside a git checkout") {
		t.Fatalf("lsp --root <non-checkout> = exit %d\nstdout: %s\nstderr: %s", code, out, derr)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, derr = runCapture(t, []string{"lsp", "--root", repo, "--bundle", "local-nope", "--json"})
	if code != cli.ExitRefused || !strings.Contains(out, `"code": "missing-bundle"`) ||
		!strings.Contains(out, "names no bundle under any served root — run verdict fetch-local") {
		t.Fatalf("lsp --bundle local-nope = exit %d\nstdout: %s\nstderr: %s", code, out, derr)
	}
}
