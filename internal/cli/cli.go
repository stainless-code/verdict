// Package cli owns the process-wide CLI contract shared by every verb: the
// global flag strip, the layered preferences resolution (flags > file >
// env > defaults), warning accumulation, and typed refusals rendered either
// as a machine envelope (--json) or human text with frozen exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Exit codes for the machine contract.
const (
	ExitOK      = 0
	ExitRefused = 1
	ExitUsage   = 2
	ExitBroke   = 3
)

// Runner carries the per-invocation CLI state: routing, prefs, warnings.
type Runner struct {
	Out                io.Writer
	Human              io.Writer
	Diag               io.Writer
	Now                func() time.Time
	Effective          Effective
	Output             string
	EnvReviewColorMode string
	Cmd                string
	Rentry             string
	Pr                 string
	Repo               string
	Warnings           []string
	Candidates         []any
	JSONMode           bool
	JSONPeeked         bool
	EnvNoColor         bool
	PrefColorSet       bool
	NoColor            bool
}

// Effective is core's render configuration (alias of core.Effective).
type Effective = core.Effective

// Defaults returns the knob values every verb starts from.
func Defaults() Effective {
	return core.Defaults()
}

// DefaultProductURL and MakerURL are core's product constants.
const (
	DefaultProductURL = core.DefaultProductURL
	DefaultMakerURL   = core.DefaultMakerURL
	DefaultMakerName  = core.DefaultMakerName
	FooterDivider     = core.FooterDivider
)

// NowUTC formats t as UTC to the second.
func NowUTC(t time.Time) string {
	return core.NowUTC(t)
}

// Warn records a warning: `warn: ...` on the diagnostic stream plus the
// WARNINGS_JSON accumulation that lands in the envelope. This is a
// frozen-format writer, not a [slog] call — the bytes are the CLI contract.
// Diagnostic stream writes are un-actionable, so errcheck excludes the
// [fmt.Fprint] family here via .golangci.yml.
func (r *Runner) Warn(msg string) {
	if r.Effective.Color == colorAlways {
		fmt.Fprintf(r.Diag, "\033[33mwarn: %s\033[0m\n", msg)
	} else {
		fmt.Fprintf(r.Diag, "warn: %s\n", msg)
	}
	r.Warnings = append(r.Warnings, msg)
}

// RefusalError is core's typed refusal (alias of core.RefusalError): machine
// code plus human message. It is the only error type the command layer
// renders; matching uses [errors.As].
type RefusalError = core.RefusalError

// Refuse renders a refusal and returns exit 1 (check_gate stays false on the
// record path, so every refusal exits 1): the one exception is a cancel,
// which exits 3 wherever it is rendered (GO-CLI-05, see [Runner.RefuseExit]).
func (r *Runner) Refuse(err error) int {
	return r.RefuseExit(err, ExitRefused)
}

// RefuseExit renders a refusal with an explicit exit code: the gate and
// tool-failure paths pick 3, everything else 1. A `canceled` refusal always
// exits 3 (GO-CLI-05) — see [ExitFor], the single place that rule lives.
func (r *Runner) RefuseExit(err error, code int) int {
	ref, ok := refusalOf(err)
	if !ok {
		ref = &RefusalError{Code: "internal", Message: err.Error()}
	}
	code = ExitFor(ref, code)
	msg := ref.Message + "\n" + HintFor(ref.Code)
	if r.JSONMode {
		env := core.RenderEnvelope(r.Cmd, r.Repo, r.Pr, NowUTC(r.Now()), "",
			r.Warnings, nil, r.refusalObject(ref, msg))
		fmt.Fprintln(r.Out, env.MarshalPretty())
	} else {
		if r.Effective.Color == colorAlways {
			fmt.Fprintf(r.Diag, "\033[31merror: %s\033[0m\n", msg)
		} else {
			fmt.Fprintf(r.Diag, "error: %s\n", msg)
		}
	}
	if r.Rentry != "" {
		fmt.Fprintf(r.Diag, "re-enter: %s\n", r.Rentry)
	}
	return code
}

// ExitFor maps a refusal onto its exit code: a `canceled` refusal always
// exits 3 (GO-CLI-05), because it means the verb never ran, and every other
// refusal keeps the code its caller picked. Renderers that write their own
// envelope (rather than calling [Runner.RefuseExit]) use this so the rule
// lives once.
func ExitFor(ref *RefusalError, code int) int {
	if ref.Code == CodeCanceled {
		return ExitBroke
	}
	return code
}

// Progress adapts a [Runner] to core.Progress for the thin verb adapters.
// Every line reads the runner's *current* streams, so [Runner.RouteHuman]'s
// swap stays effective no matter when the service writes.
type Progress struct {
	R *Runner
}

// Human writes one rendered human line to the runner's human stream.
func (p Progress) Human(line string) { fmt.Fprintln(p.R.Human, line) }

// Warn records and streams one warning through the runner.
func (p Progress) Warn(msg string) { p.R.Warn(msg) }

// Diag writes one raw diagnostic line to the runner's diagnostic stream.
func (p Progress) Diag(line string) { fmt.Fprintln(p.R.Diag, line) }

// RouteHuman sends human progress to stderr in JSON mode (stdout carries
// exactly one envelope) and to stdout otherwise, mirroring json_redirect.
func (r *Runner) RouteHuman() {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// RefuseTyped replays a typed refusal's accumulated warnings and candidates,
// then renders it through [Runner.Refuse] (exit 1, or 3 for a cancel per
// GO-CLI-05). A nil Warnings keeps what the adapter already collected.
func (r *Runner) RefuseTyped(ref *RefusalError) int {
	if ref.Candidates != nil {
		r.Candidates = ref.Candidates
	}
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	return r.Refuse(ref)
}

// Canceled renders the cancellation refusal (GO-CLI-05, exit 3) when ctx is
// already done, naming the verb in the frozen message; otherwise it reports
// false so the caller continues.
func (r *Runner) Canceled(ctx context.Context, verb string) (int, bool) {
	if ctx.Err() == nil {
		return 0, false
	}
	return r.RefuseExit(&RefusalError{Code: CodeCanceled, Message: "canceled before " + verb + " completed"}, ExitBroke), true
}

func (r *Runner) refusalObject(ref *RefusalError, msg string) *jq.Obj {
	errObj := jq.NewObj("code", ref.Code, "message", msg)
	if r.Candidates != nil {
		errObj.Set("candidates", r.Candidates)
	}
	if ref.RetryAt != "" {
		errObj.Set("retry_at", ref.RetryAt)
	}
	return errObj
}

func refusalOf(err error) (*RefusalError, bool) {
	if ref, ok := errors.AsType[*RefusalError](err); ok {
		return ref, true
	}
	return nil, false
}

// UsageError is a plain flag error: message on stderr, exit 2, no envelope.
type UsageError struct {
	Message string
}

// Error implements error.
func (e *UsageError) Error() string { return e.Message }

// ReportUsage prints the message and returns exit 2.
func (r *Runner) ReportUsage(err *UsageError) int {
	fmt.Fprintln(r.Diag, err.Message)
	return ExitUsage
}

// HintFor returns the remediation hint for a refusal code. The table
// lives in core so every transport renders the same hints (see
// [core.HintFor]).
func HintFor(code string) string { return core.HintFor(code) }

// SortedKeys returns the sorted keys of m (jq `keys[]` order).
func SortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
