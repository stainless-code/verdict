package fixcore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/stainless-code/verdict/internal/core"
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
	msgNoSelectedFinding = "fix --selected: %s names %d reacted sig(s) and none is " +
		"open inline in the store — re-fetch the PR for current reactions, or triage them"
)

// selection is the reaction-scoped scope one --selected run works from.
type selection struct {
	path string
	sigs []string
}

// readSelection loads the fetch-written selection beside the bundle. A
// present-but-unreadable file refuses corruption the way every other store
// does: starting from an empty scope would silently drop the operator's work.
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
	if !ok || len(sigs) == 0 {
		return nil, st.refuse("corrupt-store", fmt.Sprintf(msgCorruptSelection, path))
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
// findings at or above the floor, narrowed by the path globs and — for
// --selected — to the sigs a human reacted to.
func (st *state) batchSelector(dir, floor string) (*readcommon.Selector, *core.RefusalError) {
	sel := &readcommon.Selector{
		Status: readcommon.StatusOpen,
		Kind:   readcommon.KindInline,
		Floor:  floor,
		Paths:  st.req.Paths,
	}
	if !st.req.Selected {
		return sel, nil
	}
	picked, ref := st.readSelection(dir)
	if ref != nil {
		return nil, ref
	}
	st.selection = picked
	sel.Sigs = picked.sigs
	return sel, nil
}

// noSelected explains a selection that matched nothing instead of printing a
// batch of zero rows.
func (st *state) noSelected() *core.RefusalError {
	return st.refuse(codeNoSelectedFinding, fmt.Sprintf(
		msgNoSelectedFinding, st.selection.path, len(st.selection.sigs)))
}
