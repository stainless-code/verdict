//go:build unix

package serve

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// hashDiffWithin runs hashDiff under a deadline, so a regression that blocks
// on a FIFO fails the test instead of hanging the suite.
func hashDiffWithin(t *testing.T, path string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- hashDiff(sha256.New(), path) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("hashDiff blocked on a FIFO: it waited for a writer on a non-regular file")
		return nil
	}
}

// TestHashDiffRefusesAFIFOWithoutBlocking pins the non-blocking guard: a
// full.diff that is a FIFO with no writer would block a plain open forever
// while the findings-store lock is held. The open itself is non-blocking and
// the opened descriptor is what is refused, so there is no stat-then-open
// window for a swap to land in.
func TestHashDiffRefusesAFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "full.diff")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	err := hashDiffWithin(t, path)
	if err == nil || !strings.Contains(err.Error(), readcommon.ErrNotRegular.Error()) {
		t.Fatalf("hashDiff = %v, want a not-a-regular-file refusal", err)
	}
}

// TestHashDiffRefusesASymlinkedFIFOWithoutBlocking proves the descriptor
// check covers a link whose target is swapped to a FIFO: following the link
// must not wait for a writer either.
func TestHashDiffRefusesASymlinkedFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "full.diff")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := hashDiffWithin(t, link); err == nil || !strings.Contains(err.Error(), readcommon.ErrNotRegular.Error()) {
		t.Fatalf("hashDiff = %v, want a not-a-regular-file refusal", err)
	}
}

// TestHashDiffFollowsASymlinkToARegularFile pins the symlink decision: the
// fingerprint hashes a linked diff exactly as the other bundle readers read
// it, so the link and its target fingerprint identically.
func TestHashDiffFollowsASymlinkToARegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "real.diff")
	if err := os.WriteFile(target, []byte("diff --git a/a b/a\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(dir, "full.diff")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	direct, linked := sha256.New(), sha256.New()
	if err := hashDiff(direct, target); err != nil {
		t.Fatalf("hashDiff target: %v", err)
	}
	if err := hashDiff(linked, link); err != nil {
		t.Fatalf("hashDiff link: %v", err)
	}
	if !bytes.Equal(direct.Sum(nil), linked.Sum(nil)) {
		t.Error("a symlinked full.diff fingerprinted differently from its target")
	}
}
