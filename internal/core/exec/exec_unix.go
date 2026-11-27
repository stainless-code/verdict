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

// setGroup starts the child as the leader of a process group of its own, or,
// with session, of a session of its own (and so of its group, the group id
// its pid either way): the group kill and sweep reach it the same way, and a
// new session has no controlling terminal.
func setGroup(cmd *osexec.Cmd, session bool) {
	if session {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return
	}
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

// terminatePoll is the gap between two liveness reads while a grace period
// runs out.
const terminatePoll = 25 * time.Millisecond

// terminateGroup ends one process group. A zero grace keeps the posture every
// verb but the agent launcher has: SIGKILL the group at once. A positive grace
// asks the whole group to stop with SIGTERM — the group, because signaling one
// pid would leave the agent's own children running — and escalates to SIGKILL
// only when the group is still there at the end of it. ESRCH ends the wait
// immediately, so a child that honors the stop signal costs no grace at all.
func terminateGroup(cmd *osexec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	if grace > 0 && signalStop(cmd) && groupOutlived(cmd.Process.Pid, grace) {
		_ = killGroup(cmd) //nolint:errcheck // the group is already gone or unreachable
		return
	}
	_ = killGroup(cmd) //nolint:errcheck // no process left to report a kill failure to
}

// signalStop asks the whole group to stop, reporting whether the signal was
// delivered: a group that is already gone needs no escalation either.
func signalStop(cmd *osexec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) == nil
}

// groupOutlived reports whether the process group is still addressable when the
// grace period runs out. The poll is a plain sleep, not a context wait: the
// context that started the termination has already ended, and the escalation
// must happen whether or not a caller is still listening.
func groupOutlived(pid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		if err := syscall.Kill(-pid, 0); err != nil {
			return false
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true
		}
		time.Sleep(min(remaining, terminatePoll))
	}
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
