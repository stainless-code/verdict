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
	verbDrain = "drain"
	wantFlags = "--provider <name>, --max-threads <1-100>, --agent <name>, --timeout <secs>, " +
		"--post, --dry-run (with --post), -- <agent args>, --json"
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
	args     []string
	post     bool
	dryRun   bool
}

// Run executes `drain <owner/repo> <pr> [--provider <p>] [--max-threads <n>]
// [--agent <name> [--timeout <s>] [-- <agent args>]] [--post] [--dry-run]
// [--json]`. Plan and draft modes never write to the host; --agent --post
// plans, drafts, validates, and posts in one run; --post alone validates
// existing drafts locally before any host read. --post --dry-run runs the
// same gates and reads and writes a preview instead of posting.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Cmd, r.Repo, r.Pr = verbDrain, req.Repo, req.Pr
	fl, limit, code, done := parseRunArgs(ctx, r, args, req)
	if done {
		return code
	}
	if fl.agent != "" || !fl.post {
		return runPlanAndMaybePost(ctx, r, req, fl, limit)
	}
	return postMode(ctx, r, req, fl)
}

func parseRunArgs(ctx context.Context, r *cli.Runner, args []string, req Request) (*flags, int, int, bool) {
	fl, code, done := parseFlags(r, args)
	if done {
		return nil, 0, code, true
	}
	if fl.dryRun && !fl.post {
		return nil, 0, r.ReportUsage(&cli.UsageError{Message: "--dry-run requires --post"}), true
	}
	r.RouteHuman()
	if code, done := r.Canceled(ctx, verbDrain); done {
		return nil, 0, code, true
	}
	if ref := preflight(req); ref != nil {
		return nil, 0, r.Refuse(ref), true
	}
	limit, code, done := parseLimit(r, fl.max)
	if done {
		return nil, 0, code, true
	}
	if ref := checkPostProviderCap(fl); ref != nil {
		return nil, 0, r.Refuse(ref), true
	}
	return fl, limit, 0, false
}

func checkPostProviderCap(fl *flags) *core.RefusalError {
	if fl.post && fl.provider != "" {
		return checkThreadReplyCap(fl.provider)
	}
	return nil
}

func runPlanAndMaybePost(ctx context.Context, r *cli.Runner, req Request, fl *flags, limit int) int {
	dir, plan, code, done := buildPlan(ctx, r, req, fl, limit)
	if done {
		return code
	}
	if fl.agent != "" && len(plan.Pending) > 0 {
		if code, done := draft(ctx, r, req, fl, dir, plan); done {
			return code
		}
	}
	if fl.post && len(plan.Pending) > 0 {
		return postMode(ctx, r, req, fl)
	}
	return finishPlan(r, req, fl, dir, plan)
}

func buildPlan(ctx context.Context, r *cli.Runner, req Request, fl *flags, limit int) (string, *draincore.File, int, bool) {
	resp, ref := fetchcore.RunThreads(ctx, refreshRequest(r, req, fl))
	if ref != nil {
		return "", nil, r.Refuse(drainFailed(ref.Message)), true
	}
	r.Warnings = resp.Warnings
	if fl.post {
		if ref := checkThreadReplyCap(resp.Data.Provider); ref != nil {
			return "", nil, r.Refuse(ref), true
		}
	}
	in, ref := readInputs(ctx, req, resp)
	if ref != nil {
		return "", nil, r.Refuse(ref), true
	}
	plan, _ := draincore.NewPlan(resp.Data.Provider, threadscore.PRNumber(req.Pr), resp.Data.HeadSHA,
		req.TimeUTC(), in.self, in.feed, in.ledger, limit)
	if err := draincore.WritePlan(in.dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return "", nil, r.Refuse(drainFailed(fmt.Sprintf("could not write %s/%s: %v", in.dir, draincore.KeyPlanJSON, err))), true
	}
	// A dry run leaves the real bundle untouched; its live read uses a scratch
	// copy that is removed when the refresh returns.
	if !fl.dryRun {
		if err := draincore.WriteProcedure(in.dir); err != nil {
			return "", nil, r.Refuse(drainFailed(fmt.Sprintf("could not write %s/%s: %v", in.dir, draincore.KeyProcedureMD, err))), true
		}
	}
	return in.dir, plan, 0, false
}

