package incident

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// lockSuffix names the file beside the ledger that admits one attach at a
	// time: an attach is a read-modify-write of the whole file, so two running
	// together both read the same rows and the second write drops the first.
	lockSuffix = ".lock"
	// lockMode is the lock file's owner-only mode.
	lockMode = 0o600
	// lockWait bounds the wait for a concurrent attach: longer than any attach
	// takes, shorter than an operator's patience.
	lockWait = 5 * time.Second
	// lockPoll is the retry interval while an attach holds the lock.
	lockPoll = 5 * time.Millisecond
	// lockStale is the age at which a lock file may be a killed attach's
	// leftover: the holder never spans a subprocess or a network call, so it
	// holds the lock for milliseconds. Age alone breaks nothing.
	lockStale = time.Minute
)

// ErrBusy marks a ledger whose lock this attach could not take inside the wait
// window. The holder is still running, so the caller may retry.
var ErrBusy = errors.New("incident ledger busy")

// claimSeq tells two lock tokens apart when their pids and clocks agree: one
// process holds the lock only once at a time, but it claims it per attach.
var claimSeq atomic.Uint64

// owner is the attach that wrote the lock: the token the file carries and the
// pid a waiter probes before treating the lock as a leftover.
type owner struct {
	start string
	pid   int
	seq   uint64
}

// token renders the owner as the lock file holds it.
func (o owner) token() string {
	return fmt.Sprintf("%d %s %d\n", o.pid, o.start, o.seq)
}

// release drops the lock only while it still carries this attach's token: a
// holder whose lock a waiter broke must not delete the new holder's file.
func (o owner) release(file string) func() {
	return func() {
		raw, err := os.ReadFile(file)
		if err != nil || string(raw) != o.token() {
			return
		}
		_ = os.Remove(file)
	}
}

// lock takes the ledger's exclusive lock, waiting up to wait for an attached
// writer to release it, and returns the release.
func lock(root string, wait time.Duration) (func(), error) {
	file := lockPath(root)
	own, err := claimWaiting(file, time.Now().Add(wait))
	if err != nil {
		return nil, err
	}
	return own.release(file), nil
}

// claimWaiting polls for the lock until deadline: the owner when the lock is
// free or its holder is gone, ErrBusy while a live attach holds it.
func claimWaiting(file string, deadline time.Time) (owner, error) {
	for {
		own, err := claimLock(file)
		if err == nil {
			return own, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return owner{}, err
		}
		if !time.Now().Before(deadline) {
			return owner{}, ErrBusy //nolint:wrapcheck // the package's own sentinel
		}
		time.Sleep(lockPoll)
	}
}

// claimLock makes one attempt at the ledger lock: the owner on success,
// [fs.ErrExist] while a live attach holds it, and the failure otherwise.
func claimLock(file string) (owner, error) {
	own, err := claim(file)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return own, err
	}
	if !abandoned(file) {
		return owner{}, err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return owner{}, fmt.Errorf("incident ledger lock %s: %w", file, err) //nolint:wrapcheck // the held lock cannot be broken
	}
	return claim(file)
}

// claim creates the lock file and writes this attach's token into it.
func claim(file string) (owner, error) {
	own := owner{
		pid:   os.Getpid(),
		start: time.Now().UTC().Format(time.RFC3339Nano),
		seq:   claimSeq.Add(1),
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, lockMode)
	if err != nil {
		return owner{}, fmt.Errorf("incident ledger lock %s: %w", file, err) //nolint:wrapcheck // the caller matches fs.ErrExist
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprint(f, own.token())
	return own, nil
}

// abandoned reports whether the lock is a killed attach's leftover: aged past
// any real attach AND written by a process that is gone. mtime alone is not a
// death certificate — a stopped or sleeping holder keeps its rows, so it keeps
// its lock.
func abandoned(file string) bool {
	fi, err := os.Stat(file)
	if err != nil || time.Since(fi.ModTime()) <= lockStale {
		return false
	}
	pid, ok := lockPid(file)
	return !ok || !pidAlive(pid)
}

// lockPid reads the pid the holder wrote. No readable pid means nothing is left
// to probe: a crash between creating the lock and writing the token leaves a
// file whose holder cannot be running.
func lockPid(file string) (int, bool) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, false
	}
	field, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	pid, err := strconv.Atoi(field)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// lockPath anchors the lock beside the ledger it guards.
func lockPath(root string) string {
	return path(root) + lockSuffix
}
