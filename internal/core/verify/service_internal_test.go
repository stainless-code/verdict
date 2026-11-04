package verifycore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordTimeoutMarkerClearsAndReportsFailure pins that an empty marker
// empties the path and a failed write is returned.
func TestRecordTimeoutMarkerClearsAndReportsFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	timeout := filepath.Join(dir, ".verify.timeout")
	writeStale(t, timeout)
	if ref := recordTimeoutMarker(timeout, ""); ref != nil {
		t.Fatalf("recordTimeoutMarker: %v", ref)
	}
	assertEmpty(t, timeout)
	if ref := recordTimeoutMarker(filepath.Join(dir, "missing", "x"), ""); ref == nil {
		t.Fatal("recordTimeoutMarker with an unwritable path = nil, want the write error")
	}
}

func writeStale(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("stale marker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertEmpty(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if b, err := os.ReadFile(p); err != nil || len(b) != 0 {
			t.Fatalf("%s = %q (%v), want empty", filepath.Base(p), b, err)
		}
	}
}
