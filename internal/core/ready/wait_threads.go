package readycore

import (
	"context"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const waitPageLimit = 100

func waitThreads(ctx context.Context, req *core.ReadRequest) ([]any, *core.RefusalError) {
	owner, name := readcommon.SplitOwner(req.Repo)
	cursor := ""
	seen := map[string]bool{}
	nodes := []any{}
	for range waitPageLimit {
		page, next, more, ref := waitThreadPage(ctx, req, readcommon.ReviewThreadsArgv(owner, name, req.Pr, cursor))
		if ref != nil {
			return nil, ref
		}

		nodes = append(nodes, page...)
		if !more {
			return nodes, nil
		}
		if !waitCursorValid(next, seen) {
			return nil, waitReadFailed()
		}
		seen[next] = true
		cursor = next
	}
	return nil, waitReadFailed()
}

func waitThreadPage(ctx context.Context, req *core.ReadRequest, argv []string) ([]any, string, bool, *core.RefusalError) {
	raw, _, ok := coreexec.Gh(ctx, req.Commander, argv...)
	if !ok {
		return nil, "", false, waitReadFailed()
	}
	page, next, more, err := readcommon.ThreadsPage(raw)
	if err != nil || !waitCommentsComplete(page) {
		return nil, "", false, waitReadFailed()
	}
	return page, next, more, nil
}

func waitCursorValid(next string, seen map[string]bool) bool { return next != "" && !seen[next] }

func waitCommentsComplete(nodes []any) bool {
	for _, node := range nodes {
		if !waitThreadComplete(node) {
			return false
		}
	}
	return true
}

func waitThreadComplete(node any) bool {
	thread, ok := node.(map[string]any)
	if !ok {
		return false
	}
	comments, ok := thread["comments"].(map[string]any)
	if !ok {
		return false
	}
	entries, ok := comments["nodes"].([]any)
	return ok && jq.R(comments["totalCount"]) == strconv.Itoa(len(entries))
}
