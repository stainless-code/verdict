// Package learn is the learn verb's CLI adapter: argv parsing, rentry, one
// call into the learncore service, envelope rendering, and exit mapping.
// Domain logic — the KB store's append/status/veto modes — lives in
// internal/core/learn.
package learn

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
)

// Run executes `learn [--rule ... --scope ...] [--status] [--retire ...]
// [--restore ...] [--reason ...] [--id/--provenance/--rigor] [--json]`,
// returning the process exit code. Learn takes no repo/pr positionals.
func Run(ctx context.Context, r *cli.Runner, args []string, _ map[string]string) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "verdict learn"
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	appendRentry(r, &f)
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: msgCanceled}, cli.ExitBroke)
	}
	resp, ref := learncore.Run(ctx, learnRequest(r, &f))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("learn", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return cli.ExitOK
}

// refuse replays the service's accumulated warnings before rendering.
func refuse(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	return r.Refuse(ref)
}

// learnRequest injects the runner's clock and progress observer into the
// typed service request.
func learnRequest(r *cli.Runner, f *flags) *learncore.Request {
	out := &learncore.Request{
		Rule:       f.rule,
		Scope:      f.scope,
		ID:         f.id,
		Provenance: f.provenance,
		Rigor:      f.rigor,
		Reason:     f.reason,
		Retire:     f.retire,
		Restore:    f.restore,
		Status:     f.status,
		JSONMode:   r.JSONMode,
	}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Now: r.Now, Progress: runnerProgress{r}},
	}
	return out
}

// runnerProgress routes core's progress observations onto the runner's
// frozen streams.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// flags carries the parsed learn options.
type flags struct {
	rule       string
	scope      string
	id         string
	provenance string
	rigor      string
	reason     string
	retire     string
	restore    string
	status     bool
}

// parseFlags strips learn's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{provenance: defaultProvenance, rigor: rigorPointed}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--rule", "--scope", "--id", "--provenance", "--rigor", "--retire", "--restore", "--reason":
			if i+1 < len(args) {
				i++
				setFlag(&f, a, args[i])
			} else {
				setFlag(&f, a, "")
			}
		case "--status":
			f.status = true
		case "--json":
		default:
			return f, r.ReportUsage(unknownFlag(a)), true
		}
	}
	return f, 0, false
}

// unknownFlag reports one unknown learn flag.
func unknownFlag(a string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for learn (want %s)", a, usageFlags)}
}

// setFlag stores one value-taking flag.
func setFlag(f *flags, name, val string) {
	switch name {
	case "--rule":
		f.rule = val
	case "--scope":
		f.scope = val
	case "--id":
		f.id = val
	case "--provenance":
		f.provenance = val
	case "--rigor":
		f.rigor = val
	case "--retire":
		f.retire = val
	case "--restore":
		f.restore = val
	case "--reason":
		f.reason = val
	}
}

// appendRentry ports the rentry_cmd build (flag order fixed, values elided).
func appendRentry(r *cli.Runner, f *flags) {
	if f.rule != "" {
		r.Rentry += " --rule ..."
	}
	if f.scope != "" {
		r.Rentry += " --scope ..."
	}
	if f.status {
		r.Rentry += " --status"
	}
	if f.retire != "" {
		r.Rentry += " --retire ..."
	}
	if f.restore != "" {
		r.Rentry += " --restore ..."
	}
	if r.JSONMode {
		r.Rentry += " --json"
	}
}

// Adapter-only constants shared with the service's frozen contract.
const (
	// defaultProvenance is the --provenance default.
	defaultProvenance = "manual"
	// rigorPointed is the --rigor default.
	rigorPointed = "pointed"
	// codeCanceled names the cancel refusal the adapter exits 3 on.
	codeCanceled = "canceled"
	// msgCanceled is the frozen cancel message.
	msgCanceled = "canceled before learn completed"
	// usageFlags lists the accepted flags for the unknown-flag message.
	usageFlags = "--rule, --scope, --id, --provenance, --rigor, --status, --retire, --restore, --reason, --json"
)
