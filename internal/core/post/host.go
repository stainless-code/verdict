package postcore

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
)

// hostWriter is the write half of one code host: the tool the submit path
// spawns and the call that lands one payload.
type hostWriter interface {
	tool() string
	post(ctx context.Context, cmdr Commander, env map[string]string,
		caps core.HostCapabilities, repo, pr, dir, payload string) postAttempt
}

// hostReader is the read half: the live head the drift check compares, the
// authenticated login the authorship gate compares, the open-thread recheck
// the approval guard counts, and the local gate that runs before any call.
type hostReader interface {
	liveSHA(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (string, error)
	selfLogin(ctx context.Context, cmdr Commander, env map[string]string) (string, error)
	recheck(ctx context.Context, cmdr Commander, env map[string]string, repo, pr, dir string) (int, string, error)
	auth(env map[string]string) *core.RefusalError
}

// hostFor is the post path's only provider switch: every host behaviour a verb
// reaches goes through the pair it returns. A provider outside the switch gets
// GitHub's pair, because checkBundle refuses a host it cannot post to before
// any call is built.
func hostFor(provider string) (hostWriter, hostReader) {
	switch provider {
	case providerGitLab:
		return gitlabHost{}, gitlabHost{}
	default:
		return githubHost{}, githubHost{}
	}
}
