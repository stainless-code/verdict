//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package exec

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setGroup(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func armStarted(_ *osexec.Cmd) error { return nil }

func killGroup(cmd *osexec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill verify group: %v", err)
	}
	return nil
}

func processAlive(ctx context.Context, cmd *osexec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		return false
	}
	//nolint:gosec // G204: the pid is our own verify child, not external input.
	out, err := osexec.CommandContext(ctx, "ps", "-p", strconv.Itoa(cmd.Process.Pid), "-o", "stat=").Output()
	if err != nil {
		return true
	}
	return !strings.Contains(string(out), "Z")
}

func sweepOrphans(cmd *osexec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	pgid := -cmd.Process.Pid
	if syscall.Kill(pgid, 0) != nil {
		return false
	}
	time.Sleep(sweepGrace)
	if syscall.Kill(pgid, 0) != nil {
		return false
	}
	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil {
		return true
	}
	return true
}

func waitStatusOf(exitErr *osexec.ExitError) (waitStatus, bool) {
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return waitStatus{}, false
	}
	if ws.Signaled() {
		return waitStatus{signaled: true, signal: int(ws.Signal())}, true
	}
	return waitStatus{status: ws.ExitStatus()}, true
}

func havePS() bool {
	_, err := osexec.LookPath("ps")
	return err == nil
}

// TrimTail matches shell command substitution: strip trailing newlines.
func TrimTail(s string) string {
	return strings.TrimRight(s, "\n")
}

func releaseTree(_ *osexec.Cmd) {
}

func timeoutExitCode() (int, bool) {
	return 0, false
}

type waitStatus struct {
	signal   int
	status   int
	signaled bool
}

var errNoProcess = watchdogError("no verify process")

type watchdogError string

func (e watchdogError) Error() string { return string(e) }

const sweepGrace = 2 * time.Second

const waitDelay = 2 * time.Second
