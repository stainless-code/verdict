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
	bundle, ref := openBundle(dir)
	if ref != nil {
		t.Fatalf("openBundle: %v", ref)
	}
	defer bundle.close()
	if ref := recordTimeoutMarker(bundle, ""); ref != nil {
		t.Fatalf("recordTimeoutMarker: %v", ref)
	}
	assertEmpty(t, timeout)
	// A directory in the marker's place cannot be replaced by the atomic write.
	if err := os.Remove(timeout); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(timeout, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ref := recordTimeoutMarker(bundle, ""); ref == nil {
		t.Fatal("recordTimeoutMarker over a directory = nil, want the write error")
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
