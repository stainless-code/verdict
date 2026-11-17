package drain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// postMode runs §6's gates before a single host write: validate the drafts,
// check the capability, refresh the feed, then hand the write order to the
// pure post service. The plan and the feed it writes are the run's receipt.
func postMode(ctx context.Context, r *cli.Runner, req Request, fl *flags) int {
	dir, plan, ref := postPlan(req, fl)
	if ref != nil {
		return r.Refuse(ref)
	}
	drafts, ref := postDraftsFile(dir)
	if ref != nil {
		return r.Refuse(ref)
	}
	resp, ref := fetchcore.RunThreads(ctx, refreshRequest(r, req, fl))
	if ref != nil {
		return r.Refuse(drainFailed(ref.Message))
	}
	in, code, done := readInputs(ctx, r, req, fl, resp)
	if done {
		return code
	}
	result, ref := postWrites(ctx, req, dir, plan, drafts, in)
	if ref != nil {
		return r.Refuse(ref)
	}
	return postFinish(r, req, dir, plan, resp, result)
}

// postPlan reads the plan and checks the capability before any host read.
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
	caps := core.CapabilitiesFor(plan.Provider)
	if !caps.ThreadReply {
		return "", nil, &core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread replies on %s are not implemented (%s)", plan.Provider, caps.ThreadReplyReason())}
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
	drafts []byte, in *planInputs,
) (*draincore.PostResult, *core.RefusalError) {
	provider := plan.Provider
	cmdr := commander(req)
	draftsReq := &draincore.PostRequest{
		Plan: plan, DraftsRaw: drafts, Feed: in.feed, Ledger: in.ledger,
		Provider: provider, Repo: req.Repo, PR: req.Pr, At: req.TimeUTC(),
		Reply:    replyWriter(ctx, cmdr, req, provider),
		Resolve:  resolveWriter(ctx, cmdr, req, provider),
		Append:   appendLedgerWriter(dir),
		SaveFeed: saveFeedWriter(dir),
	}
	result, ref := draincore.Post(draftsReq)
	if ref != nil {
		return nil, ref
	}
	if err := draincore.WritePlan(dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return nil, drainFailed(err.Error())
	}
	return result, nil
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

func postFinish(r *cli.Runner, req Request, dir string, plan *draincore.File,
	resp *fetchcore.ThreadsResponse, result *draincore.PostResult,
) int {
	r.Warnings = append(r.Warnings, resp.Warnings...)
	nextText := "refresh threads with verdict threads " + req.Repo + " " + req.Pr
	if result.Incomplete {
		nextText = "re-run verdict drain " + req.Repo + " " + req.Pr + " --post"
	}
	fmt.Fprintf(r.Human, "» %d posted, %d failed -> %s/%s\n",
		len(result.Posted), len(result.Failed), dir, draincore.KeyPlanJSON)
	if r.JSONMode {
		env := core.RenderEnvelope(verbDrain, req.Repo, req.Pr, core.NowUTC(r.Now()), nextText,
			r.Warnings, plan.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	if result.Incomplete {
		return r.Refuse(&core.RefusalError{Code: "drain-incomplete", Message: fmt.Sprintf(
			"%d thread(s) could not be posted; the ledger makes a re-run idempotent", len(result.Failed))})
	}
	return cli.ExitOK
}

func commander(req Request) core.Commander {
	if req.Commander != nil {
		return req.Commander
	}
	return coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
}
