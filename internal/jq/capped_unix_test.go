//go:build unix

package jq_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// fifoDeadline bounds a call that must return at once: a non-blocking open of
// a FIFO answers immediately, so a call still running here regressed to a
// blocking open.
const fifoDeadline = 5 * time.Second

// cappedWithin reads path through [jq.CappedFile] under [fifoDeadline]. A call
// that outlives it regressed to a blocking open of fifo: the worker is
// released and joined, then the test fails.
func cappedWithin(t *testing.T, fifo, path string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := jq.CappedFile(path, 1<<20)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(fifoDeadline):
		releaseFIFO(t, fifo, done)
		t.Fatalf("CappedFile(%s) blocked on a FIFO", filepath.Base(path))
		return nil
	}
}

// TestCappedFileRefusesAFIFOWithoutBlocking proves the shared capped reader
// never waits for a writer: a FIFO, direct or behind a symlink, reads as
// unreadable at once and is never [jq.ErrTooLarge].
//
// Break killed: opening with a plain [os.Open] makes both legs wait for a
// writer and the deadline fires (the test then releases the worker and fails).
func TestCappedFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "source.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	fifoLink := filepath.Join(dir, "fifo-link.json")
	if err := os.Symlink(fifo, fifoLink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, fifoLink} {
		err := cappedWithin(t, fifo, path)
		if err == nil || errors.Is(err, jq.ErrTooLarge) {
			t.Errorf("CappedFile(%s) = %v; want an unreadable refusal", filepath.Base(path), err)
		}
	}
}

// TestCappedFileFollowsASymlinkToARegularFile proves the FIFO guard keeps the
// symlink-to-regular-file behavior callers rely on.
func TestCappedFileFollowsASymlinkToARegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "PR.json")
	if err := os.WriteFile(file, []byte(`{"n":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, link} {
		got, err := jq.CappedFile(path, 1<<20)
		if err != nil || string(got) != `{"n":1}` {
			t.Errorf("CappedFile(%s) = %q, %v; want the file body", filepath.Base(path), got, err)
		}
	}
}

// releaseFIFO unblocks a reader stuck in open(2) by opening the FIFO for
// writing, then joins the worker so none leaks behind a failed test.
func releaseFIFO[T any](t *testing.T, fifo string, done <-chan T) {
	t.Helper()
	deadline := time.After(fifoDeadline)
	for {
		if fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		select {
		case <-done:
			return
		case <-deadline:
			t.Error("the blocked worker could not be released and is leaked")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}
