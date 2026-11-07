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
func Run(ctx context.Context, r *cli.Runner, args []string, repoRoot string) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "verdict learn"
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	appendRentry(r, &f)
	if code, done := r.Canceled(ctx, "learn"); done {
		return code
	}
	resp, ref := learncore.Run(ctx, learnRequest(r, &f, repoRoot))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("learn", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return cli.ExitOK
}

// learnRequest injects the runner's clock and progress observer into the
// typed service request.
func learnRequest(r *cli.Runner, f *flags, repoRoot string) *learncore.Request {
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
		Runtime:  &core.Runtime{Now: r.Now, Progress: cli.Progress{R: r}},
		RepoRoot: repoRoot,
	}
	return out
}

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
		next, code, done := parseToken(r, &f, args, i)
		if done {
			return f, code, true
		}
		i = next
	}
	return f, 0, false
}

// parseToken consumes one learn argument, returning the next index plus a
// usage refusal when the token is unknown or a value flag lost its operand.
func parseToken(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	if want, ok := learnWants[a]; ok {
		v, next, u := cli.FlagValue(args, i, a, want)
		if u != nil {
			return i, r.ReportUsage(u), true
		}
		setFlag(f, a, v)
		return next, 0, false
	}
	if a != "--status" {
		return i, r.ReportUsage(unknownFlag(a)), true
	}
	f.status = true
	return i, 0, false
}

// unknownFlag reports one unknown learn flag.
func unknownFlag(a string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for learn (want %s)", a, usageFlags)}
}

// learnIDWant is the operand hint shared by the id-taking learn flags.
const learnIDWant = "<id>"

// learnWants names each value flag's accepted operand for the missing-value
// usage error.
var learnWants = map[string]string{
	"--rule":       "<text>",
	"--scope":      "<globs>",
	"--id":         learnIDWant,
	"--provenance": "<s>",
	"--rigor":      "<level>",
	"--retire":     learnIDWant,
	"--restore":    learnIDWant,
	"--reason":     "<text>",
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

// appendRentry owns the rentry_cmd build (flag order fixed, values elided).
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
	// usageFlags lists the accepted flags for the unknown-flag message.
	usageFlags = "--rule, --scope, --id, --provenance, --rigor, --status, --retire, --restore, --reason, --json"
)
