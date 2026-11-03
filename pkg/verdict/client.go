package verdict

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// RefusalError is a typed refusal: machine code plus human message. Match it
// with [errors.As]; every Client method returns one (or a MissingToolError)
// instead of raw core errors.
type RefusalError struct {
	Code     string
	Message  string
	Hint     string
	Warnings []string
}

// Error implements error.
func (e *RefusalError) Error() string { return e.Code + ": " + e.Message }

// MissingToolError marks a missing-tool exit: the named tool does not
// resolve on PATH. Match it with [errors.As].
type MissingToolError struct {
	Tool string
}

// Error implements error.
func (e *MissingToolError) Error() string { return "missing tool: " + e.Tool }

// Progress observes human-facing lines. It mirrors the core observer seam so
// no core type appears in a public signature; a nil Progress discards.
type Progress interface {
	Human(line string)
	Warn(msg string)
	Diag(line string)
}

// Command is one spawned process: argv, stdin, and environment.
type Command struct {
	Argv  []string
	Stdin io.Reader
	Env   []string
}

// CommandResult is one completed process: captured streams plus wait status.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	Decoy    bool
}

// Commander runs one process to completion. The production client shells out
// to the real gh/git; tests inject a scripted Commander.
type Commander interface {
	Run(ctx context.Context, cmd Command) (CommandResult, error)
}

// Options configures a Client.
type Options struct { //nolint:govet // field order is the public API surface
	// Dir is the checkout carrying .review/. It anchors tool-version and
	// provenance lookups; bundle files still resolve from the process
	// working directory, like the CLI.
	Dir string
	// Env is the injected environment snapshot (never the process env).
	Env map[string]string
	// Now is the injected clock; defaults to time.Now.
	Now func() time.Time
	// Progress observes human lines and warnings; nil discards.
	Progress Progress
	// Commander runs subprocesses; defaults to the production watchdog.
	Commander Commander
}

// Client runs review operations in-process. It owns no behavior: every
// method converts a typed request, calls one core service, and converts the
// typed response back.
type Client struct { //nolint:govet // field order is the public API surface
	dir       string
	env       map[string]string
	now       func() time.Time
	progress  Progress
	commander core.Commander
}

// New builds a Client. The zero Options yields a usable client with the wall
// clock, no environment, discarded progress, and the real command runner.
func New(o Options) *Client {
	env := map[string]string{}
	maps.Copy(env, o.Env)
	now := o.Now
	if now == nil {
		now = time.Now
	}
	var commander core.Commander = exec.Watchdog{}
	if o.Commander != nil {
		commander = &commanderAdapter{c: o.Commander}
	}
	return &Client{dir: o.Dir, env: env, now: now, progress: o.Progress, commander: commander}
}

// Close releases client resources. It is a no-op today, kept so embedding
// code never needs a second revision when a resource appears.
func (c *Client) Close() error { return nil }

// Invocation carries the repo/PR positionals shared by most requests.
type Invocation struct {
	Repo string
	PR   string
}

// Result carries the envelope fields shared by bundle-scoped responses.
// Data is the verb's data profile as JSON, byte-identical to the --json
// envelope's data object.
type Result struct {
	Repo     string
	PR       string
	At       string
	Next     string
	Warnings []string
	Data     json.RawMessage
}

// Outcome carries the envelope fields for verbs without repo/PR positionals.
type Outcome struct {
	At       string
	Next     string
	Warnings []string
	Data     json.RawMessage
}

// Finding is one ranked finding: locator fields plus the full store record.
type Finding struct { //nolint:govet // field order is the public API surface
	Sig      string
	Status   string
	Locator  string
	Rank     int
	Anchored any
	Raw      json.RawMessage
}

// Artifact is one rendered output file. The service returns bytes; writing
// them is the caller's decision.
type Artifact struct {
	Path    string
	Content []byte
}

// read builds the shared invocation: injected runtime plus positionals.
func (c *Client) read(repo, pr string) core.ReadRequest {
	root := c.dir
	return core.ReadRequest{
		Runtime: &core.Runtime{Env: c.env, Now: c.now, Progress: c.progress, Commander: c.commander},
		Repo:    repo,
		Pr:      pr,
		Forces:  &core.GlobalForces{},
		Root:    &root,
	}
}

