//go:build windows

package exec

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stainless-code/verdict/internal/core"
)

// TestExecuteTimeoutKillsTree proves the Windows watchdog: a command that
// ignores TERM and leaves a sleeping child behind is killed as a tree,
// recording the SIGKILL wait code with the timeout flag set (no decoy).
// The child reports its pid, so a parent-only kill cannot pass.
func TestExecuteTimeoutKillsTree(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "grandchild.pid"))
	vcmd := `trap '' TERM; (trap '' TERM; exec sleep 30) & ` + pidReport(pidFile) + `; exec sleep 30`
	// The deadline must outlast Git Bash's startup on a cold Windows runner:
	// a kill that lands before the child reports its pid leaves no file to
	// probe. The sleeps run 30s, so 5s still proves the timeout kill.
	tctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := Watchdog{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != sigkillExit || !res.TimedOut || res.Decoy {
		t.Errorf("result = exit %d timedOut %v decoy %v; want exit %d, timedOut true, decoy false",
			res.ExitCode, res.TimedOut, res.Decoy, sigkillExit)
	}
	assertPidDead(t, pidFile)
}

// TestSpawnedChildIsInJobBeforeItRuns proves the child is a member of its job
// the moment spawnWatched returns, with no watchdog tick in between. The old
// design assigned it on the first poll tick (or at kill time), so this read
// false right after Start. Membership is asked of the kernel
// (IsProcessInJob), not inferred from our own bookkeeping.
func TestSpawnedChildIsInJobBeforeItRuns(t *testing.T) {
	proc, waitDone, _, err := spawnWatched(t.Context(), core.Command{
		Argv: []string{"bash", "-c", "exec sleep 30"}, Env: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = killGroup(proc)
		<-waitDone
		releaseTree(proc)
	}()
	tr := treeFor(proc)
	if tr == nil {
		t.Fatal("no job registered for the spawned command")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(proc.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var inJob int32
	r, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("IsProcessInJob").Call(
		uintptr(h), uintptr(tr.handle), uintptr(unsafe.Pointer(&inJob)))
	if r == 0 {
		t.Fatalf("IsProcessInJob: %v", callErr)
	}
	if inJob == 0 {
		t.Error("child is not in its job object right after Start; a grandchild spawned now would escape the kill")
	}
	if !pidAlive(proc.Process.Pid) {
		t.Error("child is not running after spawnWatched: it was left suspended")
	}
}

// TestCancelKillsGrandchildSpawnedAtStart is the end-to-end form. The command
// spawns its grandchild as its very first act; the context carries no
// deadline, so the poll tick (the old assignment point) never runs and the
// only kill is the cancel at the end. Under the old design the child joined
// the job only at that kill, after the grandchild existed, so the grandchild
// survived. Born inside the job, it must die with it.
func TestCancelKillsGrandchildSpawnedAtStart(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "grandchild.pid"))
	vcmd := `(exec sleep 30) & ` + pidReport(pidFile) + `; exec sleep 30`
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type outcome struct {
		err error
		res core.Result
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Watchdog{}.Run(ctx, core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
		done <- outcome{err: err, res: res}
	}()
	// Git Bash is slow to start on a cold runner: wait generously for the
	// grandchild to exist before canceling, or there is nothing to probe.
	readPid(t, pidFile, time.Now().Add(60*time.Second))
	cancel()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatal(o.err)
		}
		if o.res.TimedOut {
			t.Errorf("a cancel read as a timeout: %+v", o.res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the cancel")
	}
	assertPidDead(t, pidFile)
}

// pidReport is the shell fragment that writes the last background job's
// Windows pid to file. Git Bash's `$!` is an MSYS pid, a different number
// space from the Windows pids OpenProcess takes: probing it could hit an
// unrelated process (a false "alive") or nothing at all (a false "dead").
// MSYS exposes the real pid at /proc/<pid>/winpid.
func pidReport(file string) string {
	return `cat /proc/$!/winpid > '` + file + `'`
}

// pidAlive reports whether pid is still running: the job object holds the
// process object until it exits, and STILL_ACTIVE is the released-code
// sentinel.
func pidAlive(pid int) bool {
	const (
		processQueryLimitedInformation = 0x1000
		stillActive                    = 259
	)
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
