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
		Pack:    f.pack,
		Propose: f.propose, FromBundle: f.fromBundle, Accept: f.accept, Reject: f.reject, DryRun: f.dryRun,
		Rule:       f.rule,
		Scope:      f.scope,
		ID:         f.id,
		Provenance: f.provenance,
		Rigor:      f.rigor,
		Reason:     f.reason,
		Retire:     f.retire,
		Restore:    f.restore,
		Status:     f.status,
		Incident:   f.incident,
		Sha:        f.sha,
		Paths:      f.paths,
		Note:       f.note,
		URL:        f.url,
		At:         f.at,
		JSONMode:   r.JSONMode,
	}
	out.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Now: r.Now, Progress: cli.Progress{R: r}},
		RepoRoot: repoRoot, Repo: f.fromBundle, Pr: f.pr,
	}
	return out
}

// flags carries the parsed learn options.
type flags struct {
	fromBundle string
	pr         string
	accept     string
	reject     string
	pack       string
	rule       string
	scope      string
	id         string
	provenance string
	rigor      string
	reason     string
	retire     string
	restore    string
	incident   string
	sha        string
	note       string
	url        string
	at         string
	paths      []string
	propose    bool
	dryRun     bool
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
	if a == "--from-bundle" {
		return parseBundle(r, f, args, i)
	}
	if a == "--path" {
		return parsePath(r, f, args, i)
	}
	if toggle, ok := learnToggles[a]; ok {
		toggle(f)
		return i, 0, false
	}
	want, ok := learnWants[a]
	if !ok {
		return i, r.ReportUsage(unknownFlag(a)), true
	}
	v, next, u := cli.FlagValue(args, i, a, want)
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	setFlag(f, a, v)
	return next, 0, false
}

// learnToggles are the flags that take no operand.
var learnToggles = map[string]func(*flags){
	"--propose": func(f *flags) { f.propose = true },
	"--dry-run": func(f *flags) { f.dryRun = true },
	"--status":  func(f *flags) { f.status = true },
}

// unknownFlag reports one unknown learn flag.
func unknownFlag(a string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for learn (want %s)", a, usageFlags)}
}

// learnIDWant is the operand hint shared by the id-taking learn flags, and
// learnTextWant the one the free-text flags print.
const (
	learnIDWant   = "<id>"
	learnTextWant = "<text>"
)

// learnWants names each value flag's accepted operand for the missing-value
// usage error.
var learnWants = map[string]string{
	"--accept": learnIDWant, "--reject": learnIDWant,
	"--pack":       "<directory>",
	"--rule":       learnTextWant,
	"--scope":      "<globs>",
	"--id":         learnIDWant,
	"--provenance": "<s>",
	"--rigor":      "<level>",
	"--retire":     learnIDWant,
	"--restore":    learnIDWant,
	"--reason":     learnTextWant,
	"--incident":   "<bundle>",
	"--sha":        "<sha>",
	"--note":       "<text>",
	"--url":        "<url>",
	"--at":         "<RFC3339>",
}

// parsePath collects one repeatable --path operand.
func parsePath(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, "--path", "<path>")
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	f.paths = append(f.paths, v)
	return next, 0, false
}

// flagSetters stores one value-taking flag in the field it owns.
var flagSetters = map[string]func(*flags, string){
	"--accept":     func(f *flags, v string) { f.accept = v },
	"--reject":     func(f *flags, v string) { f.reject = v },
	"--pack":       func(f *flags, v string) { f.pack = v },
	"--rule":       func(f *flags, v string) { f.rule = v },
	"--scope":      func(f *flags, v string) { f.scope = v },
	"--id":         func(f *flags, v string) { f.id = v },
	"--provenance": func(f *flags, v string) { f.provenance = v },
	"--rigor":      func(f *flags, v string) { f.rigor = v },
	"--retire":     func(f *flags, v string) { f.retire = v },
	"--restore":    func(f *flags, v string) { f.restore = v },
	"--reason":     func(f *flags, v string) { f.reason = v },
	"--incident":   func(f *flags, v string) { f.incident = v },
	"--sha":        func(f *flags, v string) { f.sha = v },
	"--note":       func(f *flags, v string) { f.note = v },
	"--url":        func(f *flags, v string) { f.url = v },
	"--at":         func(f *flags, v string) { f.at = v },
}

// setFlag stores one value-taking flag.
func setFlag(f *flags, name, val string) {
	if set, ok := flagSetters[name]; ok {
		set(f, val)
	}
}

// appendRentry owns the rentry_cmd build (flag order fixed, values elided).
func appendRentry(r *cli.Runner, f *flags) {
	appendLearningRentry(r, f)
	for _, part := range []struct {
		token string
		set   bool
	}{
		{"--incident ... --sha ... --path ...", f.incident != ""},
		{"--pack ... --id ...", f.pack != ""},
		{"--rule ...", f.rule != ""},
		{"--scope ...", f.scope != ""},
		{"--status", f.status},
		{"--retire ...", f.retire != ""},
		{"--restore ...", f.restore != ""},
	} {
		if part.set {
			r.Rentry += " " + part.token
		}
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
	usageFlags = "--propose, --from-bundle, --accept, --reject, --dry-run, " +
		"--pack, --rule, --scope, --id, --provenance, --rigor, --status, --retire, --restore, --reason, " +
		"--incident, --sha, --path, --note, --url, --at, --json"
)

func parseBundle(r *cli.Runner, f *flags, args []string, i int) (int, int, bool) {
	if i+2 >= len(args) {
		return i, r.ReportUsage(&cli.UsageError{Message: "--from-bundle needs <repo> <pr|local-name>"}), true
	}
	f.fromBundle = args[i+1]
	f.pr = args[i+2]
	return i + 2, 0, false
}

func appendLearningRentry(r *cli.Runner, f *flags) {
	if f.propose {
		r.Rentry += " --propose --from-bundle ... ..."
	}
	if f.accept != "" {
		r.Rentry += " --accept ..."
	}
	if f.reject != "" {
		r.Rentry += " --reject ... --reason ..."
	}
	if f.dryRun {
		r.Rentry += " --dry-run"
	}
}
