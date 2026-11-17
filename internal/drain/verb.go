// Package drain is the drain verb's CLI adapter: argv parsing, the threads
// refresh, plan selection, and the machine envelope. It never posts.
package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	runlaunch "github.com/stainless-code/verdict/internal/core/run"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	runverb "github.com/stainless-code/verdict/internal/run"
)

const (
	verbDrain  = "drain"
	wantFlags  = "--provider <name>, --max-threads <1-100>, --agent <name>, --timeout <secs>, -- <agent args>, --json"
	defaultMax = "20"
	pathSep    = "/"
)

// Request wires a drain run.
type Request = readcommon.Request

type flags struct {
	provider string
	max      string
	agent    string
	timeout  string
	pass     []string
}

// Run executes `drain <owner/repo> <pr> [--provider <p>] [--max-threads <n>]
// [--agent <name> [--timeout <s>] [-- <agent args>]] [--json]`. It refreshes
// threads through the same service the threads verb uses, then writes one
// plan; there is no host write and no --post in this build.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	fl, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.RouteHuman()
	if code, done := r.Canceled(ctx, verbDrain); done {
		return code
	}
	if ref := preflight(req); ref != nil {
		return r.Refuse(ref)
	}
	resp, ref := fetchcore.RunThreads(ctx, refreshRequest(r, req, fl))
	if ref != nil {
		return r.Refuse(drainFailed(ref.Message))
	}
	return finish(ctx, r, req, fl, resp)
}

// preflight refuses a bundle that cannot supply the plan inputs before any
// host read or local write.
func preflight(req Request) *core.RefusalError {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return drainFailed(ref.Message)
	}
	if !readcommon.IsDir(dir) {
		return drainFailed(fmt.Sprintf("missing bundle %s — fetch the PR first", dir))
	}
	return nil
}

// drainFailed restates a failed input in the drain family, so one code names
// every input that can keep a plan from being honest.
func drainFailed(msg string) *core.RefusalError {
	return &core.RefusalError{Code: "drain-failed", Message: msg}
}

// finish reads the refreshed feed, the login, and the ledger, then writes the
// plan. Every refusal here precedes the plan write.
func finish(ctx context.Context, r *cli.Runner, req Request, fl *flags, resp *fetchcore.ThreadsResponse) int {
	in, code, done := readInputs(ctx, r, req, fl, resp)
	if done {
		return code
	}
	plan, _ := draincore.NewPlan(resp.Data.Provider, threadscore.PRNumber(req.Pr), resp.Data.HeadSHA,
		req.TimeUTC(), in.self, in.feed, in.ledger, in.limit)
	if err := draincore.WritePlan(in.dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return r.Refuse(drainFailed(fmt.Sprintf("could not write %s/%s: %v", in.dir, draincore.KeyPlanJSON, err)))
	}
	if fl.agent != "" && len(plan.Pending) > 0 {
		code, done := draft(ctx, r, req, fl, in.dir, plan)
		if done {
			return code
		}
	}
	r.Warnings = append(r.Warnings, resp.Warnings...)
	fmt.Fprintf(r.Human, "» %d pending, %d skipped -> %s/%s\n", len(plan.Pending), len(plan.Skipped), in.dir, draincore.KeyPlanJSON)
	if r.JSONMode {
		env := core.RenderEnvelope(verbDrain, req.Repo, req.Pr, core.NowUTC(r.Now()), next(req, fl),
			r.Warnings, plan.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

// planInputs are the bundle facts every plan is built from.
type planInputs struct {
	dir    string
	feed   *threadscore.File
	self   string
	ledger []draincore.LedgerEntry
	limit  int
}

// readInputs resolves the plan inputs before the plan file is touched.
func readInputs(ctx context.Context, r *cli.Runner, req Request, fl *flags, resp *fetchcore.ThreadsResponse) (*planInputs, int, bool) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, r.Refuse(drainFailed(ref.Message)), true
	}
	feed, err := threadscore.ReadFile(dir + pathSep + threadscore.KeyThreadsJSON)
	if err != nil {
		return nil, r.Refuse(drainFailed(fmt.Sprintf("unreadable %s/threads.json: %v", dir, err))), true
	}
	if resp.Data.Truncated || feed.Truncated {
		return nil, r.Refuse(drainFailed(dir + "/threads.json is truncated — refresh threads before draining")), true
	}
	self, ref := selfLogin(ctx, req, resp.Data.Provider)
	if ref != nil {
		return nil, r.Refuse(ref), true
	}
	ledger, err := ReadLedger(dir + pathSep + draincore.KeyLedgerJSON)
	if err != nil {
		return nil, r.Refuse(drainFailed(err.Error())), true
	}
	limit, code, done := parseLimit(r, fl.max)
	if done {
		return nil, code, true
	}
	return &planInputs{dir: dir, feed: feed, self: self, ledger: ledger, limit: limit}, 0, false
}

// parseLimit turns the flag text into the plan's cap before any plan is built.
func parseLimit(r *cli.Runner, text string) (int, int, bool) {
	limit, err := strconv.Atoi(text)
	if err != nil || limit < 1 || limit > draincore.MaxMaxThreads {
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"--max-threads must be 1-%d, got '%s'", draincore.MaxMaxThreads, text)}), true
	}
	return limit, 0, false
}

