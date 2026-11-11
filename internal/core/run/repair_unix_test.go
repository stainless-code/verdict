//go:build !windows

package run

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// TestRepairCrashRoundSkipsFIFOReview proves the crash-evidence copy never
// opens the artifact: a read on a FIFO blocks until a writer appears, so the
// round stops with its own refusal and no evidence copy, promptly.
func TestRepairCrashRoundSkipsFIFOReview(t *testing.T) {
	root := repairFixture(t)
	dir := bundleDir(root)
	review := filepath.Join(dir, reviewFile)
	var lines collector
	cmdr := crashAfterInvalid(t, dir, func() error {
		if err := os.Remove(review); err != nil {
			return err
		}
		return syscall.Mkfifo(review, 0o600)
	})
	done := make(chan struct{})
	var ref *core.RefusalError
	go func() {
		defer close(done)
		_, ref = Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run still blocked after 5s: the crash-evidence copy opened a FIFO")
	}
	if ref == nil || ref.Code != codeAgentFailed {
		t.Fatalf("ref = %v, want agent-failed", ref)
	}
	assertRoundFileExists(t, dir, "02.out")
	assertNoRoundFile(t, dir, "02.review.json")
}
