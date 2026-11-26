//go:build unix

package run

import (
	"os"
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

// TestRunRefusesAHardLinkedArtifact proves the walk refuses a review.json
// hard-linked to a source file: the agent's write would land in the source.
// Only unix FileInfo carries the link count; on Windows the check does not
// apply, which the manual states.
func TestRunRefusesAHardLinkedArtifact(t *testing.T) {
	t.Parallel()
	assertUnsafeBundleRefused(t, func(_ *testing.T, bundle, source string) error {
		return os.Link(source, filepath.Join(bundle, reviewFile))
	})
}
