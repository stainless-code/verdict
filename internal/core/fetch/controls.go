package fetchcore

import (
	"context"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/statedir"
)

// resolveControls snapshots the review controls at the bundle's base commit
// (the worktree only under the explicit opt-out) before any reader runs. A
// budget armed only by the .review/budget file resolves here; the
// REVIEW_BUDGET_USD env layer already gated before any provider call.
//
//nolint:revive // prototype: the preamble reads as one ordered sequence
func resolveControls(
	ctx context.Context, ctx2 *fetchRunCtx, repo, baseSHA string, allowHead bool, budget *budgetState, consent bool,
) error {
	r := ctx2.runner
	snap, ref := controls.Resolve(ctx, r.commander, controls.Request{
		BundleDir: ctx2.dir, Repo: repo, BaseSHA: baseSHA, AllowHead: allowHead,
	})
	if ref != nil {
		return ref //nolint:wrapcheck // the typed refusal passes through unchanged
	}
	r.controls = snap
	for _, w := range snap.Warnings {
		r.Warn(w)
	}
	if allowHead {
		r.Warn("controls resolved from HEAD/worktree (explicit opt-out --allow-head-controls) — a PR can edit its judge")
	}
	if statedir.ControlsMode() == "local" {
		r.Warn("controls resolved from local state (" + statedir.Resolve().ControlsDir() + ") — prototype VERDICT_PROTO_CONTROLS=local")
	}
	if budget == nil || ctx2.env["REVIEW_BUDGET_USD"] != "" {
		return nil
	}
	resolved, err := resolveBudget(ctx2.env, r)
	if err != nil {
		return err
	}
	*budget = resolved
	if err := budgetGate(r, budget, consent); err != nil {
		return err
	}
	writeBudgetReceipt(r, budget, ctx2.dir, core.NowUTC(r.Now()))
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
