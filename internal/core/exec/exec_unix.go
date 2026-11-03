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

// setGroup puts the verify child in its own process group so the timeout
// kill stops the whole tree (a backgrounded child would otherwise survive
// and its exit 0 get recorded as proof).
func setGroup(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup SIGKILLs the child's process group (GO-EXEC-05: kill the group,
// not the pid).
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

// processAlive owns the poll liveness check: kill -0 succeeds and ps does
// not report a zombie (a reaped-but-unwaited child reads alive under
// kill -0; without the ps arm a fast exit could false-fire the timeout).
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

// sweepOrphans owns the post-reap sweep: a parent that exits fast can leave
// the tree running (decoy exit), which the poll loop never sees. If the
// group outlives the parent, a grace period lets legitimate stragglers
// finish; survivors are killed and reported.
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
	// Best effort: the group may have drained between the grace sleep
	// and the kill. Either way the sweep stands.
	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil {
		return true
	}
	return true
}

// waitStatusOf extracts the wait status from an exit error, reporting the
// signal and status the way a shell `wait` builtin does.
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

// havePS reports whether ps(1) exists for the zombie arm.
func havePS() bool {
	_, err := osexec.LookPath("ps")
	return err == nil
}

// TrimTail matches shell command substitution: strip trailing newlines.
func TrimTail(s string) string {
	return strings.TrimRight(s, "\n")
}

// releaseTree drops the platform process-tree bookkeeping after a run.
// POSIX has none: the process group dies with its leader.
func releaseTree(_ *osexec.Cmd) {
}

// timeoutExitCode reports the timeout exit code the platform's wait status
// cannot express. POSIX encodes the SIGKILL itself (137), so nothing is
// overridden.
func timeoutExitCode() (int, bool) {
	return 0, false
}

// waitStatus carries the decoded process exit.
type waitStatus struct {
	signal   int
	status   int
	signaled bool
}

// errNoProcess is the kill-group error for a process that never started.
var errNoProcess = watchdogError("no verify process")

// watchdogError is a plain error value for watchdog failures.
type watchdogError string

// Error implements error.
func (e watchdogError) Error() string { return string(e) }

// sweepGrace is the post-exit grace period for legitimate stragglers.
const sweepGrace = 2 * time.Second

// waitDelay lets stdio flush after a timeout kill (GO-EXEC-05).
const waitDelay = 2 * time.Second
