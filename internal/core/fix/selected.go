package fixcore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/recipe"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	codeMissingSelection  = "missing-selection"
	codeNoSelectedFinding = "no-selected-findings"

	msgMissingSelection = "fix --selected: no %s — re-run the bundle's fetch " +
		"(a GitHub fetch records the sigs a human reacted 👍 to)"
	msgCorruptSelection = "selection %s is unreadable or carries no sig list " +
		"(re-fetch the PR to rebuild it)"
	msgEmptySelection = "fix --selected: %s names no sig — re-fetch the bundle " +
		"(a GitHub fetch records the sigs a human reacted 👍 to), or triage them"
	msgNoSelectedFinding = "fix --selected: %s names %d reacted sig(s) and none is " +
		"open inline in the store — re-fetch the PR for current reactions, or triage them"
)

// selection is the reaction-scoped scope one --selected run works from.
type selection struct {
	path string
	sigs []string
}

// readSelection loads the fetch-written selection beside the bundle. An empty
// sig list is the one shape that must never reach the selector: Select reads an
// empty wanted set as "no filter", so the run would widen to every open inline
// finding instead of narrowing to none.
func (st *state) readSelection(dir string) (*selection, *core.RefusalError) {
	path := dir + suffixSelected
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, st.refuse(codeMissingSelection,
				fmt.Sprintf(msgMissingSelection, path))
		}
		return nil, st.refuse("corrupt-store", fmt.Sprintf(msgCorruptSelection, path))
	}
	sigs, ok := selectionSigs(raw)
	if !ok {
		return nil, st.refuse("corrupt-store", fmt.Sprintf(msgCorruptSelection, path))
	}
	if len(sigs) == 0 {
		return nil, st.refuse(codeNoSelectedFinding, fmt.Sprintf(msgEmptySelection, path))
	}
	return &selection{path: path, sigs: sigs}, nil
}

func selectionSigs(raw []byte) ([]string, bool) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return nil, false
	}
	list, _ := m.Get("sigs")
	return stringRows(list)
}

// stringRows reads a JSON array of non-empty strings, refusing anything else:
// a selection is either a list of sigs or a file this build cannot read.
func stringRows(list any) ([]string, bool) {
	rows, ok := list.([]any)
	if !ok {
		return nil, false
	}
	sigs := make([]string, 0, len(rows))
	for _, row := range rows {
		sig, ok := row.(string)
		if !ok || sig == "" {
			return nil, false
		}
		sigs = append(sigs, sig)
	}
	return sigs, true
}

// batchSelector builds the one selector a batch reads: the open inline
// findings at or above the floor, narrowed by the path globs, — for
// --selected — to the sigs a human reacted to, and — for --recipe — to the
// lens set the named recipe declares.
func (st *state) batchSelector(dir, floor string) (*readcommon.Selector, *core.RefusalError) {
	sel := &readcommon.Selector{
		Status: readcommon.StatusOpen,
		Kind:   readcommon.KindInline,
		Floor:  floor,
		Paths:  st.req.Paths,
	}
	if st.req.Selected {
		picked, ref := st.readSelection(dir)
		if ref != nil {
			return nil, ref
		}
		st.selection = picked
		sel.Sigs = picked.sigs
	}
	if st.req.Recipe != "" {
		if ref := st.applyRecipe(dir, sel); ref != nil {
			return nil, ref
		}
	}
	return sel, nil
}

// applyRecipe resolves --recipe from the bundle's base snapshot — never the
// live worktree — and narrows the selector to the recipe's lens set. A
// recipe that resolves to no lenses marks the batch empty instead of
// handing the selector an empty lens set, which would widen to every open
// inline finding rather than narrow to none.
func (st *state) applyRecipe(dir string, sel *readcommon.Selector) *core.RefusalError {
	plan, ref := recipe.Load(dir, st.req.Recipe)
	if ref != nil {
		return ref
	}
	for _, w := range plan.Warnings {
		st.warn(w)
	}
	st.recipeLenses = planLensIDs(plan)
	if len(st.recipeLenses) == 0 {
		st.recipeEmpty = true
		return nil
	}
	sel.Lenses = st.recipeLenses
	return nil
}

// planLensIDs reads the lens ids from the plan's lenses stage.
func planLensIDs(plan *recipe.Plan) []string {
	for _, stage := range plan.Stages {
		if stage.Kind != recipe.StageLenses {
			continue
		}
		ids := make([]string, 0, len(stage.Lenses))
		for _, lens := range stage.Lenses {
			ids = append(ids, lens.ID)
		}
		return ids
	}
	return nil
}

// noSelected explains a selection that matched nothing instead of printing a
// batch of zero rows.
func (st *state) noSelected() *core.RefusalError {
	return st.refuse(codeNoSelectedFinding, fmt.Sprintf(
		msgNoSelectedFinding, st.selection.path, len(st.selection.sigs)))
}
