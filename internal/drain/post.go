package drain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

const (
	scratchDirMode  = 0o755
	scratchFileMode = 0o600
)

type dryRunWriteError struct{}

func (dryRunWriteError) Error() string { return "dry run attempted a write" }

func acquireDrainLock(req Request) (func(), *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, drainFailed(ref.Message)
	}
	unlock, err := storelock.Acquire(filepath.Join(dir, verbDrain), storelock.DefaultWait)
	if err != nil {
		return nil, drainFailed(fmt.Sprintf("could not lock %s: %v", dir, err))
	}
	return unlock, nil
}

func postMode(ctx context.Context, r *cli.Runner, req Request, fl *flags) int {
	unlock, ref := acquireDrainLock(req)
	if ref != nil {
		return r.Refuse(ref)
	}
	defer unlock()
	dir, plan, drafts, ref := postPreflight(req, fl)
	if ref != nil {
		return r.Refuse(ref)
	}
	if fl.dryRun {
		return runDryRunPost(ctx, r, req, fl, dir, plan, drafts)
	}
	return runRealPost(ctx, r, req, fl, dir, plan, drafts)
}

func postFromPlan(ctx context.Context, r *cli.Runner, req Request, fl *flags,
	in *planInputs, plan *draincore.File,
) int {
	drafts, ref := postDraftsFile(in.dir)
	if ref != nil {
		return r.Refuse(ref)
	}
	if _, _, _, ref := draincore.ValidatePostDrafts(plan, drafts, plan.Provider); ref != nil {
		return r.Refuse(ref)
	}
	// Drafting can outlast changes on the host, so refresh under the post lock before writing.
	if fl.dryRun {
		return runDryRunPost(ctx, r, req, fl, in.dir, plan, drafts)
	}
	return runRealPost(ctx, r, req, fl, in.dir, plan, drafts)
}

func runDryRunPost(ctx context.Context, r *cli.Runner, req Request, fl *flags, dir string,
	plan *draincore.File, drafts []byte,
) int {
	resp, feed, ref := dryRunRefresh(ctx, r, req, fl, dir)
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	in, ref := dryRunInputs(ctx, req, resp, dir, feed)
	if ref != nil {
		return r.Refuse(ref)
	}
	result, ref := postWrites(ctx, req, dir, plan, drafts, in, true)
	if ref != nil {
		return r.Refuse(ref)
	}
	return postDryRunFinish(r, req, dir, plan, fl, result)
}

func runRealPost(ctx context.Context, r *cli.Runner, req Request, fl *flags, dir string,
	plan *draincore.File, drafts []byte,
) int {
	resp, ref := fetchcore.RunThreads(ctx, refreshRequest(r, req, fl))
	if ref != nil {
		return r.Refuse(drainFailed(ref.Message))
	}
	r.Warnings = resp.Warnings
	in, ref := readInputs(ctx, req, resp)
	if ref != nil {
		return r.Refuse(ref)
	}
	result, ref := postWrites(ctx, req, dir, plan, drafts, in, false)
	if ref != nil {
		return r.Refuse(ref)
	}
	return postFinish(r, req, dir, plan, result)
}

func postPreflight(req Request, fl *flags) (string, *draincore.File, []byte, *core.RefusalError) {
	if fl.provider != "" {
		if ref := checkThreadReplyCap(fl.provider); ref != nil {
			return "", nil, nil, ref
		}
	}
	dir, plan, ref := postPlan(req, fl)
	if ref != nil {
		return "", nil, nil, ref
	}
	drafts, ref := postDraftsFile(dir)
	if ref != nil {
		return "", nil, nil, ref
	}
	if _, _, _, ref := draincore.ValidatePostDrafts(plan, drafts, plan.Provider); ref != nil {
		return "", nil, nil, ref
	}
	return dir, plan, drafts, nil
}

func checkThreadReplyCap(provider string) *core.RefusalError {
	caps := core.CapabilitiesFor(provider)
	if !caps.ThreadReply {
		return &core.RefusalError{Code: "provider-unimplemented", Message: core.Msgf(
			"thread replies on %s are not implemented (%s)", provider, caps.ThreadReplyReason())}
	}
	return nil
}

func postPlan(req Request, fl *flags) (string, *draincore.File, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return "", nil, drainFailed(ref.Message)
	}
	plan, err := draincore.ReadPlan(dir + pathSep + draincore.KeyPlanJSON)
	if err != nil {
		return "", nil, drainFailed(err.Error())
	}
	if ref := planProvider(plan.Provider, fl.provider); ref != nil {
		return "", nil, ref
	}
	return dir, plan, nil
}

