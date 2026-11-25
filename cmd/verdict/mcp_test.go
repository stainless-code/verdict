package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/mcp"
)

func mcpTempFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = out.Close()
		_ = errOut.Close()
	})
	return out, errOut
}

func readOutput(t *testing.T, f *os.File) string {
	t.Helper()
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestMCPHelp proves `verdict mcp --help` documents the transport and the
// top-level review usage block lists the experimental entry points.
func TestMCPHelp(t *testing.T) {
	out, errOut := mcpTempFiles(t)
	code := run([]string{"mcp", "--help"}, out, errOut, os.Getenv, os.ReadFile)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, readOutput(t, errOut))
	}
	help := readOutput(t, out)
	for _, want := range []string{"verdict mcp", "stdio", "--read-only", "--operator   deprecated", "tools/call"} {
		if !strings.Contains(help, want) {
			t.Errorf("help misses %q:\n%s", want, help)
		}
	}
	if !strings.Contains(usageText(), "mcp [--read-only] (experimental)") {
		t.Error("review usage block misses the experimental mcp line")
	}
	if !strings.Contains(usageText(), "mcp config --agent") ||
		!strings.Contains(usageText(), "(experimental)") {
		t.Error("review usage block misses mcp config tagged experimental")
	}
}

// TestMCPUsageError proves unknown transport flags are exit 2 with no
// stdout bytes.
func TestMCPUsageError(t *testing.T) {
	out, errOut := mcpTempFiles(t)
	code := run([]string{"mcp", "--bogus"}, out, errOut, os.Getenv, os.ReadFile)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := readOutput(t, out); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got := readOutput(t, errOut); !strings.Contains(got, "unknown flag '--bogus' for mcp") {
		t.Errorf("stderr = %q", got)
	}
}

// emptyStdin points [os.Stdin] at an empty file so a served session ends at
// once, like a closed pipe.
func emptyStdin(t *testing.T) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		_ = f.Close()
	})
}

// TestMCPOperatorRetired proves --operator is a deprecated no-op alias: it
// still starts the server (with or without --read-only, which used to be a
// usage error), names the one rule on stderr, and keeps stdout protocol-only.
func TestMCPOperatorRetired(t *testing.T) {
	for _, argv := range [][]string{{"mcp", "--operator"}, {"mcp", "--operator", "--read-only"}} {
		emptyStdin(t)
		out, errOut := mcpTempFiles(t)
		if code := run(argv, out, errOut, os.Getenv, os.ReadFile); code != 0 {
			t.Fatalf("%v exit = %d, want 0; stderr = %s", argv, code, readOutput(t, errOut))
		}
		if got := readOutput(t, out); got != "" {
			t.Errorf("%v stdout = %q, want empty", argv, got)
		}
		want := "mcp: --operator is deprecated and ignored: local writes are allowed unless --read-only"
		if got := readOutput(t, errOut); !strings.Contains(got, want) {
			t.Errorf("%v stderr = %q, want the deprecation notice %q", argv, got, want)
		}
	}
}

// TestMCPHelpListsTheRegistry keeps the hand-written tool lists in
// `verdict mcp --help` equal to what the server registers, so a new tool
// cannot ship with a stale help count.
func TestMCPHelpListsTheRegistry(t *testing.T) {
	help := mcpUsageText()
	def := mcp.ToolNames(false)
	if want := fmt.Sprintf("Default tools (%d): ", len(def)); !strings.Contains(help, want) {
		t.Errorf("help misses %q", want)
	}
	flat := strings.Join(strings.Fields(help), " ")
	if !strings.Contains(flat, strings.Join(def, ", ")+".") {
		t.Errorf("help default list != registry %v", def)
	}
	ro := mcp.ToolNames(true)
	slices.Sort(ro)
	_, rest, found := strings.Cut(flat, "--read-only the one write-posture flag (shared with serve and lsp): expose read tools plus record dry_run (")
	list, _, closed := strings.Cut(rest, ")")
	if !found || !closed {
		t.Fatal("help misses the --read-only tool list")
	}
	got := strings.Split(list, ", ")
	slices.Sort(got)
	if !slices.Equal(got, ro) {
		t.Errorf("help read-only list = %v, registry = %v", got, ro)
	}
}
