//go:build windows

package exec

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stainless-code/verdict/internal/core"
)

// TestExecuteTimeoutKillsTree proves the budget expiry kills the whole tree:
// a grandchild that ignores TERM is dead once Run returns the timeout code.
func TestExecuteTimeoutKillsTree(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "grandchild.pid"))
	vcmd := `trap '' TERM; (trap '' TERM; exec sleep 30) & ` + pidReport(pidFile) + `; exec sleep 30`
	tctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := Watchdog{}.Run(tctx, &core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != sigkillExit || !res.TimedOut || res.Decoy {
		t.Errorf("result = exit %d timedOut %v decoy %v; want exit %d, timedOut true, decoy false",
			res.ExitCode, res.TimedOut, res.Decoy, sigkillExit)
	}
	assertPidDead(t, pidFile)
}

// TestSpawnedChildIsInJobBeforeItRuns proves the job assignment lands before
// the suspended primary thread is resumed: a grandchild spawned at start is
// already inside the job, and the child is running afterwards.
func TestSpawnedChildIsInJobBeforeItRuns(t *testing.T) {
	var expired atomic.Bool
	proc, waitDone, _, err := spawnWatched(t.Context(), &core.Command{
		Argv: []string{"bash", "-c", "exec sleep 30"}, Env: os.Environ(),
	}, &expired)
	if err != nil {
		t.Fatal(err)
	}
	defer reapWatched(t, proc, waitDone)
	tr := treeFor(proc)
	if tr == nil {
		t.Fatal("no job registered for the spawned command")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid32(proc.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandle(h)
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

// TestCancelKillsGrandchildSpawnedAtStart proves a canceled run is not read
// as a timeout and still kills the grandchild the child spawned immediately.
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
		res, err := Watchdog{}.Run(ctx, &core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
		done <- outcome{err: err, res: res}
	}()
	readPid(t, pidFile)
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

// TestResumeOKReadsTheDWORD proves ResumeThread's verdict comes from the low
// 32 bits: a failure whose upper half carries junk still reads as a failure,
// not as a resumed thread that lets armStarted return success.
func TestResumeOKReadsTheDWORD(t *testing.T) {
	for _, tc := range []struct {
		name string
		word uintptr
		want bool
	}{
		{name: "failure", word: 0x00000000FFFFFFFF, want: false},
		{name: "failure with junk upper bits", word: 0xDEADBEEFFFFFFFFF, want: false},
		{name: "previous suspend count zero", word: 0, want: true},
		{name: "previous suspend count one", word: 1, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumeOK(tc.word); got != tc.want {
				t.Errorf("resumeOK(%#x) = %v; want %v", tc.word, got, tc.want)
			}
		})
	}
}

// TestWin32OKReadsTheBOOL proves a BOOL result's verdict is its low 32 bits:
// FALSE with junk above it is still FALSE.
func TestWin32OKReadsTheBOOL(t *testing.T) {
	for _, tc := range []struct {
		name string
		word uintptr
		want bool
	}{
		{name: "false", word: 0, want: false},
		{name: "false with junk upper bits", word: 0xDEADBEEF00000000, want: false},
		{name: "true", word: 1, want: true},
		{name: "true with junk upper bits", word: 0xDEADBEEF00000001, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := win32OK(tc.word); got != tc.want {
				t.Errorf("win32OK(%#x) = %v; want %v", tc.word, got, tc.want)
			}
		})
	}
}

func pidReport(file string) string {
	return `cat /proc/$!/winpid > '` + file + `'`
}

// reapWatched kills the spawned tree, waits for the reaper goroutine, and
// releases the job handle. Cleanup failures are logged, never failed here:
// the test's assertions own the verdict.
func reapWatched(t *testing.T, proc *osexec.Cmd, waitDone chan error) {
	t.Helper()
	if err := killGroup(proc); err != nil {
		t.Logf("kill spawned child: %v", err)
	}
	if err := <-waitDone; err != nil {
		t.Logf("wait spawned child: %v", err)
	}
	releaseTree(proc)
}

func pidAlive(pid int) bool {
	const (
		processQueryLimitedInformation = 0x1000
		stillActive                    = 259
	)
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid32(pid))
	if err != nil {
		return false
	}
	defer closeHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
