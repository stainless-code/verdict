package fetchcore

import (
	"context"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/jq"
)

// resolveControls snapshots the review controls at the bundle's base commit
// (the worktree only under the explicit opt-out) before any reader runs.
// Spend gates before and after the snapshot through the effective
// resolution, never through legacy knob files; the REVIEW_BUDGET_USD env
// layer already gated before any provider call.
func resolveControls(
	ctx context.Context, ctx2 *fetchRunCtx, repo, baseSHA string, allowHead bool,
) error {
	r := ctx2.runner
	snap, ref := controls.Resolve(ctx, r.commander, &controls.Request{
		BundleDir: ctx2.dir, Repo: repo, BaseSHA: baseSHA, AllowHead: allowHead,
		Env: ctx2.env,
	})
	if ref != nil {
		return ref //nolint:wrapcheck // the typed refusal passes through unchanged
	}
	r.controls = snap
	if allowHead {
		r.Warn("controls resolved from HEAD/worktree (explicit opt-out --allow-head-controls) — a PR can edit its judge")
	}
	return nil
}

// readControl reads one control from the resolved snapshot. A nil snapshot
// reads absent (fail closed); no fetch reader ever touches the worktree.
func (s *session) readControl(rel string) ([]byte, bool) { return s.controls.ReadFile(rel) }

func controlsWire(ctx2 *fetchRunCtx) *jq.Obj {
	snap := ctx2.runner.controls
	if snap == nil {
		return nil
	}
	return jq.NewObj("policy", snap.File.Policy, "sha", snap.File.SHA, "files", snap.Used())
}
