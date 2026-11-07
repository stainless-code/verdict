package recordcore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Refusal codes for the cost-name repair. The family is closed: every code
// here is emitted below and registered in core's refusal set.
const (
	codeCostModelsNotArray   = "cost-models-not-array"
	codeCostPanelUnresolved  = "cost-panel-unresolved"
	codeCostEntryContradict  = "cost-entry-contradicts-panel"
	codeCostEntryUnnamed     = "cost-entry-unnamed"
	codeCostRecordUnreadable = "cost-unreadable"
)

// costNamePlan is one planned rewrite: the decoded document it came from,
// the file it belongs to, and how many entries gain a name. Planning every
// file before writing any keeps the pass all-or-nothing, so a refusal never
// leaves half a bundle named.
type costNamePlan struct {
	doc   any
	path  string
	gains int
}

// NameCostModels names the models[] entries of every cost record under dir
// from panel, rewriting each file in place. Panel order is synthesizer
// first, then lenses, so a record's entry i takes panel[i].
//
// An entry that already names a model keeps that name when the panel agrees
// with it and refuses when it does not: a repair must never silently
// reattribute spend. Entries the panel cannot explain, an object-shaped
// models field, and an undecodable record all refuse with nothing written,
// so the operator supplies the right panel or fixes the shape and re-runs.
//
// dryRun plans and validates the same pass without touching a byte, so
// `record --dry-run --cost-names` proves the repair before it lands.
//
// The pass is idempotent: a second run changes no byte and reports zero
// names gained.
func NameCostModels(dir string, panel []string, dryRun bool) (int, *core.RefusalError) {
	plans, ref := planCostNames(dir, panel)
	if ref != nil {
		return 0, ref
	}
	return writePlans(plans, dryRun)
}

// writePlans performs the planned rewrites, or counts them on a dry run.
func writePlans(plans []costNamePlan, dryRun bool) (int, *core.RefusalError) {
	renamed := 0
	for _, plan := range plans {
		n, ref := applyPlan(plan, dryRun)
		if ref != nil {
			return renamed, ref
		}
		renamed += n
	}
	return renamed, nil
}

// applyPlan writes one planned rewrite, or counts it on a dry run, reporting
// how many entries the file gains.
func applyPlan(plan costNamePlan, dryRun bool) (int, *core.RefusalError) {
	if plan.gains == 0 || dryRun {
		return plan.gains, nil
	}
	if ref := writePlan(plan); ref != nil {
		return 0, ref
	}
	return plan.gains, nil
}

// writePlan rewrites one cost file. A failure here is mid-pass, so the
// message says what is already on disk: the pass is idempotent, so re-running
// finishes the repair once the file is writable.
func writePlan(plan costNamePlan) *core.RefusalError {
	if err := atomic.WriteFile(plan.path, []byte(jq.PrettyOrdered(plan.doc))); err != nil {
		return &core.RefusalError{
			Code: codeCostRecordUnreadable,
			Message: "could not write " + plan.path + ": " + err.Error() +
				"; files written earlier in this pass are kept — re-run once the file is writable",
		}
	}
	return nil
}