// readBare builds an invocation for verbs without repo/PR positionals.
func (c *Client) readBare() core.ReadRequest {
	return c.read("", "")
}

// refusalErr converts a core refusal to the public error. Nil stays nil so
// callers can return it directly.
func refusalErr(ref *core.RefusalError) error {
	if ref == nil {
		return nil
	}
	return &RefusalError{
		Code:     ref.Code,
		Message:  ref.Message,
		Hint:     core.HintFor(ref.Code),
		Warnings: append([]string{}, ref.Warnings...),
	}
}

// convertErr converts any service error: typed refusals and missing tools
// become public errors, anything else passes through untouched.
func convertErr(err error) error {
	if err == nil {
		return nil
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return refusalErr(ref)
	}
	if missing, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &MissingToolError{Tool: missing.Tool}
	}
	return err
}

// wireJSON renders a core data profile to envelope-identical JSON. Ordered
// profiles render through [jq.Obj.MarshalCompact] so the stored key order
// survives; jq.Compact alone would encode the Obj's Go fields.
func wireJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	w := core.Wire(v)
	if w == nil {
		return nil
	}
	if obj, ok := w.(*jq.Obj); ok {
		return json.RawMessage(obj.MarshalCompact())
	}
	return json.RawMessage(jq.Compact(w))
}

// resultOf builds the shared bundle-scoped envelope.
func resultOf(repo, pr, at, next string, warnings []string, data any) Result {
	return Result{Repo: repo, PR: pr, At: at, Next: next, Warnings: warnings, Data: wireJSON(data)}
}

// resultPtr builds the shared bundle-scoped envelope as a pointer.
func resultPtr(repo, pr, at, next string, warnings []string, data any) *Result {
	r := resultOf(repo, pr, at, next, warnings, data)
	return &r
}

// outcomeOf builds the shared positionals-free envelope.
func outcomeOf(at, next string, warnings []string, data any) Outcome {
	return Outcome{At: at, Next: next, Warnings: warnings, Data: wireJSON(data)}
}

// outcomePtr builds the shared positionals-free envelope as a pointer.
func outcomePtr(at, next string, warnings []string, data any) *Outcome {
	o := outcomeOf(at, next, warnings, data)
	return &o
}

// findingOf converts one core finding item to the public shape.
func findingOf(item *core.FindingItem) *Finding {
	if item == nil {
		return nil
	}
	return &Finding{
		Sig:      item.Sig,
		Status:   item.Status,
		Locator:  item.Locator,
		Rank:     item.Rank,
		Anchored: item.Anchored,
		Raw:      wireJSON(item.Wire()),
	}
}

// artifactOf converts one core file artifact to the public shape.
func artifactOf(a *core.FileArtifact) *Artifact {
	if a == nil {
		return nil
	}
	return &Artifact{Path: a.Path, Content: a.Content}
}

// commanderAdapter lets an injected facade Commander serve the core seam.
type commanderAdapter struct {
	c Commander
}

// commanderError marks a failure of the injected command runner while
// preserving the cause chain for [errors.As].
type commanderError struct {
	err error
}

// Error implements error.
func (e *commanderError) Error() string { return "verdict: commander: " + e.err.Error() }

// Unwrap returns the runner's cause.
func (e *commanderError) Unwrap() error { return e.err }

// Run implements core.Commander.
func (a *commanderAdapter) Run(ctx context.Context, cmd core.Command) (core.Result, error) {
	res, err := a.c.Run(ctx, Command{Argv: cmd.Argv, Stdin: cmd.Stdin, Env: cmd.Env})
	if err != nil {
		return core.Result{}, &commanderError{err: err}
	}
	return core.Result{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode, TimedOut: res.TimedOut, Decoy: res.Decoy}, nil
}

// streamWriter routes a service's human/diagnostic writer onto progress
// lines, or discards when no observer is installed.
func (c *Client) streamWriter(human bool) io.Writer {
	if c.progress == nil {
		return io.Discard
	}
	return &lineWriter{onLine: func(line string) {
		if human {
			c.progress.Human(line)
		} else {
			c.progress.Diag(line)
		}
	}}
}

// lineWriter forwards complete newline-terminated lines.
type lineWriter struct {
	onLine func(string)
	buf    []byte
}

// Write implements [io.Writer].
func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.onLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}
