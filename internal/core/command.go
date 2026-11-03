package core

import (
	"context"
	"fmt"
	"io"
)

// Command is one spawned process: the argv to run, the stdin to feed it,
// the environment it inherits, and the per-stream capture cap. A nil Stdin
// inherits the caller's stdin (the verify contract); a nil Env inherits the
// process environment.
type Command struct {
	Argv  []string
	Stdin io.Reader
	Env   []string
	// OutputCap is the capture cap in bytes: per stream for the separated
	// commanders, combined for the watchdog. 0 (or negative) keeps the
	// commander's documented default. A commander never grows a capture
	// past this bound: the surplus is discarded and recorded.
	OutputCap int
}

// Result is one completed process: the captured streams plus the wait
// status. ExitCode follows the shell `wait` code (128+signo for signaled
// processes). TimedOut reports the watchdog: the budget fired or the
// post-exit sweep found an orphaned tree, so the proof is refused even
// when the exit reads 0. Decoy refines it: the parent finished while its
// tree outlived it (the orphan sweep fired, not the budget), so the
// service records the decoy marker instead of the timeout line.
// StdoutTruncated and StderrTruncated report a capture that hit its cap;
// the returned stream carries a truncation notice in place of the dropped
// bytes.
type Result struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	TimedOut        bool
	Decoy           bool
	StdoutTruncated bool
	StderrTruncated bool
}

// Commander runs one process to completion. The production implementation
// lives in internal/core/exec and owns the process-group watchdog; tests
// inject a scripted commander. No service body calls exec directly.
type Commander interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// MissingToolError marks a missing-tool exit: the adapter prints the
// `missing: <tool>` line and exits 1 with no envelope, never a typed
// refusal. Commanders return it when argv[0] does not resolve (production
// maps the exec look-path failure; scripted commanders return it directly),
// and services pass it through untouched.
type MissingToolError struct {
	Tool string
}

// Error implements error.
func (e *MissingToolError) Error() string { return "missing tool: " + e.Tool }

// OutputCappedError reports a stream that hit its capture cap before the
// child finished. A commander returns it when the capped stream is still
// expected to be a complete document (stdout that callers parse); the
// captured prefix/suffix is still attached to the result, but it is not a
// substitute for the whole stream.
type OutputCappedError struct {
	// Stream names the capped stream ("stdout" or "stderr").
	Stream string
	// Limit is the cap in bytes.
	Limit int
}

// Error implements error.
func (e *OutputCappedError) Error() string {
	return fmt.Sprintf("%s exceeded the %d-byte output cap", e.Stream, e.Limit)
}
