package readycore

import (
	"context"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes readiness, optionally waiting for CI under one hard deadline.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if req.WaitFor == "" && req.Timeout == "" {
		return runNow(ctx, req)
	}
	if req.WaitFor != "build" {
		return nil, &core.RefusalError{Code: waitBadFlags, Message: "ready --wait-for must be build and requires --timeout"}
	}
	timeout, _, ref := WaitDurations(req.Timeout, "")
	if ref != nil {
		return nil, ref
	}
	if ref := readyWaitBundle(req); ref != nil {
		return nil, ref
	}
	return runBuildWait(ctx, req, timeout)
}

func runBuildWait(ctx context.Context, req *Request, timeout time.Duration) (*Response, *core.RefusalError) {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	waited, ref := Wait(bounded, &WaitRequest{
		ReadRequest: req.ReadRequest, Until: untilChecksDone,
		Timeout: req.Timeout, PollRuntime: req.PollRuntime,
	})
	if ctx.Err() != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "ready wait canceled"}
	}
	if bounded.Err() != nil {
		return readyWaitTimeout(req, nil), nil
	}
	if ref != nil {
		return nil, ref
	}
	if waited.Data.Outcome != "condition-met" {
		return readyWaitTimeout(req, waited.Data), nil
	}
	return readyAfterWait(bounded, ctx, req, waited.Data)
}

func readyAfterWait(ctx, parent context.Context, req *Request, wait *WaitData) (*Response, *core.RefusalError) {
	resp, ref := runNow(ctx, req)
	if parent.Err() != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "ready wait canceled"}
	}
	if readyExpired(ctx) {
		return readyWaitTimeout(req, wait), nil
	}
	if ref != nil {
		return nil, ref
	}
	resp.Data.Wait = wait
	return resp, nil
}

func readyWaitTimeout(req *Request, wait *WaitData) *Response {
	if wait == nil {
		wait = &WaitData{Until: untilChecksDone}
	}
	wait.Outcome = outcomeTimeout
	st := &state{req: req, repo: req.Repo, pr: req.Pr, rec: recWait, prState: stateOpen, warnings: append([]string{}, req.Warnings...)}
	st.blockers = []Blocker{{
		Kind: outcomeTimeout, Detail: "CI did not finish within " + req.Timeout,
		Action: "inspect CI and re-run ready with a bounded timeout",
	}}
	resp := st.finish()
	resp.Data.Wait = wait
	return resp
}

func readyWaitBundle(req *Request) *core.RefusalError {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return ref
	}
	if isLocalDir(dir) {
		return &core.RefusalError{Code: "no-pr-target", Message: "ready build waits require a PR bundle"}
	}
	if !isRegular(dir + "/PR.json") {
		return &core.RefusalError{Code: "missing-bundle", Message: "ready build waits require a fetched PR bundle"}
	}
	return nil
}

func readyExpired(ctx context.Context) bool { return ctx.Err() != nil }
