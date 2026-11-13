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

const ghReposPrefix = "repos/"

func (githubHost) listSummaryComments(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr string,
) ([]summaryRemoteComment, error) {
	items, err := readPublicationList(ctx, cmdr, []string{
		toolGH, cmdAPI, ghReposPrefix + repo + "/issues/" + pr + "/comments", flagPaginate,
	})
	if err != nil {
		return nil, err
	}
	return summaryCommentsFromMaps(items, ""), nil
}

func (githubHost) createSummaryComment(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, body string,
) (summaryRemoteComment, error) {
	return ghSummaryWrite(ctx, cmdr, ghReposPrefix+repo+"/issues/"+pr+"/comments", methodPost, body)
}

func (githubHost) updateSummaryComment(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, _, commentID, body string,
) (summaryRemoteComment, error) {
	return ghSummaryWrite(ctx, cmdr, ghReposPrefix+repo+"/issues/comments/"+commentID, methodPatch, body)
}

func ghSummaryWrite(ctx context.Context, cmdr Commander, endpoint, method, body string) (summaryRemoteComment, error) {
	if strings.TrimSpace(body) == "" {
		return summaryRemoteComment{}, shapeError(errEmptySummary)
	}
	if cmdr == nil {
		return summaryRemoteComment{}, errNoCommander
	}
	payload := strings.TrimRight(jq.NewObj(fieldBody, body).MarshalPretty(), newline) + newline
	res, err := cmdr.Run(ctx, &core.Command{
		Argv:  []string{toolGH, cmdAPI, endpoint, flagMethod, method, flagInput, "-"},
		Stdin: strings.NewReader(payload),
	})
	if err != nil {
		return summaryRemoteComment{}, jq.Sealed("github summary write", err)
	}
	if res.ExitCode != 0 {
		return summaryRemoteComment{}, shapeError(failureReason(res, nil))
	}
	return decodeSummaryCommentObject(res.Stdout, "")
}

func (githubHost) postedInlineURLs(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr string, review *jq.Obj,
) ([]summaryInlineLink, error) {
	id := summaryReviewID(review)
	if id == "" || cmdr == nil {
		return nil, nil
	}
	items, err := readPublicationList(ctx, cmdr, []string{
		toolGH, cmdAPI, ghReposPrefix + repo + "/pulls/" + pr + "/reviews/" + id + "/comments", flagPaginate,
	})
	if err != nil {
		return nil, err
	}
	return inlineLinksFromMaps(items), nil
}
