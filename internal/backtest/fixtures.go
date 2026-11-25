package backtest

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
	qualitycore "github.com/stainless-code/verdict/internal/core/quality"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runFixtures executes `backtest fixtures`: a pure read of the shipped
// recordings against the hand-known defects in the fixture catalog. It never
// launches an agent and never calls a host.
func runFixtures(ctx context.Context, r *cli.Runner, req readcommon.Request, f *flags) int {
	resp, ref := qualitycore.RunFixtures(ctx, &qualitycore.Request{
		RepoRoot:       req.RepoRoot,
		FixtureID:      f.fixture,
		RecordingsRoot: f.recordings,
		Now:            r.Now,
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	fmt.Fprint(r.Human, qualitycore.HumanReport(resp.Data)+"\n")
	renderJSON(r, resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}
