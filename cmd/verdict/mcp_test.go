package main

import (
	"os"
	"strings"
	"testing"
)

// mcpTempFiles returns stdout/stderr temp files for run() calls.
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

// readFile resets and reads a temp output file.
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
	for _, want := range []string{"verdict mcp", "stdio", "--read-only", "tools/call"} {
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
