package postcore

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// queryResolveThread is GitHub's one resolve mutation: the thread id is a
// GraphQL variable, never interpolated into the document.
const queryResolveThread = `mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{id isResolved}}}`

// githubHost is the `gh` host: one atomic review write, and the GraphQL reads
// the drift, authorship, and recheck gates need. GitHub's own CLI carries the
// token, so the host needs no local prerequisite gate.
type githubHost struct{}

func (githubHost) tool() string { return toolGH }

func (githubHost) auth(map[string]string) *core.RefusalError { return nil }

// post submits one payload as GitHub's single atomic review.
func (githubHost) post(ctx context.Context, cmdr Commander, _ map[string]string,
	_ core.HostCapabilities, repo, pr, dir, payload string,
) postAttempt {
	res, err := cmdr.Run(ctx, &core.Command{
		Argv:  githubReviewArgv(repo, pr),
		Stdin: strings.NewReader(payload + newline),
	})
	if err != nil || res.ExitCode != 0 {
		return postFailure(dir, res.Stderr, false, nil)
	}
	return postAccepted(dir, strings.TrimRight(res.Stdout, newline), res.Stderr, nil)
}

func (githubHost) liveSHA(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr string) (string, error) {
	return ghLiveSHA(ctx, cmdr, repo, pr)
}

func (githubHost) selfLogin(ctx context.Context, cmdr Commander, _ map[string]string) (string, error) {
	return ghSelfLogin(ctx, cmdr)
}

func (githubHost) recheck(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr, dir string) (int, string, error) {
	return postRecheck(ctx, cmdr, repo, pr, dir)
}

// resolveThread closes one review thread with GitHub's GraphQL mutation and
// holds the answer to the state the call asked for.
func (githubHost) resolveThread(ctx context.Context, cmdr Commander, _ map[string]string,
	_, _, threadID string,
) error {
	res, err := cmdr.Run(ctx, &core.Command{Argv: githubResolveArgv(threadID)})
	if err != nil {
		return jq.Sealed("github resolve thread", err)
	}
	if res.ExitCode != 0 {
		return &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"github did not resolve thread %s: %s", threadID, firstReason(res.Stderr))}
	}
	if !githubResolved(res.Stdout) {
		return &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"github did not confirm thread %s resolved", threadID)}
	}
	return nil
}

// githubResolveArgv is the one GraphQL call that closes a review thread.
func githubResolveArgv(threadID string) []string {
	return []string{
		toolGH, cmdAPI, "graphql",
		flagRawField, "query=" + queryResolveThread,
		flagRawField, "threadId=" + threadID,
	}
}

// githubResolved reads the mutation's post-condition: the thread it answered
// with must be resolved.
func githubResolved(stdout string) bool {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return false
	}
	v, ok := navField(doc, "data")
	if !ok {
		return false
	}
	if v, ok = navField(v, "resolveReviewThread"); !ok {
		return false
	}
	if v, ok = navField(v, "thread"); !ok {
		return false
	}
	resolved, ok := navField(v, "isResolved")
	return ok && resolved == true
}
