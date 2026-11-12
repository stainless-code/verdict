package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	osexec "os/exec"

	"github.com/stainless-code/verdict/internal/core"
)

// Runner runs one process with separated stdout/stderr capture for the read,
// fetch, post, fix, init, and threads verbs: the `gh` reads
// stdout while probe stderr stages beside the bundle, so the two streams must
// not mix the way the [Watchdog]'s combined proof capture does. Stdout keeps
// its head (callers parse it as a complete document) and stderr keeps its tail
// (the real error survives), both bounded by the runner's cap.
//
// Spawn failures come back as errors; completed processes — any exit code —
// come back as results with the wait code, so callers map non-zero
// exits to their verb's warn-or-refuse posture instead of unpacking an error
// (GO-EXEC-05). An over-cap stdout returns [core.OutputCappedError]: the
// captured head cannot stand in for the whole document.
//
// The zero value is the read-verb posture: [ReadOutputCap], no missing-tool
// pre-check, and inherits the caller's stdin. The fetch and scaffold verbs
// pass their own cap and opt into the pre-check.
type Runner struct {
	// Stdin feeds commands that leave [core.Command.Stdin] nil. Nil (the
	// default) inherits this process's stdin, exactly like os/exec.
	Stdin io.Reader
	// OutputCap is the per-stream capture cap for commands that leave
	// [core.Command.OutputCap] unset; 0 or negative uses [ReadOutputCap].
	OutputCap int
	// MissingTool pre-checks argv[0] and returns [core.MissingToolError]
	// when it does not resolve, so the adapter prints the frozen
	// `missing: <tool>` line with no envelope. The verbs that can shell out
	// before any progress output opt in.
	MissingTool bool
}

// Run starts cmd.Argv with separated, bounded stream capture, returning the
// wait status. A completed process returns Result with a nil error whatever
// its exit code; only a spawn failure or an over-cap stdout returns an error.
func (r Runner) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 {
		return core.Result{}, errEmptyArgv
	}
	if err := r.precheck(cmd.Argv[0]); err != nil {
		return core.Result{}, err
	}
	//nolint:gosec // G204: argv is the operator's own gh/git plumbing, not external input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	limit := r.captureCap(cmd)
	stdout := newBoundedBuffer(limit, captureHead)
	stderr := newBoundedBuffer(limit, captureTail)
	proc.Stdout = stdout
	proc.Stderr = stderr
	proc.Stdin = commandStdin(cmd, r.Stdin)
	proc.Env = cmd.Env
	proc.Dir = cmd.Dir
	proc.WaitDelay = waitDelay
	setGroup(proc)
	proc.Cancel = func() error { return killGroup(proc) }
	if err := startTree(proc); err != nil {
		return core.Result{}, err
	}
	defer releaseTree(proc)
	runErr := proc.Wait()
	res := core.Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if exitErr, ok := errors.AsType[*osexec.ExitError](runErr); ok {
		res.ExitCode = waitStatusCode(exitErr, false)
	} else if runErr != nil && !errors.Is(runErr, osexec.ErrWaitDelay) {
		return res, fmt.Errorf("%s: %w", cmd.Argv[0], runErr) //nolint:wrapcheck // locally constructed spawn message, not an external error
	}
	if res.StdoutTruncated {
		return res, &core.OutputCappedError{Stream: "stdout", Limit: limit, ReadBytes: stdout.Seen()}
	}
	return res, nil
}

// commandStdin is the command's own stdin when it sets one, else the
// runner's fallback (nil inherits this process's stdin, like os/exec).
func commandStdin(cmd *core.Command, fallback io.Reader) io.Reader {
	if cmd.Stdin != nil {
		return cmd.Stdin
	}
	return fallback
}

// startTree starts proc in its own process group and arms group termination,
// releasing the group handle on either failure: a spawn error or a Windows
// job-assignment error leaves nothing for the caller to wait on or kill.
func startTree(proc *osexec.Cmd) error {
	if err := proc.Start(); err != nil {
		releaseTree(proc)
		return fmt.Errorf("%s: %w", proc.Args[0], err) //nolint:wrapcheck // spawn message assembled here
	}
	if err := armStarted(proc); err != nil {
		releaseTree(proc)
		return err
	}
	return nil
}

func (r Runner) precheck(bin string) error {
	if !r.MissingTool {
		return nil
	}
	if _, err := osexec.LookPath(bin); err != nil {
		return &core.MissingToolError{Tool: bin}
	}
	return nil
}

func (r Runner) captureCap(cmd *core.Command) int {
	dflt := r.OutputCap
	if dflt <= 0 {
		dflt = ReadOutputCap
	}
	return outputCap(cmd.OutputCap, dflt)
}

// LookPath reports whether tool resolves, returning
// [core.MissingToolError] when it does not. Fetch calls it before its
// first spawn so the missing-tool line precedes any progress output,
// like every other tool gate.
func (Runner) LookPath(tool string) error {
	if _, err := osexec.LookPath(tool); err != nil {
		return &core.MissingToolError{Tool: tool}
	}
	return nil
}

// Gh runs ["gh", args...] through cmdr, returning the captured streams.
// ok is false when the commander is missing, the spawn fails, or gh exits
// nonzero — callers map that to their verb's warn-or-refuse posture, the
// way readcommon.Gh's error return mapped before the core move.
func Gh(ctx context.Context, cmdr core.Commander, args ...string) (stdout, stderr string, ok bool) {
	return Host(ctx, cmdr, ghBin, args...)
}

// Host runs one host CLI (gh, glab, az, cafe) with exactly the given argv and
// the same ok contract as [Gh], so a verb that preflights a non-GitHub host
// shares one call shape instead of re-deriving it.
func Host(ctx context.Context, cmdr core.Commander, bin string, args ...string) (stdout, stderr string, ok bool) {
	if cmdr == nil {
		return "", "", false
	}
	res, err := cmdr.Run(ctx, &core.Command{Argv: append([]string{bin}, args...)})
	if err != nil || res.ExitCode != 0 {
		return res.Stdout, res.Stderr, false
	}
	return res.Stdout, res.Stderr, true
}

const ghBin = "gh"
