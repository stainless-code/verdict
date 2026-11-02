package core

import (
	"context"
	"io"
)

// Command is one spawned process: the argv to run, the stdin to feed it,
// and the environment it inherits. A nil Stdin inherits the caller's stdin
// (the verify contract); a nil Env inherits the process environment.
type Command struct {
	Argv  []string
	Stdin io.Reader
	Env   []string
}

// Result is one completed process: the captured streams plus the wait
// status. ExitCode follows the shell `wait` code (128+signo for signaled
// processes). TimedOut reports the watchdog: the budget fired or the
// post-exit sweep found an orphaned tree, so the proof is refused even
// when the exit reads 0. Decoy refines it: the parent finished while its
// tree outlived it (the orphan sweep fired, not the budget), so the
// service records the decoy marker instead of the timeout line.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	Decoy    bool
}

// Commander runs one process to completion. The production implementation
// lives in internal/core/exec and owns the process-group watchdog; tests
// inject a scripted commander. No service body calls exec directly.
type Commander interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}
