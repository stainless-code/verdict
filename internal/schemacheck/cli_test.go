package schemacheck_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/schemacheck"
)

// runCLI runs the hidden verb and returns its exit code and stderr.
func runCLI(args ...string) (int, string) {
	var stderr bytes.Buffer
	code := schemacheck.Run(args, &stderr)
	return code, stderr.String()
}

// assertRun requires one exit code, and want in stderr (or an empty stream
// when want is empty).
func assertRun(t *testing.T, code int, want string, args ...string) {
	t.Helper()
	gotCode, stderr := runCLI(args...)
	if gotCode != code {
		t.Errorf("exit = %d; want %d (stderr %q)", gotCode, code, stderr)
	}
	if want == "" {
		if stderr != "" {
			t.Errorf("stderr = %q; want empty", stderr)
		}
		return
	}
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q; want it to contain %q", stderr, want)
	}
}

// TestRunExitCodes pins the shell checker's contract: 0 valid, 1 rejected or
// unreadable input, 2 bad usage. The shim passes these through unchanged.
func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "point.json", `{"type":"object","required":["a"]}`)
	writeSchema(t, dir, "valid.json", `{"a":1}`)
	writeSchema(t, dir, "invalid.json", `{}`)
	writeSchema(t, dir, "broken.json", `{`)
	writeSchema(t, dir, "frag.json", `{"$defs":{"a":{"type":"string"}}}`)
	point := filepath.Join(dir, "point.json")
	validDoc := filepath.Join(dir, "valid.json")

	t.Run("valid", func(t *testing.T) {
		assertRun(t, 0, "", point, validDoc)
	})
	t.Run("invalid", func(t *testing.T) {
		assertRun(t, 1, "FAIL $: missing required key(s): a", point, filepath.Join(dir, "invalid.json"))
	})
	t.Run("fragment", func(t *testing.T) {
		frag := filepath.Join(dir, "frag.json") + "#/$defs/a"
		assertRun(t, 1, `FAIL $: want type "string"`, frag, validDoc)
	})
	t.Run("not json", func(t *testing.T) {
		assertRun(t, 1, "FAIL: ", point, filepath.Join(dir, "broken.json"))
	})
	t.Run("missing schema", func(t *testing.T) {
		assertRun(t, 1, "FAIL: ", filepath.Join(dir, "absent.json"), validDoc)
	})
	t.Run("missing document", func(t *testing.T) {
		assertRun(t, 1, "FAIL: ", point, filepath.Join(dir, "absent.json"))
	})
	t.Run("bad sub-pointer", func(t *testing.T) {
		assertRun(t, 1, "bad sub-pointer", point+"#a", validDoc)
	})
	t.Run("no arguments", func(t *testing.T) {
		assertRun(t, 2, "usage: verdict")
	})
	t.Run("one argument", func(t *testing.T) {
		assertRun(t, 2, "usage: verdict", point)
	})
}

// TestShimContract proves the shipped script keeps the exit codes and the one
// FAIL line per rejection the evals grade.
func TestShimContract(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "point.json", `{"type":"object","required":["a"]}`)
	writeSchema(t, dir, "invalid.json", `{}`)
	code, stderr := runCLI(filepath.Join(dir, "point.json"), filepath.Join(dir, "invalid.json"))
	if code != 1 {
		t.Fatalf("exit = %d; want 1", code)
	}
	if lines := strings.Count(strings.TrimSpace(stderr), "\n") + 1; lines != 1 {
		t.Errorf("stderr has %d lines; want exactly one FAIL line: %q", lines, stderr)
	}
}
