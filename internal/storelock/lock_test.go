package storelock_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/storelock"
)

// TestAcquireRefusesAHeldStore proves the lock is exclusive and typed: while
// one writer holds it a second waits its bounded window and refuses ErrBusy
// instead of entering the read-modify-write, and a release hands the store to
// the next writer.
func TestAcquireRefusesAHeldStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "kb.jsonl")
	unlock, err := storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release, err := storelock.Acquire(store, 20*time.Millisecond)
	if !errors.Is(err, storelock.ErrBusy) {
		t.Fatalf("Acquire under a holder = %v; want ErrBusy", err)
	}
	if release != nil {
		t.Fatal("a refused Acquire returned a release")
	}
	unlock()
	release, err = storelock.Acquire(store, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("Acquire after the holder released: %v", err)
	}
	release()
}

// TestLockPathSitsBesideTheStore pins the lock-file name the ignore lines and
// the troubleshooting entry spell.
func TestLockPathSitsBesideTheStore(t *testing.T) {
	if got, want := storelock.LockPath(".verdict/kb.jsonl"), ".verdict/kb.jsonl.lock"; got != want {
		t.Errorf("LockPath = %q; want %q", got, want)
	}
}
