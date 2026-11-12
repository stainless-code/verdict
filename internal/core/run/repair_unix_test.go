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

// TestCrashReviewBytesNeverBlocksOnAFIFO proves the open itself cannot hang: a
// regular file swapped for a FIFO after any stat is opened non-blocking, so
// the mode check rejects it without waiting for a writer that never comes.
func TestCrashReviewBytesNeverBlocksOnAFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), reviewFile)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	type read struct {
		raw []byte
		ok  bool
	}
	done := make(chan read, 1)
	go func() {
		raw, ok := crashReviewBytes(path)
		done <- read{raw: raw, ok: ok}
	}()
	select {
	case got := <-done:
		if got.ok || got.raw != nil {
			t.Errorf("crashReviewBytes = %q, %v; want nothing for a FIFO", got.raw, got.ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crashReviewBytes still blocked after 5s: the open waited for a FIFO writer")
	}
}
