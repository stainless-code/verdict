//go:build unix

package run

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestRunRefusesAFIFOInTheBundle proves the pre-launch walk refuses a
// non-regular entry: a FIFO in the bundle would block a reader or carry a
// write elsewhere, so the launch refuses before any agent starts.
func TestRunRefusesAFIFOInTheBundle(t *testing.T) {
	t.Parallel()
	assertUnsafeBundleRefused(t, func(_ *testing.T, bundle, _ string) error {
		return syscall.Mkfifo(filepath.Join(bundle, "cost.json"), 0o600)
	})
}
