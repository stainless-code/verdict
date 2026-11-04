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

func TestExecuteTimeoutKillsTree(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "grandchild.pid"))
	vcmd := `trap '' TERM; (trap '' TERM; exec sleep 30) & ` + pidReport(pidFile) + `; exec sleep 30`
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

func pidReport(file string) string {
	return `cat /proc/$!/winpid > '` + file + `'`
}

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
