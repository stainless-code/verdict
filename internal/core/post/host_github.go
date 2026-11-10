package postcore

import (
	"context"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

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
