package postcore

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// gitlabHost is the `glab` host: one discussion per finding, a summary note,
// and the approval last. glab carries the token, so the host needs no local
// prerequisite gate.
type gitlabHost struct{}

func (gitlabHost) tool() string { return toolGlab }

func (gitlabHost) auth(map[string]string) *core.RefusalError { return nil }

// post replays one payload onto the merge request.
func (gitlabHost) post(ctx context.Context, cmdr Commander, _ map[string]string,
	caps core.HostCapabilities, repo, pr, dir, payload string,
) postAttempt {
	return gitlabPost(ctx, cmdr, caps, repo, pr, dir, payload)
}

func (gitlabHost) liveSHA(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr string) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	mr, _, err := gitlabMRRead(ctx, cmdr, repo, pr)
	if err != nil {
		return "", err
	}
	return mr.headSHA(), nil
}

func (gitlabHost) selfLogin(ctx context.Context, cmdr Commander, _ map[string]string) (string, error) {
	return gitlabSelfLogin(ctx, cmdr)
}

func (gitlabHost) recheck(ctx context.Context, cmdr Commander, _ map[string]string, repo, pr, dir string) (int, string, error) {
	return gitlabRecheck(ctx, cmdr, repo, pr, dir)
}

// resolveThread closes one discussion with GitLab's resolved flag and holds
// the answer to the state the call asked for.
func (gitlabHost) resolveThread(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, threadID string,
) error {
	res, err := cmdr.Run(ctx, &core.Command{Argv: gitlabResolveArgv(repo, pr, threadID)})
	if err != nil {
		return jq.Sealed("gitlab resolve thread", err)
	}
	if res.ExitCode != 0 {
		return &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"gitlab did not resolve discussion %s: %s", threadID, firstReason(res.Stderr))}
	}
	if !gitlabDiscussionResolved(res.Stdout) {
		return &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"gitlab did not confirm discussion %s resolved", threadID)}
	}
	return nil
}

// gitlabResolveArgv is the one REST call that closes a discussion.
func gitlabResolveArgv(repo, pr, threadID string) []string {
	return []string{
		toolGlab, cmdAPI,
		gitlabEndpoint(repo, pr, gitlabDiscussionsPath+"/"+url.PathEscape(threadID)+"?resolved=true"),
		flagMethod, methodPut,
	}
}

// gitlabDiscussionResolved reads the discussion's first non-system note: the
// resolved pair lives on the root note, and a plain note is not resolvable.
func gitlabDiscussionResolved(stdout string) bool {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return false
	}
	for _, note := range jq.Elements(m["notes"]) {
		nm, ok := note.(map[string]any)
		if !ok || !jq.Falsey(nm["system"]) {
			continue
		}
		return nm["resolved"] == true
	}
	return false
}

const gitlabNotesPath = "/notes"

func (gitlabHost) listSummaryComments(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr string,
) ([]summaryRemoteComment, error) {
	items, err := readPublicationList(ctx, cmdr, append(
		gitlabReadArgv(repo, pr, gitlabNotesPath+gitlabPageQuery), flagPaginate,
	))
	if err != nil {
		return nil, err
	}
	return summaryCommentsFromMaps(items, ""), nil
}

func (gitlabHost) createSummaryComment(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, body string,
) (summaryRemoteComment, error) {
	return gitlabSummaryWrite(ctx, cmdr, repo, pr, methodPost, gitlabNotesPath, body)
}

func (gitlabHost) updateSummaryComment(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, commentID, body string,
) (summaryRemoteComment, error) {
	return gitlabSummaryWrite(ctx, cmdr, repo, pr, methodPut, gitlabNotesPath+"/"+commentID, body)
}

func gitlabSummaryWrite(ctx context.Context, cmdr Commander, repo, pr, method, suffix, body string) (summaryRemoteComment, error) {
	if strings.TrimSpace(body) == "" {
		return summaryRemoteComment{}, shapeError(errEmptySummary)
	}
	if cmdr == nil {
		return summaryRemoteComment{}, errNoCommander
	}
	res, err := gitlabCall(ctx, cmdr, gitlabMethodWriteArgv(repo, pr, method, suffix), jq.NewObj(fieldBody, body).MarshalPretty())
	if err != nil {
		return summaryRemoteComment{}, err
	}
	if res.ExitCode != 0 {
		return summaryRemoteComment{}, shapeError(failureReason(res, nil))
	}
	return decodeSummaryCommentObject(res.Stdout, "")
}

func (gitlabHost) postedInlineURLs(context.Context, Commander, map[string]string,
	string, string, *jq.Obj,
) ([]summaryInlineLink, error) {
	// GitLab inline discussion URLs come directly from POST /discussions responses
	// and ride on postAttempt.inlineLinks.
	return nil, nil
}
