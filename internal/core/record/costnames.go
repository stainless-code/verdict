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
// first, then lenses, so an entry takes the panel name of its seat. Each
// record is named on its own: a document holding several records gives every
// record the same panel, never a slice of it, because one record's seats say
// nothing about another's.
//
// An entry that already names its seat keeps that name; a name that sits on
// the wrong seat, repeats, or comes from outside the panel is a
// contradiction and refuses, because a repair must never silently
// reattribute spend. An object-shaped models field, a non-object entry, and
// an undecodable record all refuse with nothing written.
//
// dryRun plans and validates the same pass without touching a byte, so
// `record --dry-run --cost-names` proves the repair before it lands. found
// reports whether the bundle carried any cost record at all, so a caller can
// tell "nothing to name" from "already named".
//
// The pass is idempotent: a second run changes no byte and reports zero
// names gained.
func NameCostModels(dir string, panel []string, dryRun bool) (named int, found bool, ref *core.RefusalError) {
	plans, found, ref := planCostNames(dir, panel)
	if ref != nil {
		return 0, false, ref
	}
	named, ref = writePlans(plans, dryRun)
	return named, found, ref
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
func planCostNames(dir string, panel []string) ([]costNamePlan, bool, *core.RefusalError) {
	files := readcommon.CostFiles(dir)
	plans := make([]costNamePlan, 0, len(files))
	found := false
	for _, path := range files {
		plan, planned, ref := planCostFile(path, panel)
		if ref != nil {
			return nil, false, ref
		}
		if !planned {
			continue
		}
		found = true
		plans = append(plans, plan)
	}
	return plans, found, nil
}

// planCostFile plans one cost file, reporting false when it holds no record.
func planCostFile(path string, panel []string) (costNamePlan, bool, *core.RefusalError) {
	doc, ref := decodeCostFile(path)
	if ref != nil || doc == nil {
		return costNamePlan{}, false, ref
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
// single record object or an array of them. Every record is named on its own
// from the whole panel: an array document is a list of runs or groups, so
// flattening their entries would put one record's seats on another's spend.
func nameCostDocument(path string, doc any, panel []string) (int, *core.RefusalError) {
	records := costRecords(doc)
	if len(records) == 0 {
		return 0, nil
	}
	if ref := checkPanel(path, panel); ref != nil {
		return 0, ref
	}
	gained := 0
	for i, rec := range records {
		n, ref := nameRecord(path, i, len(records), rec, panel)
		if ref != nil {
			return 0, ref
		}
		gained += n
	}
	return gained, nil
}

// checkPanel refuses a panel that cannot address seats one to one.
func checkPanel(path string, panel []string) *core.RefusalError {
	if len(panel) == 0 {
		return &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: filepath.Base(path) + ": no panel to name the cost entries from",
		}
	}
	if name, ok := repeatedName(panel); !ok {
		return &core.RefusalError{
			Code: codeCostPanelUnresolved,
			Message: fmt.Sprintf("%s: the panel names %q twice — each seat needs one distinct name",
				filepath.Base(path), name),
		}
	}
	return nil
}

// repeatedName reports the first name a panel lists twice.
func repeatedName(panel []string) (string, bool) {
	seen := make(map[string]bool, len(panel))
	for _, name := range panel {
		if seen[name] {
			return name, false
		}
		seen[name] = true
	}
	return "", true
}

// nameRecord names one record's entries from the panel, refusing when the
// record has no one-to-one seat mapping.
func nameRecord(path string, index, total int, rec *jq.Obj, panel []string) (int, *core.RefusalError) {
	entries, ref := recordEntries(path, rec)
	if ref != nil {
		return 0, ref
	}
	if len(entries) == 0 {
		return 0, nil
	}
	who := recordName(path, index, total)
	if len(entries) != len(panel) {
		return 0, &core.RefusalError{
			Code: codeCostEntryUnnamed,
			Message: fmt.Sprintf("%s: %d models[] entries but %d panel names — "+
				"pass the panel that ran for this record, in order, so every entry has exactly one name",
				who, len(entries), len(panel)),
		}
	}
	gained := 0
	for i, entry := range entries {
		switch name := costName(entry); name {
		case panel[i]:
			continue
		case "":
			entry.Set("name", panel[i])
			gained++
		default:
			return 0, seatRefusal(who, i, name, panel[i])
		}
	}
	return gained, nil
}

// recordEntries reads one record's models array, refusing any shape the
// reader cannot price as entry objects.
func recordEntries(path string, rec *jq.Obj) ([]*jq.Obj, *core.RefusalError) {
	raw, ok := rec.Get("models")
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, notArrayRefusal(path, "models is an object")
	}
	entries := make([]*jq.Obj, 0, len(list))
	for i, item := range list {
		obj, ok := item.(*jq.Obj)
		if !ok {
			return nil, notArrayRefusal(path, fmt.Sprintf("entry %d is not an object", i+1))
		}
		entries = append(entries, obj)
	}
	return entries, nil
}

// notArrayRefusal refuses a models field the reader cannot attribute.
func notArrayRefusal(path, what string) *core.RefusalError {
	return &core.RefusalError{
		Code: codeCostModelsNotArray,
		Message: filepath.Base(path) + ": models must be an array of entry objects (" + what + ") — " +
			`write it as {"models": [{"name": "<panel slug>", "model": "<model>", ...}]}`,
	}
}

// recordName names the record a refusal is about, so a multi-record file
// points at the record that failed rather than the whole file.
func recordName(path string, index, total int) string {
	if total <= 1 {
		return filepath.Base(path)
	}
	return fmt.Sprintf("%s record %d", filepath.Base(path), index+1)
}

// costRecords collects the record objects of one decoded cost file.
func costRecords(doc any) []*jq.Obj {
	switch d := doc.(type) {
	case *jq.Obj:
		return []*jq.Obj{d}
	case []any:
		records := make([]*jq.Obj, 0, len(d))
		for _, item := range d {
			if rec, ok := item.(*jq.Obj); ok {
				records = append(records, rec)
			}
		}
		return records
	}
	return nil
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

// seatRefusal reports a name that cannot describe the seat it occupies.
func seatRefusal(who string, index int, got, want string) *core.RefusalError {
	return &core.RefusalError{
		Code: codeCostEntryContradict,
		Message: fmt.Sprintf("%s: entry %d is named %q but the panel puts %q there",
			who, index+1, got, want),
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
	named, found, ref := NameCostModels(dir, panel, st.req.DryRun)
	if ref != nil {
		return ref
	}
	st.addWarning(costNameWarning(named, found, st.req.DryRun))
	return nil
}

// costNameWarning says what the pass did, or would do on a dry run.
func costNameWarning(named int, found, dryRun bool) string {
	switch {
	case !found:
		return "cost names: this bundle carries no cost record to name"
	case named == 0:
		return "cost names: every models[] entry already names its panel seat"
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
