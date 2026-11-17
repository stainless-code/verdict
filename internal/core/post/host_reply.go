package postcore

import (
	"context"
	"fmt"
	"net/url"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// replyThread posts one drain reply on GitHub as a direct reply to the root
// comment. The body is one -f field, never part of a shell command.
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

// replyThread posts one drain reply as a GitLab discussion note. Like the
// other GitLab writes, the JSON body arrives on stdin with the content type
// pinned on argv.
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

// replyID reads the new note id from a host reply. Both hosts accept a number
// or a string; a reply the host did not identify cannot enter the ledger.
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
