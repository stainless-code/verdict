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
func (Watchdog) Run(ctx context.Context, cmd core.Command) (core.Result, error) {
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

// errEmptyArgv is the spawn error for an empty argv.
var errEmptyArgv = watchdogError("empty argv")

// WatchdogEnforced reports whether the watchdog can poll and kill a tree.
// POSIX needs ps(1) for the zombie arm; the Windows job object covers it.
// The service warns through its own progress observer when false.
func WatchdogEnforced() bool {
	return havePS()
}

// watchdogBudget reads the watchdog budget in whole seconds from the
// context deadline the service installed. Absent reads unbudgeted.
func watchdogBudget(ctx context.Context) (int, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return int(time.Until(deadline).Seconds()), true
}

// spawnWatched starts argv in its own process group with combined output
// captured to a bounded tail, returning the command, its wait channel, and
// the capture (read after the wait).
func spawnWatched(ctx context.Context, cmd core.Command) (*osexec.Cmd, chan error, *boundedBuffer, error) {
	//nolint:gosec // G204: argv is the operator's own verify command (the documented bash -c model), not external input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	// Inherit the binary's cwd (verify runs where the caller ran, so relative
	// verifyCmds resolve identically); nil stdin and env inherit too.
	setGroup(proc)
	proc.Cancel = func() error { return killGroup(proc) }
	proc.WaitDelay = waitDelay
	out := newBoundedBuffer(outputCap(cmd.OutputCap, VerifyOutputCap), captureTail)
	proc.Stdout = out
	proc.Stderr = out
	proc.Env = cmd.Env
	proc.Stdin = cmd.Stdin
	if err := proc.Start(); err != nil {
		releaseTree(proc)
		return nil, nil, nil, watchdogError("start command: " + err.Error())
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()
	return proc, waitDone, out, nil
}

// pollTimeout owns the polling watchdog: while the main pid reads alive
// (kill -0 plus the ps zombie arm) and the budget holds, sleep one second;
// on expiry SIGKILL the group and record the timeout. The wait result is
// never consumed here: the channel is buffered so the waiter never blocks,
// and waitExit takes the single receive (reaping before the orphan sweep).
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

// stopTimedOut SIGKILLs the expired group. The kill is best effort: the
// process may have exited between the last liveness check and the kill,
// in which case there is nothing left to stop. Either way the timeout
// stands.
func stopTimedOut(cmd *osexec.Cmd) bool {
	if err := killGroup(cmd); err != nil {
		return true
	}
	return true
}

// waitPollSecond sleeps one watchdog tick, reporting whether the
// deadline context fired during the sleep.
func waitPollSecond(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(time.Second):
	}
	return ctx.Err() != nil
}

// waitExit drains the wait result and maps it to the wait code:
// 128+signo for signaled processes, the exit status otherwise. A
// timeout-killed tree reads 137 (SIGKILL).
func waitExit(waitDone chan error, timedOut bool) int {
	err := <-waitDone
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*osexec.ExitError](err); ok {
		return waitStatusCode(exitErr, timedOut)
	}
	if errors.Is(err, osexec.ErrWaitDelay) {
		// The command itself exited 0; an orphan held the output pipe past
		// WaitDelay, which the orphan sweep reports through the decoy flag.
		return 0
	}
	if timedOut {
		return sigkillExit
	}
	return 1
}

// waitStatusCode maps a decoded wait status to the wait code:
// 128+signo for signaled processes, the exit status otherwise. An
// undecodable status reads 1, or 137 on a timed-out tree.
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

// sigkillExit is the wait code for a SIGKILLed command.
const sigkillExit = 137

// sigOffset maps a fatal signal to the wait code (128+signo).
const sigOffset = 128
