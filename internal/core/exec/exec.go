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
	"sync"
	"sync/atomic"
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
// budget: expiry SIGKILLs the group and records itself, so TimedOut reports
// the kill this watchdog made rather than a deadline read after the exit. A
// nil Stdin or Env inherits the caller's. Infra failures (spawn) come back as
// errors; completed processes — any exit code — come back as results.
func (Watchdog) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 {
		return core.Result{}, errEmptyArgv
	}
	var expired atomic.Bool
	proc, waitDone, out, term, err := spawnWatched(ctx, cmd, &expired)
	if err != nil {
		return core.Result{}, err
	}
	defer releaseTree(proc)
	timedOut := false
	if havePS() {
		timedOut = pollTimeout(ctx, proc, term)
	}
	waitErr := <-waitDone
	timedOut = timedOut || expired.Load()
	exit := waitExit(proc, waitErr, timedOut)
	decoy := false
	if !timedOut && sweepOrphans(proc) {
		timedOut = true
		decoy = true
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

func spawnWatched(ctx context.Context, cmd *core.Command,
	expired *atomic.Bool,
) (*osexec.Cmd, chan error, *boundedBuffer, *terminator, error) {
	//nolint:gosec // G204: argv is the operator's own verify command (the documented bash -c model), not external input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	setGroup(proc)
	term := newTerminator(proc, cmd.GracePeriod, expired)
	proc.Cancel = term.cancel(ctx)
	proc.WaitDelay = waitDelay
	out := newBoundedBuffer(outputCap(cmd.OutputCap, VerifyOutputCap), captureTail)
	proc.Stdout = out
	proc.Stderr = out
	proc.Env = cmd.Env
	proc.Stdin = cmd.Stdin
	proc.Dir = cmd.Dir
	if err := proc.Start(); err != nil {
		releaseTree(proc)
		return nil, nil, nil, nil, watchdogError("start command: " + err.Error())
	}
	if err := armStarted(proc); err != nil {
		releaseTree(proc)
		return nil, nil, nil, nil, err
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	return proc, waitDone, out, term, nil
}

// terminator owns one command's group termination: whichever arm fires first —
// the context's Cancel hook or the watchdog's poll — signals the process group,
// and the [sync.Once] keeps the second from signaling it again.
type terminator struct {
	proc    *osexec.Cmd
	expired *atomic.Bool
	grace   time.Duration
	once    sync.Once
}

func newTerminator(proc *osexec.Cmd, grace time.Duration, expired *atomic.Bool) *terminator {
	return &terminator{proc: proc, grace: grace, expired: expired}
}

// cancel is the Cancel hook both commanders install. A deadline is the budget
// kill and records itself before the stop signal, so a child that catches
// SIGTERM and exits 0 still reads as timed out; an operator cancel records
// nothing, because it is not the watchdog firing.
func (t *terminator) cancel(ctx context.Context) func() error {
	return func() error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.expired.Store(true)
		}
		t.terminate()
		return nil
	}
}

func (t *terminator) terminate() {
	t.once.Do(func() { terminateGroup(t.proc, t.grace) })
}

// stopTimedOut ends a group that outlived its budget and records the kill, so
// the result names the watchdog even when the child chose its own exit code.
func (t *terminator) stopTimedOut() bool {
	t.expired.Store(true)
	t.terminate()
	return true
}

// timeoutPoll caps one poll sleep, so a child that exits early is noticed long
// before its budget runs out.
const timeoutPoll = time.Second

// pollTimeout watches one budgeted command to its deadline. The deadline comes
// from the context, never from a rounded second count, and an operator cancel
// stops the run without claiming the watchdog fired: the context's Cancel hook
// owns termination then, and [context.DeadlineExceeded] alone is the budget.
func pollTimeout(ctx context.Context, cmd *osexec.Cmd, term *terminator) bool {
	deadline, budgeted := ctx.Deadline()
	if !budgeted {
		return false
	}
	for processAlive(ctx, cmd) {
		if errors.Is(ctx.Err(), context.Canceled) {
			return false
		}
		if remaining := time.Until(deadline); remaining > 0 {
			waitPoll(ctx, remaining)
			continue
		}
		return term.stopTimedOut()
	}
	return false
}

// waitPoll sleeps for the shorter of the remaining budget and one poll
// interval, waking early when the context ends.
func waitPoll(ctx context.Context, remaining time.Duration) {
	timer := time.NewTimer(min(remaining, timeoutPoll))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// waitExit translates one Wait result into the wait status callers record. A
// child that exited on its own keeps the status it chose: once a Cancel hook
// has run, os/exec reports the context error in place of a successful exit, and
// a graceful shutdown routinely ends that way.
func waitExit(proc *osexec.Cmd, waitErr error, timedOut bool) int {
	if exitErr, ok := errors.AsType[*osexec.ExitError](waitErr); ok {
		return waitStatusCode(exitErr, timedOut)
	}
	if waitErr == nil || errors.Is(waitErr, osexec.ErrWaitDelay) || contextDone(waitErr, proc) {
		return 0
	}
	if timedOut {
		return sigkillExit
	}
	return 1
}

// contextDone reports os/exec's substitution of the context error for a
// successful exit after Cancel ran. The status the child chose is the truth,
// not the deadline that asked it to stop.
func contextDone(err error, proc *osexec.Cmd) bool {
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return false
	}
	state := proc.ProcessState
	return state != nil && state.Success()
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
