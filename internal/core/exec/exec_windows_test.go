//go:build windows

package exec

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// TestExecuteTimeoutKillsTree proves the Windows watchdog: a command that
// ignores TERM and leaves a sleeping child behind is killed as a tree,
// recording the SIGKILL wait code with the timeout flag set (no decoy).
// The child reports its pid, so a parent-only kill cannot pass.
func TestExecuteTimeoutKillsTree(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "grandchild.pid"))
	vcmd := `trap '' TERM; (trap '' TERM; exec sleep 30) & echo $! > '` + pidFile + `'; exec sleep 30`
	tctx, cancel := context.WithTimeout(t.Context(), time.Second)
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
