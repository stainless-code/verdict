// Package panel is the CLI adapter for the local verification planner.
package panel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run writes only verification/plan.json and verification/dispatch.md.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	if len(args) > 0 {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict panel <owner/repo> <pr> [--json]"})
	}
	if code, done := r.Canceled(ctx, "panel"); done {
		return code
	}
	dir, ref := readcommon.ResolveWritableBundleDir(req.RepoRoot, req.Pr)
	if ref != nil {
		return r.Refuse(ref)
	}
	review, err := recordcore.LoadReview(filepath.Join(dir, "review.json"))
	if err != nil {
		return refuse(r, err)
	}
	roster, err := os.ReadFile(filepath.Join(dir, "panel"))
	if err != nil {
		return r.Refuse(&core.RefusalError{Code: panelcore.CodeEmpty, Message: "missing pinned bundle panel roster; configure defaults.panel and re-fetch"})
	}
	prompt, err := os.ReadFile(filepath.Join(req.RootPath(), "skills/verdict/prompts/verifier.md"))
	if err != nil {
		return r.Refuse(&core.RefusalError{Code: "report-failed", Message: fmt.Sprintf("could not read verifier prompt under %s: %v", req.RootPath(), err)})
	}
	plan, dispatch, warnings, err := panelcore.Plan(dir, readcommon.OpenBundle(dir).Head(), req.TimeUTC(), string(roster), string(prompt), review)
	if err != nil {
		return refuse(r, err)
	}
	file, moreWarnings, err := panelcore.Load(dir, readcommon.OpenBundle(dir).Head(), panelcore.Findings(review))
	if err != nil {
		return refuse(r, err)
	}
	warnings = append(warnings, moreWarnings...)
	dispatch, second := panelcore.SecondLook(plan, dispatch, file)
	output := filepath.Join(dir, "verification")
	if err = os.MkdirAll(output, 0o755); err == nil {
		err = atomic.WriteFile(filepath.Join(output, "plan.json"), []byte(jq.PrettyOrdered(plan)))
	}
	if err == nil {
		err = atomic.WriteFile(filepath.Join(output, "dispatch.md"), []byte(dispatch))
	}
	if err != nil {
		return r.Refuse(&core.RefusalError{Code: "report-failed", Message: fmt.Sprintf("could not write panel artifacts: %v", err)})
	}
	data := jq.NewObj("dir", dir, "plan", dir+"/verification/plan.json", "dispatch", dir+"/verification/dispatch.md")
	totals, _ := plan.Get("totals")
	tm := totals.(*jq.Obj).Fields
	for _, key := range []string{"verifiers", "findings", "dispatches"} {
		data.Set(key, tm[key])
	}
	data.Set("second_look", second)
	r.Warnings = append(r.Warnings, warnings...)
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("panel", req.Repo, req.Pr, req.TimeUTC(), "dispatch the plan; write verification/verdicts.json; then record or post", r.Warnings, data, nil)))
	} else {
		fmt.Fprintf(r.Human, "» panel: %v verifiers · %v findings · %v dispatches -> %s/verification/dispatch.md\n", tm["verifiers"], tm["findings"], tm["dispatches"], dir)
	}
	return 0
}
func refuse(r *cli.Runner, err error) int {
	var ref *core.RefusalError
	if errors.As(err, &ref) {
		return r.Refuse(ref)
	}
	return r.Refuse(&core.RefusalError{Code: "report-failed", Message: err.Error()})
}
