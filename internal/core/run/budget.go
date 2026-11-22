package run

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/budget"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/overrides"
)

// overriddenByFinders is the published overridden_by value naming --finders as
// the layer that set the finder count. It is not the resume record's finder
// key: the two spellings are separate contracts.
const overriddenByFinders = "finders"

// budgetFinderSource labels the finder count a review budget decided, so
// finder_source never claims a flag or a captured layer supplied a count the
// classifier chose. The budget object beside it carries the flag/config detail.
const budgetFinderSource = "budget"

// requestedBudget is the review depth one invocation asked for, where it asked
// from, and whether it asked at all.
type requestedBudget struct {
	tier   string
	source string
	set    bool
}

// resolveBudget turns the captured bundle plus the requested depth into the
// recorded review-budget decision. It returns nil when neither --budget nor
// defaults.budget asked for one, so a run that names no depth keeps every
// artifact byte-identical.
func (st *state) resolveBudget(dir string, captured *config.RunState) (*core.ReviewBudget, *core.RefusalError) {
	request, ref := st.budgetRequest(captured)
	if ref != nil || !request.set {
		return nil, ref
	}
	in := budgetInputs(dir, captured)
	decision := budget.ForTier(request.tier)
	if request.tier == budget.RequestAuto {
		decision = budget.Classify(&in)
	}
	recorded := &core.ReviewBudget{
		Tier:       decision.Tier,
		Source:     request.source,
		Requested:  request.tier,
		Files:      in.FileCount,
		DiffBytes:  in.DiffBytes,
		Finders:    decision.Finders,
		CrossCheck: budget.CrossForCount(decision.Finders),
		Reasons:    decision.Reasons,
	}
	if st.req.FindersRaw != "" {
		recorded.OverriddenBy = overriddenByFinders
	}
	return recorded, nil
}

// recordBudgetCount pins the decision to the finder count the precedence chain
// actually resolved, so launcher.json and --json can never claim a dispatch the
// run did not launch. overridden_by is already set by resolveBudget, which
// knows that --finders outranks the depth it resolved.
func (st *state) recordBudgetCount(count int) {
	if st.budget == nil {
		return
	}
	st.budget.Finders, st.budget.CrossCheck = count, budget.CrossForCount(count)
}

// budgetRequest reads the requested depth: --budget outranks the captured
// defaults.budget, which fetch resolved from the repository configuration.
func (st *state) budgetRequest(captured *config.RunState) (requestedBudget, *core.RefusalError) {
	if st.req.BudgetRaw != "" {
		if !budget.ValidRequest(st.req.BudgetRaw) {
			return requestedBudget{}, st.refuse(codeBadFlags, "--budget must be "+budget.Vocabulary())
		}
		return requestedBudget{tier: st.req.BudgetRaw, source: "flag", set: true}, nil
	}
	if captured == nil || captured.Resolved == nil {
		return requestedBudget{}, nil
	}
	tier := captured.Resolved.Defaults.Values.Budget
	if tier == "" {
		return requestedBudget{}, nil
	}
	if !budget.ValidRequest(tier) {
		return requestedBudget{}, st.refuse(codeBadFlags,
			"captured budget must be "+budget.Vocabulary()+"; re-fetch the bundle")
	}
	return requestedBudget{tier: tier, source: "config", set: true}, nil
}

// budgetInputs reads what the classifier may see: the bundle's rows and gate
// measurements, the walkthrough thresholds its controls snapshot resolved, and
// the built-in sensitive globs extended by the captured configuration.
func budgetInputs(dir string, captured *config.RunState) budget.Inputs {
	bundle := diagram.LoadBundle(dir)
	files := make([]budget.File, 0, len(bundle.Files))
	for i := range bundle.Files {
		files = append(files, budget.File{
			Path:         bundle.Files[i].Path,
			PreviousPath: bundle.Files[i].PreviousPath,
			Role:         bundle.Files[i].Role,
		})
	}
	maxFiles, maxBytes := diagram.Thresholds(overrides.At(filepath.Join(dir, controls.DirName, "overrides")))
	globs := budget.BuiltinSensitive()
	if captured != nil && captured.Resolved != nil {
		globs = append(globs, captured.Resolved.Defaults.Values.SensitivePaths...)
	}
	return budget.Inputs{
		Files: files, FileCount: bundle.FileCount, DiffBytes: bundle.DiffBytes,
		MaxFiles: maxFiles, MaxBytes: maxBytes, Sensitive: globs,
	}
}

// budgetLine renders the one human summary line the decision earns. It carries
// the measurements and every reason, so an operator sees why a deep pass is
// about to cost two finders before it starts.
func budgetLine(recorded *core.ReviewBudget) string {
	line := fmt.Sprintf("review budget: %s (%d files, %d bytes)", recorded.Tier, recorded.Files, recorded.DiffBytes)
	if len(recorded.Reasons) == 0 {
		return line
	}
	return line + " — " + strings.Join(recorded.Reasons, ", ")
}
