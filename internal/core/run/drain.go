package run

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
)

// Drain launches one drafting agent through the same launcher run uses, then
// asserts a fresh drain.replies.json. The digest is passed from the plan so the
// directive and the artifact check name the same draft. There is no repair
// loop and no host write; the caller validates the artifact's contents.
func Drain(ctx context.Context, req *core.RunRequest, digest string) (*core.RunResponse, *core.RefusalError) {
	st := &state{
		req: req, warnings: append([]string{}, req.Warnings...), drainDigest: digest,
	}
	return execute(ctx, st)
}
