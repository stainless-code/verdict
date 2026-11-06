// Package exec is core's production command runners: [Watchdog] for the
// verify proof path (combined output, process group, tree kill on the
// watchdog budget, orphan sweep) and [Runner] for every verb that needs the
// streams apart. Both report completed processes as results; spawn failures
// are errors. No service body calls exec directly; tests inject a scripted
// [core.Commander] instead.
package exec

import (
	"context"
	"errors"
	osexec "os/exec"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// Watchdog runs commands on the host with the verify watchdog contract:
// process-group kill on timeout, orphan sweep on clean exits, and the
// shell `wait` exit code (128+signo for signaled processes). It is the only
// combined-capture runner; every other verb uses [Runner].
type Watchdog struct{}

// Run starts cmd.Argv in its own process group with combined output
// capture, returning the wait status. The context deadline is the watchdog
// budget: expiry SIGKILLs the group. A nil Stdin or Env inherits the
// caller's. Infra failures (spawn) come back as errors; completed
// processes — any exit code — come back as results.
func (Watchdog) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 {
		return core.Result{}, errEmptyArgv
	}
	secs, budgeted := watchdogBudget(ctx)
	proc, waitDone, out, err := spawnWatched(ctx, cmd)
	if err != nil {
		return core.Result{}, err
	}
	defer releaseTree(proc)
	timedOut := false
	if havePS() && budgeted {
		timedOut = pollTimeout(ctx, proc, secs)
	}
	exit := waitExit(waitDone, timedOut)
	decoy := false
	if !timedOut && sweepOrphans(proc) {
		timedOut = true
		decoy = true
	}
	if ctx.Err() == context.DeadlineExceeded {
		timedOut = true
	}
	return core.Result{
		Stdout:          out.String(),
		ExitCode:        exit,
		TimedOut:        timedOut,
		Decoy:           decoy,
		StdoutTruncated: out.Truncated(),
	}, nil
}

var errEmptyArgv = watchdogError("empty argv")

// WatchdogEnforced reports whether the watchdog can poll and kill a tree.
// POSIX needs ps(1) for the zombie arm; the Windows job object covers it.
// The service warns through its own progress observer when false.
func WatchdogEnforced() bool {
	return havePS()
}

func watchdogBudget(ctx context.Context) (int, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return int(time.Until(deadline).Seconds()), true
}

func spawnWatched(ctx context.Context, cmd *core.Command) (*osexec.Cmd, chan error, *boundedBuffer, error) {
	//nolint:gosec // G204: argv is the operator's own verify command (the documented bash -c model), not external input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	setGroup(proc)
	proc.Cancel = func() error { return killGroup(proc) }
	proc.WaitDelay = waitDelay
	out := newBoundedBuffer(outputCap(cmd.OutputCap, VerifyOutputCap), captureTail)
	proc.Stdout = out
	proc.Stderr = out
	proc.Env = cmd.Env
	proc.Stdin = cmd.Stdin
	proc.Dir = cmd.Dir
	if err := proc.Start(); err != nil {
		releaseTree(proc)
		return nil, nil, nil, watchdogError("start command: " + err.Error())
	}
	if err := armStarted(proc); err != nil {
		releaseTree(proc)
		return nil, nil, nil, err
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	return proc, waitDone, out, nil
}

func pollTimeout(ctx context.Context, cmd *osexec.Cmd, timeoutSecs int) bool {
	elapsed := 0
	for {
		if !processAlive(ctx, cmd) {
			return false
		}
		if elapsed >= timeoutSecs {
			break
		}
		if waitPollSecond(ctx) {
			break
		}
		elapsed++
	}
	return stopTimedOut(cmd)
}

func stopTimedOut(cmd *osexec.Cmd) bool {
	if err := killGroup(cmd); err != nil {
		return true
	}
	return true
}

func waitPollSecond(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(time.Second):
	}
	return ctx.Err() != nil
}

func waitExit(waitDone chan error, timedOut bool) int {
	err := <-waitDone
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*osexec.ExitError](err); ok {
		return waitStatusCode(exitErr, timedOut)
	}
	if errors.Is(err, osexec.ErrWaitDelay) {
		return 0
	}
	if timedOut {
		return sigkillExit
	}
	return 1
}

func waitStatusCode(exitErr *osexec.ExitError, timedOut bool) int {
	if timedOut {
		if code, ok := timeoutExitCode(); ok {
			return code
		}
	}
	ws, ok := waitStatusOf(exitErr)
	if !ok {
		if timedOut {
			return sigkillExit
		}
		return 1
	}
	if ws.signaled {
		return sigOffset + ws.signal
	}
	return ws.status
}

const sigkillExit = 137

const sigOffset = 128
