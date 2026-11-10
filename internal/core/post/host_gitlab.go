package postcore

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
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