// planCostNames decodes every cost record and records the rewrite each one
// needs, refusing on the first that cannot be named correctly.
func planCostNames(dir string, panel []string) ([]costNamePlan, *core.RefusalError) {
	files := readcommon.CostFiles(dir)
	plans := make([]costNamePlan, 0, len(files))
	for _, path := range files {
		plan, planned, ref := planCostFile(path, panel)
		if ref != nil {
			return nil, ref
		}
		if planned {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

// planCostFile plans one cost file, reporting false when it has no record.
func planCostFile(path string, panel []string) (costNamePlan, bool, *core.RefusalError) {
	doc, ref := decodeCostFile(path)
	if ref != nil {
		return costNamePlan{}, false, ref
	}
	if doc == nil {
		return costNamePlan{}, false, nil
	}
	gains, ref := nameCostDocument(path, doc, panel)
	if ref != nil {
		return costNamePlan{}, false, ref
	}
	return costNamePlan{doc: doc, path: path, gains: gains}, true, nil
}

// decodeCostFile reads and decodes one cost record, returning a nil document
// when the file is not there at all.
func decodeCostFile(path string) (any, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, &core.RefusalError{
			Code:    codeCostRecordUnreadable,
			Message: "could not read " + path + ": " + err.Error(),
		}
	}
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return nil, &core.RefusalError{
			Code:    codeCostRecordUnreadable,
			Message: "could not decode " + path + ": " + derr.Error(),
		}
	}
	return doc, nil
}

// nameCostDocument names the records of one cost file, which holds either a
// single record object or an array of them. A file's entries, read in
// document order, are the run's panel order: the synthesizer first, then
// each lens. Naming happens only when that order is forced — the entries
// that need a name must be exactly the panel, in order — because a partial
// list cannot say which seat an entry occupies, and a guess would
// misattribute real spend.
func nameCostDocument(path string, doc any, panel []string) (int, *core.RefusalError) {
	entries, ref := costEntries(path, costRecords(doc))
	if ref != nil {
		return 0, ref
	}
	if len(entries) == 0 {
		return 0, nil
	}
	if countUnnamed(entries) == 0 {
		return 0, checkExistingNames(path, entries, panel)
	}
	if ref := checkSeats(path, entries, panel); ref != nil {
		return 0, ref
	}
	return applyNames(path, entries, panel)
}

// countUnnamed counts the entries that still need a name from the panel.
func countUnnamed(entries []*jq.Obj) int {
	unnamed := 0
	for _, entry := range entries {
		if costName(entry) == "" {
			unnamed++
		}
	}
	return unnamed
}

// checkSeats refuses before naming when the panel cannot supply exactly one
// seat per entry.
func checkSeats(path string, entries []*jq.Obj, panel []string) *core.RefusalError {
	if len(panel) == 0 {
		return &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: filepath.Base(path) + ": no panel to name the cost entries from",
		}
	}
	if len(entries) != len(panel) {
		return &core.RefusalError{
			Code: codeCostEntryUnnamed,
			Message: fmt.Sprintf("%s: %d models[] entries but %d panel names — "+
				"pass the panel that ran for this record, in order, so every entry has exactly one name",
				filepath.Base(path), len(entries), len(panel)),
		}
	}
	return nil
}

// applyNames writes the panel's name onto each entry that has none.
func applyNames(path string, entries []*jq.Obj, panel []string) (int, *core.RefusalError) {
	gained := 0
	for i, entry := range entries {
		switch name := costName(entry); name {
		case panel[i]:
			continue
		case "":
			entry.Set("name", panel[i])
			gained++
		default:
			return 0, contradictRefusal(path, i, name, panel[i])
		}
	}
	return gained, nil
}

// checkExistingNames refuses a document whose names repeat or come from
// outside the panel: those entries claim seats the panel does not have, so
// nothing is renamed and the operator fixes the record by hand.
func checkExistingNames(path string, entries []*jq.Obj, panel []string) *core.RefusalError {
	seat := panelSeats(panel)
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		if ref := claimSeat(path, i, costName(entry), seat, seen); ref != nil {
			return ref
		}
	}
	return nil
}

// panelSeats indexes the panel for membership checks.
func panelSeats(panel []string) map[string]bool {
	seat := make(map[string]bool, len(panel))
	for _, name := range panel {
		seat[name] = true
	}
	return seat
}

// claimSeat records one entry's name, refusing a repeat or a name the panel
// does not list.
func claimSeat(path string, index int, name string, seat, seen map[string]bool) *core.RefusalError {
	if seen[name] {
		return contradictRefusal(path, index, name, "a name no other entry repeats")
	}
	seen[name] = true
	if len(seat) > 0 && !seat[name] {
		return contradictRefusal(path, index, name, "a name the panel lists")
	}
	return nil
}

