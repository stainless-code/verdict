package exec

import (
	"bytes"
	"context"
	"errors"
	osexec "os/exec"

	"github.com/stainless-code/verdict/internal/core"
)

// SplitCommander runs argv with separate stdout and stderr capture, like
// the fetch port's old runCmd: a missing binary reads [core.MissingToolError]
// (the adapter renders the frozen `missing: <tool>` line with no envelope),
// a completed process with a non-zero exit comes back with its streams plus
// an error, and spawn failures come back as errors. A nil Stdin or Env
// inherits the caller's, exactly like the shell spawn it replaces. The
// verify watchdog in exec.go stays the combined-capture contract; fetch
// needs the streams apart for its .err sidecars and terminal passthrough.
type SplitCommander struct{}

// Run starts argv to completion, capturing both streams apart.
func (SplitCommander) Run(ctx context.Context, cmd core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 {
		return core.Result{}, errEmptyArgv
	}
	if _, err := osexec.LookPath(cmd.Argv[0]); err != nil {
		return core.Result{}, &core.MissingToolError{Tool: cmd.Argv[0]}
	}
	//nolint:gosec // G204: argv is the operator's own fetch command, not agent input.
	proc := osexec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	proc.Env = cmd.Env
	proc.Stdin = cmd.Stdin
	var stdout, stderr bytes.Buffer
	proc.Stdout = &stdout
	proc.Stderr = &stderr
	res := core.Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err := proc.Run(); err != nil {
		res.Stdout, res.Stderr = stdout.String(), stderr.String()
		res.ExitCode = splitExitCode(err)
		return res, &splitError{bin: cmd.Argv[0], err: err}
	}
	res.Stdout, res.Stderr = stdout.String(), stderr.String()
	return res, nil
}

// LookPath reports whether tool resolves, returning
// [core.MissingToolError] when it does not. Fetch calls it before its
// first spawn so the missing-tool line precedes any progress output,
// exactly like the shell's `need` gate.
func (SplitCommander) LookPath(tool string) error {
	if _, err := osexec.LookPath(tool); err != nil {
		return &core.MissingToolError{Tool: tool}
	}
	return nil
}

// splitExitCode maps a completed process's wait result to the shell `wait`
// code (128+signo for signaled processes, the exit status otherwise).
func splitExitCode(err error) int {
	if exitErr, ok := errors.AsType[*osexec.ExitError](err); ok {
		return waitStatusCode(exitErr, false)
	}
	return 1
}

// splitError seals a non-zero fetch spawn; callers report the captured
// streams, never this carrier.
type splitError struct {
	err error
	bin string
}

// Error implements error.
func (e *splitError) Error() string {
	return e.bin + ": " + e.err.Error()
}

// Unwrap exposes the underlying process error.
func (e *splitError) Unwrap() error {
	return e.err
}
