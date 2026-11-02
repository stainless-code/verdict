//go:build windows

package verify

import (
	"context"
	"os/exec"
	"time"
)

// setGroup is a no-op on Windows: process-group semantics come from the
// fallback kill below (documented: full tree-kill parity is Unix-only;
// Windows CI runs build+vet+gofmt only per F12).
func setGroup(cmd *exec.Cmd) {
}

// killGroup kills the verify process on Windows (no negative-pgid kill;
// best effort, documented above).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	return cmd.Process.Kill()
}

// processAlive ports the poll liveness check: the process handle reads
// alive until Wait reaps it.
func processAlive(_ context.Context, cmd *exec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	return cmd.ProcessState == nil || !cmd.ProcessState.Exited()
}

// sweepOrphans has no process-group sweep on Windows; the documented Unix
// behavior is a no-op here.
func sweepOrphans(cmd *exec.Cmd) bool {
	return false
}

// waitStatusOf extracts the wait status from an exit error.
func waitStatusOf(exitErr *exec.ExitError) (waitStatus, bool) {
	if exitErr.ProcessState == nil {
		return waitStatus{}, false
	}
	return waitStatus{status: exitErr.ProcessState.ExitCode()}, true
}

// havePS reports whether a zombie arm exists on Windows (none).
func havePS() bool {
	return false
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

// sweepGrace is unused on Windows (kept for symmetry with the Unix file).
const sweepGrace = 2 * time.Second

// waitDelay lets stdio flush after a timeout kill (GO-EXEC-05).
const waitDelay = 2 * time.Second
