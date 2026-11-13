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

const (
	codeFailed = "report-failed"
	verb       = "panel"
	outputMode = 0o755
	panelNext  = "dispatch the plan; write verification/verdicts.json; then record or post"
)

type planned struct {
	dir, dispatch string
	plan          *jq.Obj
	warnings      []string
	second        int
}

// Run writes only verification/plan.json and verification/dispatch.md.
func Run(ctx context.Context, r *cli.Runner, args []string, req readcommon.Request) int {
	if len(args) > 0 {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict panel <owner/repo> <pr> [--json]"})
	}
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	output, err := prepare(req)
	if err != nil {
		return refuse(r, err)
	}
	if err := writePlan(output); err != nil {
		return refuse(r, err)
	}
	for _, warning := range output.warnings {
		r.Warn(warning)
	}
	render(r, req, output)
	return 0
}

func prepare(req readcommon.Request) (*planned, error) {
	dir, ref := readcommon.ResolveWritableBundleDir(req.RepoRoot, req.Pr)
	if ref != nil {
		return nil, panelError(ref)
	}
	review, err := recordcore.LoadReview(filepath.Join(dir, "review.json"))
	if err != nil {
		return nil, panelError(err)
	}
	roster, err := os.ReadFile(filepath.Join(dir, verb))
	if err != nil {
		return nil, &core.RefusalError{
			Code:    panelcore.CodeEmpty,
			Message: "missing pinned bundle panel roster; configure defaults.panel and re-fetch",
		}
	}
	prompt, err := readPrompt(req.RootPath())
	if err != nil {
		return nil, panelError(err)
	}
	head := readcommon.OpenBundle(dir).Head()
	plan, dispatch, warnings, err := panelcore.Plan(dir, head, req.TimeUTC(), string(roster), string(prompt), review)
	if err != nil {
		return nil, panelError(err)
	}
	return completePlan(dir, head, plan, dispatch, warnings, review)
}

func completePlan(dir, head string, plan *jq.Obj, dispatch string, warnings []string, review *jq.Obj) (*planned, error) {
	file, more, err := panelcore.Load(dir, head, panelcore.Findings(review))
	if err != nil {
		return nil, panelError(err)
	}
	file, err = withStoredRefutations(dir, review, file)
	if err != nil {
		return nil, panelError(err)
	}
	dispatch, second := panelcore.SecondLook(plan, dispatch, file)
	return &planned{dir: dir, plan: plan, dispatch: dispatch, warnings: append(warnings, more...), second: second}, nil
}

func readPrompt(root string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(root, "prompts/verifier.md"))
	if err == nil {
		return raw, nil
	}
	raw, err = os.ReadFile(filepath.Join(root, "skills/verdict/prompts/verifier.md"))
	if err != nil {
		return nil, &core.RefusalError{Code: codeFailed, Message: fmt.Sprintf("could not read verifier prompt under %s: %v", root, err)}
	}
	return raw, nil
}

func writePlan(output *planned) error {
	dir := filepath.Join(output.dir, "verification")
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return &core.RefusalError{Code: codeFailed, Message: "verification/ must not be a symlink"}
	}
	if err := os.MkdirAll(dir, outputMode); err != nil {
		return jq.Sealed("create verification directory", err)
	}
	if err := atomic.WriteFile(filepath.Join(dir, "plan.json"), []byte(jq.PrettyOrdered(output.plan))); err != nil {
		return jq.Sealed("write verification/plan.json", err)
	}
	if err := atomic.WriteFile(filepath.Join(dir, "dispatch.md"), []byte(output.dispatch)); err != nil {
		return jq.Sealed("write verification/dispatch.md", err)
	}
	return nil
}

func render(r *cli.Runner, req readcommon.Request, output *planned) {
	data := jq.NewObj("dir", output.dir, "plan", output.dir+"/verification/plan.json",
		"dispatch", output.dir+"/verification/dispatch.md")
	raw, _ := output.plan.Get("totals")
	totals, ok := raw.(*jq.Obj)
	if !ok {
		return
	}
	for _, key := range []string{"verifiers", "findings", "dispatches"} {
		data.Set(key, totals.Fields[key])
	}
	data.Set("second_look", output.second)
	if r.JSONMode {
		env := core.RenderEnvelope(verb, req.Repo, req.Pr, req.TimeUTC(), panelNext, r.Warnings, data, nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return
	}
	const line = "» panel: %v verifiers · %v findings · %v dispatches -> %s/verification/dispatch.md\n"
	fmt.Fprintf(r.Human, line, totals.Fields["verifiers"], totals.Fields["findings"], totals.Fields["dispatches"], output.dir)
}

func refuse(r *cli.Runner, err error) int {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return r.Refuse(ref)
	}
	return r.Refuse(&core.RefusalError{Code: codeFailed, Message: err.Error()})
}

func panelError(err error) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return jq.Sealed(verb, err)
}

func withStoredRefutations(dir string, review *jq.Obj, file *panelcore.File) (*panelcore.File, error) {
	if _, err := os.Stat(filepath.Join(dir, "findings.json")); errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return nil, panelError(err)
	}
	return panelcore.WithStored(file, entries, panelcore.Findings(review)), nil
}