func planProvider(planned, requested string) *core.RefusalError {
	if requested != "" && requested != planned {
		return drainFailed(fmt.Sprintf("--provider %s disagrees with the plan's %s", requested, planned))
	}
	return nil
}

func postDraftsFile(dir string) ([]byte, *core.RefusalError) {
	raw, err := jq.CappedFile(dir+pathSep+draincore.KeyDraftsJSON, jq.MaxPRBytes)
	if err == nil {
		return raw, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return nil, drainFailed(fmt.Sprintf("unreadable %s/%s: %v", dir, draincore.KeyDraftsJSON, err))
}

func postWrites(ctx context.Context, req Request, dir string, plan *draincore.File,
	drafts []byte, in *planInputs, dryRun bool,
) (*draincore.PostResult, *core.RefusalError) {
	provider := plan.Provider
	cmdr := commander(req)
	draftsReq := &draincore.PostRequest{
		Plan: plan, DraftsRaw: drafts, Feed: in.feed, Ledger: in.ledger,
		Provider: provider, Repo: req.Repo, PR: req.Pr, At: req.TimeUTC(),
		DryRun:   dryRun,
		Reply:    replyWriter(ctx, cmdr, req, provider),
		Resolve:  resolveWriter(ctx, cmdr, req, provider),
		Append:   appendLedgerWriter(dir),
		SaveFeed: saveFeedWriter(dir),
	}
	if dryRun {
		blockDryRunWrites(draftsReq)
	}
	result, ref := draincore.Post(draftsReq)
	if ref != nil {
		return nil, ref
	}
	if dryRun {
		return result, nil
	}
	if err := draincore.WritePlan(dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return nil, drainFailed(err.Error())
	}
	return result, nil
}

func blockDryRunWrites(req *draincore.PostRequest) {
	blocked := dryRunWriteError{}
	req.Reply = func(string, string, string) (string, error) { return "", blocked }
	req.Resolve = func(string) error { return blocked }
	req.Append = func(*draincore.LedgerEntry) error { return blocked }
	req.SaveFeed = func(*threadscore.File) error { return blocked }
}

func replyWriter(ctx context.Context, cmdr core.Commander, req Request, provider string,
) func(string, string, string) (string, error) {
	return func(rootID, threadID, body string) (string, error) {
		id, ref := postcore.ReplyThread(ctx, cmdr, provider, req.Env, req.Repo, req.Pr, rootID, threadID, body)
		if ref != nil {
			return "", jq.NewErrf("reply to %s: %v", threadID, ref)
		}
		return id, nil
	}
}

func resolveWriter(ctx context.Context, cmdr core.Commander, req Request, provider string) func(string) error {
	return func(threadID string) error {
		if ref := postcore.ResolveThread(ctx, cmdr, provider, req.Env, req.Repo, req.Pr, threadID); ref != nil {
			return jq.NewErrf("resolve %s: %v", threadID, ref)
		}
		return nil
	}
}

func appendLedgerWriter(dir string) func(*draincore.LedgerEntry) error {
	return func(entry *draincore.LedgerEntry) error {
		return draincore.AppendLedger(dir+pathSep+draincore.KeyLedgerJSON, entry)
	}
}

func saveFeedWriter(dir string) func(*threadscore.File) error {
	return func(feed *threadscore.File) error {
		return threadscore.WriteFile(dir+pathSep+threadscore.KeyThreadsJSON, feed)
	}
}

type quietProgress struct{ r *cli.Runner }

func (q quietProgress) Human(string) {}

func (q quietProgress) Warn(msg string) { q.r.Warn(msg) }

func (q quietProgress) Diag(string) {}

// dryRunRefresh performs the live thread read into a scratch bundle, so the
// real threads.json keeps its bytes while the preview sees the current feed.
func dryRunRefresh(ctx context.Context, r *cli.Runner, req Request, fl *flags, dir string,
) (*fetchcore.ThreadsResponse, *threadscore.File, *core.RefusalError) {
	scratchRoot, scratchDir, ref := prepareDryRunBundle(dir, req.Pr)
	if ref != nil {
		return nil, nil, ref
	}
	defer os.RemoveAll(scratchRoot)
	read := req
	read.RepoRoot = scratchRoot
	rt := refreshRequest(r, read, fl)
	rt.Progress = quietProgress{r: r}
	resp, ref := fetchcore.RunThreads(ctx, rt)
	if ref != nil {
		return nil, nil, drainFailed(ref.Message)
	}
	feed, err := threadscore.ReadFile(filepath.Join(scratchDir, threadscore.KeyThreadsJSON))
	if err != nil {
		return nil, nil, drainFailed(fmt.Sprintf("unreadable dry-run feed: %v", err))
	}
	return resp, feed, nil
}

func prepareDryRunBundle(dir, pr string) (string, string, *core.RefusalError) {
	scratchRoot, err := os.MkdirTemp("", "verdict-drain-dryrun-")
	if err != nil {
		return "", "", drainFailed(fmt.Sprintf("could not prepare dry-run read: %v", err))
	}
	scratchDir, ref := readcommon.ResolveBundleDir(scratchRoot, pr)
	if ref != nil {
		return "", "", drainFailed(ref.Message)
	}
	if err := os.MkdirAll(scratchDir, scratchDirMode); err != nil {
		return "", "", drainFailed(fmt.Sprintf("could not prepare dry-run read: %v", err))
	}
	for _, name := range []string{"PR.json", threadscore.KeyThreadsJSON} {
		if err := copyBundleFile(filepath.Join(dir, name), filepath.Join(scratchDir, name)); err != nil {
			return "", "", drainFailed(err.Error())
		}
	}
	return scratchRoot, scratchDir, nil
}

func copyBundleFile(src, dst string) error {
	raw, err := jq.CappedFile(src, jq.MaxPRBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return jq.NewErrf("unreadable %s: %v", src, err)
	}
	if err := os.WriteFile(dst, raw, scratchFileMode); err != nil {
		return jq.NewErrf("could not prepare dry-run read: %v", err)
	}
	return nil
}

func dryRunInputs(ctx context.Context, req Request, resp *fetchcore.ThreadsResponse,
	dir string, feed *threadscore.File,
) (*planInputs, *core.RefusalError) {
	return inputsFrom(ctx, req, resp, dir, feed)
}

func dryRunNext(req Request, fl *flags) string {
	parts := []string{"verdict", verbDrain, req.Repo, req.Pr}
	passed := false
	for _, a := range fl.args {
		if a == "--" {
			passed = true
		}
		if !passed && a == "--dry-run" {
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func postSuccessNext(req Request) string {
	return "refresh threads with verdict threads " + req.Repo + " " + req.Pr
}

func postDryRunFinish(r *cli.Runner, req Request, dir string, plan *draincore.File, fl *flags,
	result *draincore.PostResult,
) int {
	preview := &draincore.DryRunFile{
		Provider: plan.Provider, PR: plan.PR, HeadSHA: plan.HeadSHA,
		PlannedAt: cli.NowUTC(r.Now()), Planned: result.Planned, Skipped: plan.Skipped,
	}
	if err := draincore.WriteDryRun(filepath.Join(dir, draincore.KeyDryRunJSON), preview); err != nil {
		return r.Refuse(drainFailed(err.Error()))
	}
	fmt.Fprint(r.Human, core.Msgf("» %d planned, %d skipped (dry run; nothing posted) -> %s/%s\n",
		len(preview.Planned), len(preview.Skipped), dir, draincore.KeyDryRunJSON))
	r.Emit(verbDrain, req.Repo, req.Pr, cli.NowUTC(r.Now()), dryRunNext(req, fl),
		r.Warnings, preview.Wire())
	return cli.ExitOK
}

func postFinish(r *cli.Runner, req Request, dir string, plan *draincore.File,
	result *draincore.PostResult,
) int {
	nextText := postSuccessNext(req)
	if result.Incomplete {
		nextText = "re-run verdict drain " + req.Repo + " " + req.Pr + " --post"
	}
	fmt.Fprint(r.Human, core.Msgf("» %d posted, %d failed -> %s/%s\n",
		len(result.Posted), len(plan.Failed), dir, draincore.KeyPlanJSON))
	if result.Incomplete {
		ref := &core.RefusalError{Code: "drain-incomplete", Message: core.Msgf(
			"%d thread(s) could not be posted; the ledger makes a re-run idempotent", len(plan.Failed))}
		if !r.JSONMode {
			return r.Refuse(ref)
		}
		env := core.RenderEnvelope(verbDrain, req.Repo, req.Pr, cli.NowUTC(r.Now()), nextText,
			r.Warnings, plan.Wire(), &core.SchemaError{
				Code:    ref.Code,
				Message: ref.Message + "\n" + cli.HintFor(ref.Code),
			})
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		r.WriteRentry()
		return cli.ExitFor(ref, cli.ExitRefused)
	}
	r.Emit(verbDrain, req.Repo, req.Pr, cli.NowUTC(r.Now()), nextText,
		r.Warnings, plan.Wire())
	return cli.ExitOK
}

func commander(req Request) core.Commander {
	if req.Commander != nil {
		return req.Commander
	}
	return coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
}
