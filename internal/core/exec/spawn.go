package exec

import (
	"context"
	"errors"
	"io"
	osexec "os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// SpawnCommander runs argv with separated stdout/stderr capture for the
// read verbs' gh/git/jq calls: the shell's `$(gh ...)` reads stdout while
// probe stderr stages beside the bundle, so the two streams must not mix
// the way the verify watchdog's combined capture does. Stdout keeps its head
// (callers parse it as a complete document) and stderr keeps its tail (the
// real error survives), both bounded by [ReadOutputCap]. A nil Stdin reads
// empty (mirroring the shell, which never pipes into these reads); a nil
// Env inherits the process environment. Spawn failures come back as
// errors; completed processes — any exit code — come back as results with
// the shell `wait` code, so callers map nonzero exits to their verb's
// warn-or-refuse posture. An over-cap stdout returns
// [core.OutputCappedError]: the captured head cannot stand in for the whole
// document.
type SpawnCommander struct{}

// Run starts cmd.Argv with separated, bounded stream capture, returning the
// wait status.
func (SpawnCommander) Run(ctx context.Context, cmd core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 {
		return core.Result{}, errEmptyArgv
	}
	//nolint:gosec // G204: argv is the operator's own gh/git/jq plumbing (like the shell port), not external input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	limit := outputCap(cmd.OutputCap, ReadOutputCap)
	stdout := newBoundedBuffer(limit, captureHead)
	stderr := newBoundedBuffer(limit, captureTail)
	proc.Stdout = stdout
	proc.Stderr = stderr
	proc.Stdin = stdinOf(cmd)
	proc.Env = cmd.Env
	runErr := proc.Run()
	res := core.Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if exitErr, ok := errors.AsType[*osexec.ExitError](runErr); ok {
		res.ExitCode = waitStatusCode(exitErr, false)
	} else if runErr != nil {
		return res, watchdogError("start command: " + runErr.Error())
	}
	if res.StdoutTruncated {
		return res, &core.OutputCappedError{Stream: "stdout", Limit: limit}
	}
	return res, nil
}

// stdinOf returns the command's stdin, defaulting to an empty reader like
// the shell's non-piped reads.
func stdinOf(cmd core.Command) io.Reader {
	if cmd.Stdin == nil {
		return strings.NewReader("")
	}
	return cmd.Stdin
}

// Gh runs ["gh", args...] through cmdr, returning the captured streams.
// ok is false when the commander is missing, the spawn fails, or gh exits
// nonzero — callers map that to their verb's warn-or-refuse posture, the
// way readcommon.Gh's error return mapped before the core move.
func Gh(ctx context.Context, cmdr core.Commander, args ...string) (stdout, stderr string, ok bool) {
	if cmdr == nil {
		return "", "", false
	}
	res, err := cmdr.Run(ctx, core.Command{Argv: append([]string{ghBin}, args...)})
	if err != nil || res.ExitCode != 0 {
		return res.Stdout, res.Stderr, false
	}
	return res.Stdout, res.Stderr, true
}

// ghBin is the GitHub CLI binary the read verbs shell out to.
const ghBin = "gh"