// selfLogin reads verdict's own login once, as threads resolve does.
func selfLogin(ctx context.Context, req Request, provider string) (string, *core.RefusalError) {
	var cmdr core.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	if req.Commander != nil {
		cmdr = req.Commander
	}
	self, err := postcore.SelfLogin(ctx, cmdr, provider, req.Env)
	if err != nil || self == "" {
		return "", drainFailed(fmt.Sprintf("could not read authenticated login for %s: %v", provider, err))
	}
	return self, nil
}

// next names the command this run did not do.
func next(req Request, fl *flags) string {
	if fl.agent != "" {
		return "post the drafts with verdict drain " + req.Repo + " " + req.Pr + " --post"
	}
	return "draft replies with verdict drain " + req.Repo + " " + req.Pr + " --agent <name>"
}

// refreshRequest reuses the threads verb's refresh path and commander, with
// the drain warnings carried through to its envelope.
func refreshRequest(r *cli.Runner, req Request, fl *flags) *fetchcore.ThreadsRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	return &fetchcore.ThreadsRequest{
		ReadRequest: read,
		Provider:    fl.provider,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags accepts the PR A flag set. --post is deliberately unknown here:
// host writes belong to the later PR.
func parseFlags(r *cli.Runner, args []string) (*flags, int, bool) {
	fl := &flags{max: defaultMax}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			fl.pass = append(fl.pass, args[i+1:]...)
			return fl, 0, false
		}
		nextIndex, code, done := consumeFlag(r, fl, args, i)
		if done {
			return nil, code, true
		}
		i = nextIndex
	}
	return fl, 0, false
}

// consumeFlag parses one flag and returns the index of its last operand.
func consumeFlag(r *cli.Runner, fl *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	switch {
	case a == "--provider" || a == "--max-threads" || a == "--agent" || a == "--timeout":
		v, nextIndex, u := cli.FlagValue(args, i, a, "<value>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		setFlag(fl, a, v)
		return nextIndex, 0, false
	case strings.Contains(a, "=") && setEqualsFlag(fl, a):
		return i, 0, false
	default:
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for %s (want %s)", a, verbDrain, wantFlags)}), true
	}
}

// setEqualsFlag handles --name=value forms and reports whether one matched.
func setEqualsFlag(fl *flags, a string) bool {
	for name, target := range map[string]*string{
		"--provider=": &fl.provider, "--max-threads=": &fl.max,
		"--agent=": &fl.agent, "--timeout=": &fl.timeout,
	} {
		if value, ok := strings.CutPrefix(a, name); ok {
			*target = value
			return true
		}
	}
	return false
}

// setFlag stores one value flag's operand.
func setFlag(fl *flags, name, value string) {
	switch name {
	case "--provider":
		fl.provider = value
	case "--max-threads":
		fl.max = value
	case "--agent":
		fl.agent = value
	case "--timeout":
		fl.timeout = value
	}
}

// ReadLedger reads the reply ledger written by address-reviews and drain. An
// absent ledger is an empty one; a corrupt ledger refuses instead of being
// read as "nobody answered".
func ReadLedger(path string) ([]draincore.LedgerEntry, error) {
	raw, err := jq.CappedFile(path, jq.MaxPRBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []draincore.LedgerEntry{}, nil
		}
		return nil, fmt.Errorf("unreadable %s: %w", path, err) //nolint:wrapcheck // names the local ledger path
	}
	var entries []draincore.LedgerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("unreadable %s: %w", path, err) //nolint:wrapcheck // names the local ledger path
	}
	return entries, nil
}

// draft launches one agent through the run service against the plan just
// written, then validates the single artifact it must leave behind.
func draft(ctx context.Context, r *cli.Runner, req Request, fl *flags, dir string, plan *draincore.File) (int, bool) {
	launch := &core.RunRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Watchdog{},
		},
		Repo: req.Repo, Pr: req.Pr, RepoRoot: req.RepoRoot,
		LookPath:    exec.LookPath,
		PassThrough: fl.pass,
		Environ:     os.Environ(),
		Agent:       fl.agent,
		TimeoutRaw:  fl.timeout,
		Warnings:    append([]string{}, r.Warnings...),
	}
	launch.TimeoutRaw = fl.timeout
	if launch.TimeoutRaw == "" {
		launch.TimeoutRaw = runverb.DefaultTimeoutS
	}
	res, ref := runlaunch.Drain(ctx, launch, plan.PlanDigest)
	if ref != nil {
		return r.Refuse(ref), true
	}
	r.Warnings = append(r.Warnings, res.Warnings...)
	raw, err := jq.CappedFile(dir+pathSep+draincore.KeyDraftsJSON, jq.MaxPRBytes)
	if err != nil {
		return r.Refuse(&core.RefusalError{Code: "no-review-artifact", Message: err.Error()}), true
	}
	summary, skipped, ref := draincore.CheckDrafts(raw, plan)
	if ref != nil {
		return r.Refuse(ref), true
	}
	plan.Drafts = summary
	plan.Skipped = append(plan.Skipped, skipped...)
	if err := draincore.WritePlan(dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return r.Refuse(drainFailed(err.Error())), true
	}
	return 0, false
}
