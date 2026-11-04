package verifycore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTruncateOutputsEmptiesEveryPath pins the pre-run truncation the verify
// record depends on: every watchdog output is emptied before the run, and a
// failed write is returned rather than swallowed. A stale .verify.timeout
// marker surviving into the next run would be read as this run's timeout.
//
// Mutant killed: service.go:297 CONDITIONALS_NEGATION (`err != nil` ->
// `err == nil`): the first successful write returns nil early, so the second
// path keeps its stale bytes and the failing call reports no error.
func TestTruncateOutputsEmptiesEveryPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, ".verify.out")
	timeout := filepath.Join(dir, ".verify.timeout")
	writeStale(t, out, timeout)
	if err := truncateOutputs(out, timeout); err != nil {
		t.Fatalf("truncateOutputs: %v", err)
	}
	assertEmpty(t, out, timeout)
	if err := truncateOutputs(out, filepath.Join(dir, "missing", "x")); err == nil {
		t.Fatal("truncateOutputs with an unwritable path = nil, want the write error")
	}
}

// writeStale seeds each path with bytes a previous run would leave behind.
func writeStale(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("stale marker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// assertEmpty fails if any path still carries bytes.
func assertEmpty(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if b, err := os.ReadFile(p); err != nil || len(b) != 0 {
			t.Fatalf("%s = %q (%v), want empty", filepath.Base(p), b, err)
		}
	}
}
