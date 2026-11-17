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
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return r.Refuse(drainFailed(ref.Message))
	}
	plan, err := draincore.ReadPlan(dir + pathSep + draincore.KeyPlanJSON)
	if err != nil {
		return r.Refuse(drainFailed(err.Error()))
	}
	provider := plan.Provider
	if fl.provider != "" && fl.provider != provider {
		return r.Refuse(drainFailed(fmt.Sprintf("--provider %s disagrees with the plan's %s", fl.provider, provider)))
	}
	caps := core.CapabilitiesFor(provider)
	if !caps.ThreadReply {
		return r.Refuse(&core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread replies on %s are not implemented (%s)", provider, caps.ThreadReplyReason())})
	}
	var drafts []byte
	drafts, err = jq.CappedFile(dir+pathSep+draincore.KeyDraftsJSON, jq.MaxPRBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r.Refuse(drainFailed(fmt.Sprintf("unreadable %s/%s: %v", dir, draincore.KeyDraftsJSON, err)))
	}
	resp, ref := fetchcore.RunThreads(ctx, refreshRequest(r, req, fl))
	if ref != nil {
		return r.Refuse(drainFailed(ref.Message))
	}
	in, code, done := readInputs(ctx, r, req, fl, resp)
	if done {
		return code
	}
	cmdr := commander(req)
	at := req.TimeUTC()
	draftsReq := &draincore.PostRequest{
		Plan: plan, DraftsRaw: drafts, Feed: in.feed, Ledger: in.ledger,
		Provider: provider, Repo: req.Repo, PR: req.Pr, At: at,
		Reply: func(rootID, threadID, body string) (string, error) {
			id, rerr := postcore.ReplyThread(ctx, cmdr, provider, req.Env, req.Repo, req.Pr, rootID, threadID, body)
			if rerr != nil {
				return "", rerr
			}
			return id, nil
		},
		Resolve: func(threadID string) error {
			return postcore.ResolveThread(ctx, cmdr, provider, req.Env, req.Repo, req.Pr, threadID)
		},
		Append: func(entry draincore.LedgerEntry) error {
			return draincore.AppendLedger(dir+pathSep+draincore.KeyLedgerJSON, entry)
		},
		SaveFeed: func(feed *threadscore.File) error {
			return threadscore.WriteFile(dir+pathSep+threadscore.KeyThreadsJSON, feed)
		},
	}
	result, ref := draincore.Post(draftsReq)
	if ref != nil {
		return r.Refuse(ref)
	}
	if err := draincore.WritePlan(dir+pathSep+draincore.KeyPlanJSON, plan); err != nil {
		return r.Refuse(drainFailed(err.Error()))
	}
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
		return r.Refuse(&core.RefusalError{Code: draincore.CodeDrainIncomplete, Message: fmt.Sprintf(
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
