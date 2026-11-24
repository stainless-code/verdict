//go:build unix

package noblock_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/noblock"
)

// fifoDeadline bounds a call that must return at once: a non-blocking open of
// a FIFO answers immediately, so a call still running here regressed to a
// blocking open.
const fifoDeadline = 5 * time.Second

// within runs call under [fifoDeadline]. A call that outlives it regressed to a
// blocking open of fifo: the test releases the blocked reader by opening the
// FIFO for writing, joins the worker, and only then fails, so no goroutine is
// left blocked behind a failed test.
func within(t *testing.T, fifo string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(fifoDeadline):
	}
	release := time.NewTimer(fifoDeadline)
	defer release.Stop()
	for {
		if fd, err := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		select {
		case err := <-done:
			t.Fatalf("the call blocked on a FIFO: it waited for a writer (then returned %v)", err)
			return nil
		case <-release.C:
			t.Fatal("the call blocked on a FIFO and could not be released; its worker is leaked")
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func fifoAt(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
}

// TestReadFileRefusesAFIFOWithoutBlocking proves a FIFO, direct or behind a
// symlink, is refused with [noblock.ErrNotRegular] at once.
//
// Break killed: opening with a plain [os.Open] makes the call wait for a writer
// and the deadline fires.
func TestReadFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "source.json")
	fifoAt(t, fifo)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, link} {
		wantNotRegular(t, "ReadFile", path, within(t, fifo, func() error {
			_, err := noblock.ReadFile(path)
			return err
		}))
		wantNotRegular(t, "OpenRegular", path, within(t, fifo, func() error { return openAndClose(path) }))
	}
}

func wantNotRegular(t *testing.T, call, path string, err error) {
	t.Helper()
	if !errors.Is(err, noblock.ErrNotRegular) {
		t.Errorf("%s(%s) = %v; want ErrNotRegular", call, filepath.Base(path), err)
	}
}

func openAndClose(path string) error {
	f, err := noblock.OpenRegular(path)
	if err == nil {
		_ = f.Close()
	}
	return err
}

// TestReadFileReadsRegularAndSymlinkedFiles proves a regular file, and a
// symlink to one, read their bytes.
func TestReadFileReadsRegularAndSymlinkedFiles(t *testing.T) {
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
		got, err := noblock.ReadFile(path)
		if err != nil || string(got) != `{"n":1}` {
			t.Errorf("ReadFile(%s) = %q, %v; want the file body", filepath.Base(path), got, err)
		}
	}
}

// TestReadFileKeepsItsOtherRefusals proves a directory refuses as not-regular
// and a missing file keeps [fs.ErrNotExist].
func TestReadFileKeepsItsOtherRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := noblock.ReadFile(dir); !errors.Is(err, noblock.ErrNotRegular) {
		t.Errorf("ReadFile(dir) = %v; want ErrNotRegular", err)
	}
	if _, err := noblock.ReadFile(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(absent) = %v; want fs.ErrNotExist", err)
	}
}