func finishPlan(r *cli.Runner, req Request, fl *flags, dir string, plan *draincore.File) int {
	nextText := next(req, fl)
	if fl.post {
		nextText = postSuccessNext(req)
	}
	fmt.Fprintf(r.Human, "» %d pending, %d skipped -> %s/%s\n", len(plan.Pending), len(plan.Skipped), dir, draincore.KeyPlanJSON)
	if r.JSONMode {
		env := core.RenderEnvelope(verbDrain, req.Repo, req.Pr, core.NowUTC(r.Now()), nextText,
			r.Warnings, plan.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return cli.ExitOK
}

type planInputs struct {
	dir    string
	feed   *threadscore.File
	self   string
	ledger []draincore.LedgerEntry
}

func readInputs(ctx context.Context, req Request, resp *fetchcore.ThreadsResponse) (*planInputs, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, drainFailed(ref.Message)
	}
	feed, err := threadscore.ReadFile(dir + pathSep + threadscore.KeyThreadsJSON)
	if err != nil {
		return nil, drainFailed(fmt.Sprintf("unreadable %s/threads.json: %v", dir, err))
	}
	return inputsFrom(ctx, req, resp, dir, feed)
}

func inputsFrom(ctx context.Context, req Request, resp *fetchcore.ThreadsResponse,
	dir string, feed *threadscore.File,
) (*planInputs, *core.RefusalError) {
	if resp.Data.Truncated || feed.Truncated {
		return nil, drainFailed(dir + "/threads.json is truncated — refresh threads before draining")
	}
	self, ref := selfLogin(ctx, req, resp.Data.Provider)
	if ref != nil {
		return nil, ref
	}
	ledger, err := ReadLedger(dir + pathSep + draincore.KeyLedgerJSON)
	if err != nil {
		return nil, drainFailed(err.Error())
	}
	return &planInputs{dir: dir, feed: feed, self: self, ledger: ledger}, nil
}

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

func drainFailed(msg string) *core.RefusalError {
	return &core.RefusalError{Code: "drain-failed", Message: msg}
}

func parseLimit(r *cli.Runner, text string) (int, int, bool) {
	limit, err := strconv.Atoi(text)
	if err != nil || limit < 1 || limit > draincore.MaxMaxThreads {
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"--max-threads must be 1-%d, got '%s'", draincore.MaxMaxThreads, text)}), true
	}
	return limit, 0, false
}

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

func next(req Request, fl *flags) string {
	if fl.agent != "" {
		return "post replies with verdict drain " + req.Repo + " " + req.Pr + " --post"
	}
	return "draft replies with verdict drain " + req.Repo + " " + req.Pr + " --agent <name>"
}

func refreshRequest(r *cli.Runner, req Request, fl *flags) *fetchcore.ThreadsRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	if read.Commander == nil {
		read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	}
	return &fetchcore.ThreadsRequest{
		ReadRequest: read,
		Provider:    fl.provider,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

func parseFlags(r *cli.Runner, args []string) (*flags, int, bool) {
	fl := &flags{max: defaultMax, args: append([]string{}, args...)}
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

func consumeFlag(r *cli.Runner, fl *flags, args []string, i int) (int, int, bool) {
	a := args[i]
	switch {
	case a == "--post":
		fl.post = true
		return i, 0, false
	case a == "--dry-run":
		fl.dryRun = true
		return i, 0, false
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
		return nil, jq.NewErrf("unreadable %s: %v", path, err)
	}
	var entries []draincore.LedgerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, jq.NewErrf("unreadable %s: %v", path, err)
	}
	return entries, nil
}

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
	if launch.TimeoutRaw == "" {
		launch.TimeoutRaw = runverb.DefaultTimeoutS
	}
	res, ref := runlaunch.Drain(ctx, launch, plan.PlanDigest)
	if ref != nil {
		return r.Refuse(ref), true
	}
	r.Warnings = res.Warnings
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