// costRecords collects the record objects of one decoded cost file.
func costRecords(doc any) []*jq.Obj {
	switch d := doc.(type) {
	case *jq.Obj:
		return []*jq.Obj{d}
	case []any:
		return objectItems(d)
	}
	return nil
}

// costEntries lists every models[] entry of every record, in document order.
func costEntries(path string, records []*jq.Obj) ([]*jq.Obj, *core.RefusalError) {
	var entries []*jq.Obj
	for _, rec := range records {
		list, ref := modelsList(path, rec)
		if ref != nil {
			return nil, ref
		}
		entries = append(entries, objectItems(list)...)
	}
	return entries, nil
}

// modelsList reads one record's models field, refusing any shape but the
// array the reader prices.
func modelsList(path string, rec *jq.Obj) ([]any, *core.RefusalError) {
	raw, ok := rec.Get("models")
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, &core.RefusalError{
			Code: codeCostModelsNotArray,
			Message: filepath.Base(path) + ": models must be an array of named entries — " +
				`write it as {"models": [{"name": "<panel slug>", "model": "<model>", ...}]}`,
		}
	}
	return list, nil
}

// objectItems keeps the objects of a decoded array, dropping anything the
// repair cannot name in place.
func objectItems(items []any) []*jq.Obj {
	objects := make([]*jq.Obj, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(*jq.Obj); ok {
			objects = append(objects, obj)
		}
	}
	return objects
}

// costName reads an entry's model name, empty when it has none.
func costName(entry *jq.Obj) string {
	if raw, ok := entry.Get("name"); ok {
		if name, ok := raw.(string); ok {
			return name
		}
	}
	return ""
}

// contradictRefusal reports a name that cannot describe the seat it occupies.
func contradictRefusal(path string, index int, got, want string) *core.RefusalError {
	return &core.RefusalError{
		Code: codeCostEntryContradict,
		Message: fmt.Sprintf("%s: entry %d is named %q but the panel puts %q there",
			filepath.Base(path), index+1, got, want),
	}
}

// repairCostNames runs the cost-name repair when the verb asked for it.
func (st *svc) repairCostNames(dir string) *core.RefusalError {
	if !st.req.Flags.CostNames {
		return nil
	}
	return st.nameCostModels(dir, st.req.Flags.CostPanel)
}

// nameCostModels runs the repair the record verb was asked for: the flag's
// own list first, then a panel pinned in the bundle, then a refusal. A name
// is never invented from a slug, because a wrong attribution is worse than
// an absent one.
func (st *svc) nameCostModels(dir string, panel []string) *core.RefusalError {
	if len(panel) == 0 {
		panel = pinnedPanel(dir)
	}
	if len(panel) == 0 {
		return &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: "no panel to name the cost entries from: no list was passed and the bundle pins none",
		}
	}
	named, ref := NameCostModels(dir, panel, st.req.DryRun)
	if ref != nil {
		return ref
	}
	st.addWarning(costNameWarning(named, st.req.DryRun))
	return nil
}

// costNameWarning says what the pass did, or would do on a dry run.
func costNameWarning(named int, dryRun bool) string {
	switch {
	case named == 0:
		return "cost names: every models[] entry already carries its panel name"
	case dryRun:
		return fmt.Sprintf("cost names: would name %d models[] entries from the panel "+
			"(dry run — nothing written)", named)
	}
	return fmt.Sprintf("cost names: named %d models[] entries from the panel; no token count changed", named)
}

// pinnedPanel reads the panel the bundle was fetched under. Most bundles pin
// none: the panel came from a one-shot invocation, so the flag's own list is
// the source in practice.
func pinnedPanel(dir string) []string {
	rs, ok, err := config.ReadRunState(dir)
	if err != nil || !ok || rs.Resolved == nil {
		return nil
	}
	return rs.Resolved.Defaults.Values.Panel
}
