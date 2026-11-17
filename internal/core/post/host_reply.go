package postcore

import (
	"context"
	"fmt"
	"net/url"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func (githubHost) replyThread(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, rootID, _, body string,
) (string, error) {
	argv := []string{
		toolGH, cmdAPI, flagMethod, methodPost,
		ghReposPrefix + repo + "/pulls/" + pr + "/comments/" + rootID + "/replies",
		flagRawField, "body=" + body,
	}
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv})
	if err != nil {
		return "", jq.NewErrf("github reply thread: %v", err)
	}
	if res.ExitCode != 0 {
		return "", &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"github did not reply to comment %s: %s", rootID, firstReason(res.Stderr))}
	}
	return replyID(res.Stdout)
}

func (gitlabHost) replyThread(ctx context.Context, cmdr Commander, _ map[string]string,
	repo, pr, _, threadID, body string,
) (string, error) {
	endpoint := gitlabEndpoint(repo, pr,
		gitlabDiscussionsPath+"/"+url.PathEscape(threadID)+gitlabNotesPath)
	argv := []string{
		toolGlab, cmdAPI, endpoint, flagMethod, methodPost,
		flagHeader, gitlabContentType, flagInput, "-",
	}
	res, err := gitlabCall(ctx, cmdr, argv, jq.NewObj("body", body).MarshalPretty())
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"gitlab did not reply to discussion %s: %s", threadID, firstReason(res.Stderr))}
	}
	return replyID(res.Stdout)
}

func replyID(stdout string) (string, error) {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return "", jq.NewErrf("reply response unreadable: %v", err)
	}
	id, ok := navField(doc, "id")
	if !ok || jq.Falsey(id) {
		return "", jq.NewErr("reply response carried no id")
	}
	return jq.Str(id), nil
}
