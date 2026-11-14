//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package incident

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/storelock"
	"github.com/stainless-code/verdict/internal/testenv"
)

const (
	// lockHolderEnv names the ledger the helper process locks.
	lockHolderEnv = "VERDICT_INCIDENT_LOCK_ROOT"
	// lockHeldLine is the helper's signal that the lock is taken.
	lockHeldLine = "held"
)

// TestLockHolderProcess is the helper the lock tests start: it takes the
// ledger's lock, says so on stdout, and holds it until it is killed.
func TestLockHolderProcess(t *testing.T) {
	root := os.Getenv(lockHolderEnv)
	if root == "" {
		t.Skip("helper process for the lock tests")
	}
	release, err := lock(root, lockWait)
	if err != nil {
		t.Fatalf("helper lock: %v", err)
	}
	defer release()
	fmt.Println(lockHeldLine)
	time.Sleep(time.Hour)
}

// holdLock starts the helper against root and returns it once the helper holds
// the lock.
func holdLock(t *testing.T, root string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLockHolderProcess$")
	cmd.Env = append(testenv.Base(nil), lockHolderEnv+"="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the holder: %v", err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != lockHeldLine {
		t.Fatalf("the holder never reported the lock (%q, %v)", line, err)
	}
	return cmd
}

// TestStoppedHolderKeepsTheLock proves the lock is the kernel's, not a file's:
// a SIGSTOPped holder keeps the ledger however old its lock file looks, and the
// refused attach leaves the rows alone. The old check-then-remove protocol
// judged such a file abandoned and handed the holder's row away.
func TestStoppedHolderKeepsTheLock(t *testing.T) {
	root := t.TempDir()
	seeded := ledgerLine(shaA, "src/a.ts", "2026-11-14T00:00:00Z") + "\n"
	writeStore(t, root, seeded)
	cmd := holdLock(t, root)
	defer func() {
		_ = cmd.Process.Kill() //nolint:errcheck // the holder may have exited already
		_ = cmd.Wait()         //nolint:errcheck // a killed holder's exit says nothing about the lock
	}()

	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("stop the holder: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(storelock.LockPath(path(root)), past, past); err != nil {
		t.Fatal(err)
	}
	release, err := lock(root, 20*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("lock over a stopped holder = %v; want ErrBusy", err)
	}
	if release != nil {
		t.Fatal("a refused lock returned a release")
	}
	if got := readStore(t, root); got != seeded {
		t.Fatalf("a refused lock moved the ledger: %q", got)
	}
}

// TestKilledHolderReleasesTheLock proves the kernel releases the lock when the
// holder dies: a SIGKILLed attach blocks nobody, and nothing is left to break.
func TestKilledHolderReleasesTheLock(t *testing.T) {
	root := t.TempDir()
	mkdirStore(t, root)
	cmd := holdLock(t, root)

	if _, err := lock(root, 20*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("lock under a live holder = %v; want ErrBusy", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill the holder: %v", err)
	}
	_ = cmd.Wait() //nolint:errcheck // a killed holder's exit says nothing about the lock

	release, err := lock(root, lockWait)
	if err != nil {
		t.Fatalf("lock after the holder was killed: %v", err)
	}
	release()
}
