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
	for _, want := range []string{"verdict mcp", "stdio", "--read-only", "--operator", "tools/call"} {
		if !strings.Contains(help, want) {
			t.Errorf("help misses %q:\n%s", want, help)
		}
	}
	if !strings.Contains(usageText(), "mcp [--read-only | --operator] (experimental)") {
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

// TestMCPOperatorConflict proves --operator and --read-only cannot combine:
// the flag pair is a usage error before the server starts.
func TestMCPOperatorConflict(t *testing.T) {
	out, errOut := mcpTempFiles(t)
	code := run([]string{"mcp", "--operator", "--read-only"}, out, errOut, os.Getenv, os.ReadFile)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := readOutput(t, out); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got := readOutput(t, errOut); !strings.Contains(got, "mutually exclusive") {
		t.Errorf("stderr = %q", got)
	}
}

// TestMCPHelpListsTheRegistry keeps the hand-written tool lists in
// `verdict mcp --help` equal to what the server registers, so a new tool
// cannot ship with a stale help count.
func TestMCPHelpListsTheRegistry(t *testing.T) {
	help := mcpUsageText()
	def := mcp.ToolNames(false, false)
	if want := fmt.Sprintf("Default tools (%d): ", len(def)); !strings.Contains(help, want) {
		t.Errorf("help misses %q", want)
	}
	flat := strings.Join(strings.Fields(help), " ")
	if !strings.Contains(flat, strings.Join(def, ", ")+".") {
		t.Errorf("help default list != registry %v", def)
	}
	ro := mcp.ToolNames(true, false)
	slices.Sort(ro)
	_, rest, found := strings.Cut(flat, "--read-only expose only non-writing tools (")
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
