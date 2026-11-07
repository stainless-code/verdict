package recordcore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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

// detailSep joins a file name to the reason it refused, one shape for every
// message so an operator can read the location straight off the line.
const detailSep = ": "

// CostNamePlan is one planned rewrite: the decoded document it came from,
// the file it belongs to, and how many entries gain a name. Planning every
// file before writing any keeps the pass all-or-nothing, so a refusal never
// leaves half a bundle named.
type CostNamePlan struct {
	path     string
	rendered []byte
	gains    int
	entries  int
	// seen is the file as planning found it. The write refuses when the file
	// changed since: a producer that finished a cost update between planning
	// and writing would be overwritten by the older snapshot, and the run would
	// still report that no token count changed.
	seen fileStamp
}

// fileStamp identifies the bytes planning read, so the write can prove it is
// replacing the file it named rather than a newer one.
type fileStamp struct {
	modUnixNano int64
	size        int64
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
// reports whether the bundle carried any cost record at all and entries how
// many models[] entries those records hold, so a caller can tell "nothing to
// name" from "no entries to name" from "already named".
//
// The pass is idempotent: a second run changes no byte and reports zero
// names gained.
func NameCostModels(dir string, panel []string, dryRun bool) (named, entries int, found bool, ref *core.RefusalError) {
	plans, entries, found, ref := PlanCostNames(dir, panel)
	if ref != nil {
		return 0, 0, false, ref
	}
	named, ref = ApplyCostNames(plans, dryRun)
	return named, entries, found, ref
}

// PlanCostNames decodes every cost record and returns the rewrites the pass
// would make, writing nothing. Planning is separate from writing so a caller
// can refuse before it touches the bundle at all: `record` plans before its
// merge, then applies after it, so a cost-name refusal never leaves findings
// behind.
func PlanCostNames(dir string, panel []string) ([]CostNamePlan, int, bool, *core.RefusalError) {
	return planCostNames(dir, panel)
}

// ApplyCostNames performs planned rewrites, or counts them on a dry run. It is
// the only half that writes, so every refusal it can raise is a write failure
// mid-pass and says which files are already on disk.
func ApplyCostNames(plans []CostNamePlan, dryRun bool) (int, *core.RefusalError) {
	return writePlans(plans, dryRun)
}

// writtenFiles lists what a failed pass already rewrote, so the refusal can
// point at the files on disk instead of describing them.
func writtenFiles(written []string) string {
	if len(written) == 0 {
		return ""
	}
	if len(written) == 1 {
		return " (" + written[0] + " was rewritten before the failure and is kept)"
	}
	return " (" + strings.Join(written, ", ") + " were rewritten before the failure and are kept)"
}

// writePlans performs the planned rewrites, or counts them on a dry run.
func writePlans(plans []CostNamePlan, dryRun bool) (int, *core.RefusalError) {
	renamed := 0
	written := make([]string, 0, len(plans))
	for i := range plans {
		plan := &plans[i]
		n, ref := applyPlan(plan, dryRun)
		if ref != nil {
			return renamed, &core.RefusalError{Code: ref.Code, Message: ref.Message + writtenFiles(written)}
		}
		if n > 0 && !dryRun {
			written = append(written, filepath.Base(plan.path))
		}
		renamed += n
	}
	return renamed, nil
}

// applyPlan writes one planned rewrite, or counts it on a dry run, reporting
// how many entries the file gains.
func applyPlan(plan *CostNamePlan, dryRun bool) (int, *core.RefusalError) {
	if plan.gains == 0 || dryRun {
		return plan.gains, nil
	}
	if ref := writePlan(plan); ref != nil {
		return 0, ref
	}
	return plan.gains, nil
}

// stampOf reads the size and modification time that identify a cost file's
// current bytes.
func stampOf(path string) (fileStamp, *core.RefusalError) {
	info, err := fs.Stat(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		return fileStamp{}, &core.RefusalError{
			Code:    codeCostRecordUnreadable,
			Message: "could not stat " + path + detailSep + err.Error(),
		}
	}
	return fileStamp{modUnixNano: info.ModTime().UnixNano(), size: info.Size()}, nil
}

// renderPlan renders a planned rewrite and checks it against the spend
// reader's cap. It runs at plan time, not write time: pretty-printing can grow
// a near-cap record past the cap, and a caller that plans before its merge must
// hear that refusal before anything is merged.
func renderPlan(path string, doc any) ([]byte, *core.RefusalError) {
	rendered := []byte(jq.PrettyOrdered(doc))
	if int64(len(rendered)) > jq.MaxCostBytes {
		return nil, &core.RefusalError{
			Code: codeCostRecordUnreadable,
			Message: fmt.Sprintf("%s%sadding names would grow the record to %d bytes, "+
				"past the %d-byte cap the reader enforces — nothing was written",
				path, detailSep, len(rendered), jq.MaxCostBytes),
		}
	}
	return rendered, nil
}

// writePlan rewrites one cost file from the bytes planning already rendered and
// cap-checked, keeping its modification time: the measured-usage join attaches
// the session whose window brackets that stamp, so a repair that moved it would
// silently drop a paid measurement.
func writePlan(plan *CostNamePlan) *core.RefusalError {
	now, ref := stampOf(plan.path)
	if ref != nil {
		return ref
	}
	if now != plan.seen {
		return &core.RefusalError{
			Code: codeCostRecordUnreadable,
			Message: "refusing to rewrite " + plan.path + detailSep +
				"the record changed after it was planned, so naming it would drop the " +
				"update — re-run to plan against the current record",
		}
	}
	if err := atomic.WriteFileKeepingModTime(plan.path, plan.rendered); err != nil {
		return &core.RefusalError{
			Code: codeCostRecordUnreadable,
			Message: "could not write " + plan.path + detailSep + err.Error() +
				" — re-run once the file is writable to finish the repair",
		}
	}
	return nil
}

// planCostNames decodes every cost record and records the rewrite each one
// needs, refusing on the first that cannot be named correctly.
func planCostNames(dir string, panel []string) ([]CostNamePlan, int, bool, *core.RefusalError) {
	files := readcommon.CostFiles(dir)
	plans := make([]CostNamePlan, 0, len(files))
	entries := 0
	found := false
	for _, path := range files {
		plan, planned, ref := planCostFile(path, panel)
		if ref != nil {
			return nil, 0, false, ref
		}
		if !planned {
			continue
		}
		found = true
		entries += plan.entries
		plans = append(plans, plan)
	}
	return plans, entries, found, nil
}

// planCostFile plans one cost file, reporting false when it holds no record.
func planCostFile(path string, panel []string) (CostNamePlan, bool, *core.RefusalError) {
	doc, ref := decodeCostFile(path)
	if ref != nil || doc == nil {
		return CostNamePlan{}, false, ref
	}
	gains, entries, ref := nameCostDocument(path, doc, panel)
	if ref != nil {
		return CostNamePlan{}, false, ref
	}
	seen, ref := stampOf(path)
	if ref != nil {
		return CostNamePlan{}, false, ref
	}
	plan := CostNamePlan{path: path, gains: gains, entries: entries, seen: seen}
	if gains > 0 {
		rendered, ref := renderPlan(path, doc)
		if ref != nil {
			return CostNamePlan{}, false, ref
		}
		plan.rendered = rendered
	}
	return plan, true, nil
}

// decodeCostFile reads and decodes one cost record, returning a nil document
// when the file is not there at all.
func decodeCostFile(path string) (any, *core.RefusalError) {
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		reason := err.Error()
		if errors.Is(err, jq.ErrTooLarge) {
			reason = fmt.Sprintf("the record is larger than the %d-byte cap the spend "+
				"reader enforces; adding names cannot make it readable again",
				jq.MaxCostBytes)
		}
		return nil, &core.RefusalError{
			Code:    codeCostRecordUnreadable,
			Message: "could not read " + path + detailSep + reason,
		}
	}
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return nil, &core.RefusalError{
			Code:    codeCostRecordUnreadable,
			Message: "could not decode " + path + detailSep + derr.Error(),
		}
	}
	return doc, nil
}

// nameCostDocument names the records of one cost file, which holds either a
// single record object or an array of them. Every record is named on its own
// from the whole panel: an array document is a list of runs or groups, so
// flattening their entries would put one record's seats on another's spend.
func nameCostDocument(path string, doc any, panel []string) (gained, entries int, ref *core.RefusalError) {
	records, ok := readcommon.CostRecordObjects(doc)
	if !ok {
		return 0, 0, shapeRefusal(path, doc)
	}
	if len(records) == 0 {
		return 0, 0, nil
	}
	if ref := checkPanel(path, panel); ref != nil {
		return 0, 0, ref
	}
	positions, total := recordPositions(doc, len(records))
	for i, rec := range records {
		n, seen, ref := nameRecord(path, positions[i], total, rec, panel)
		if ref != nil {
			return 0, 0, ref
		}
		gained += n
		entries += seen
	}
	return gained, entries, nil
}

// recordPositions numbers each record by its place in the document, so a
// refusal points at the element the operator sees: skipping null records must
// not renumber the records after them. A single-object document has one
// position and no number at all (recordName omits it when total is one).
func recordPositions(doc any, records int) (positions []int, total int) {
	arr, isArr := doc.([]any)
	if !isArr {
		return []int{0}, 1
	}
	positions = make([]int, 0, records)
	for i, item := range arr {
		if _, isObj := item.(*jq.Obj); isObj {
			positions = append(positions, i)
		}
	}
	return positions, len(arr)
}

// checkPanel refuses a panel that cannot address seats one to one.
func checkPanel(path string, panel []string) *core.RefusalError {
	if len(panel) == 0 {
		return &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: filepath.Base(path) + detailSep + "no panel to name the cost entries from",
		}
	}
	if slices.Contains(panel, "") {
		return &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: filepath.Base(path) + detailSep + "a panel seat has no name — every seat needs one",
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
func nameRecord(path string, index, total int, rec *jq.Obj, panel []string) (gained, found int, ref *core.RefusalError) {
	entries, ref := recordEntries(path, rec)
	if ref != nil {
		return 0, 0, ref
	}
	if len(entries) == 0 {
		return 0, 0, nil
	}
	who := recordName(path, index, total)
	found = len(entries)
	if len(entries) != len(panel) {
		return 0, 0, &core.RefusalError{
			Code: codeCostEntryUnnamed,
			Message: fmt.Sprintf("%s: %d models[] entries but %d panel names — "+
				"pass the panel that ran for this record, in order, so every entry has exactly one name",
				who, len(entries), len(panel)),
		}
	}
	for i, entry := range entries {
		switch name := costName(entry); name {
		case panel[i]:
			continue
		case "":
			entry.Set("name", panel[i])
			gained++
		default:
			return 0, 0, seatRefusal(who, i, name, panel[i])
		}
	}
	return gained, found, nil
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
		return nil, notArrayRefusal(path, "models is a JSON "+jsonKind(raw))
	}
	entries := make([]*jq.Obj, 0, len(list))
	for i, item := range list {
		obj, ok := item.(*jq.Obj)
		if !ok {
			return nil, notArrayRefusal(path, "entry "+strconv.Itoa(i+1)+" is a JSON "+jsonKind(item))
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

// shapeRefusal refuses a document the reader cannot price, naming the shape it
// found so the operator knows what to delete rather than guessing.
func shapeRefusal(path string, doc any) *core.RefusalError {
	what := "the document is a JSON " + jsonKind(doc) + ", not a record object or an array of them"
	if arr, isArr := doc.([]any); isArr {
		for i, item := range arr {
			if _, isObj := item.(*jq.Obj); !isObj && item != nil {
				what = fmt.Sprintf("entry %d is a JSON %s, not a record object", i+1, jsonKind(item))
				break
			}
		}
	}
	return &core.RefusalError{
		Code: codeCostRecordUnreadable,
		Message: filepath.Base(path) + detailSep + what +
			" — the spend rollup cannot price this file, so naming part of it would report a repair nothing reads",
	}
}

// jsonKind names a decoded JSON value's type, so a refusal can say what it
// found instead of assuming the common case.
func jsonKind(value any) string {
	switch value.(type) {
	case *jq.Obj:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "number"
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

// costNamePass is one planned repair: every file validated, nothing written,
// plus the counts the warning needs once it lands.
type costNamePass struct {
	plans   []CostNamePlan
	entries int
	found   bool
}

// planCostNames resolves the panel and validates every cost record, writing
// nothing. It runs before the merge so a missing panel, a contradiction, or a
// shape the reader cannot price refuses while the bundle is still untouched:
// a refusal after the merge would leave findings a later `fix` could consume.
func (st *svc) planCostNames(dir string) (*costNamePass, *core.RefusalError) {
	if !st.req.Flags.CostNames {
		return nil, nil
	}
	panel := st.req.Flags.CostPanel
	if len(panel) == 0 {
		pinned, conflict := pinnedPanel(dir)
		if conflict != "" {
			return nil, &core.RefusalError{
				Code: codeCostPanelUnresolved,
				Message: "no single panel to name the cost entries from: " + conflict +
					" — pass --cost-names with the panel that ran, one list for every record",
			}
		}
		panel = pinned
	}
	if len(panel) == 0 {
		return nil, &core.RefusalError{
			Code:    codeCostPanelUnresolved,
			Message: "no panel to name the cost entries from: no list was passed and the bundle pins none",
		}
	}
	plans, entries, found, ref := PlanCostNames(dir, panel)
	if ref != nil {
		return nil, ref
	}
	return &costNamePass{plans: plans, entries: entries, found: found}, nil
}

// applyCostNames writes the planned rewrites, or counts them on a dry run, and
// reports what the pass did.
func (st *svc) applyCostNames(pass *costNamePass) *core.RefusalError {
	if pass == nil {
		return nil
	}
	named, ref := ApplyCostNames(pass.plans, st.req.DryRun)
	if ref != nil {
		return ref
	}
	st.addWarning(costNameWarning(named, pass.entries, pass.found, st.req.DryRun))
	return nil
}

// costNameWarning says what the pass did, or would do on a dry run.
func costNameWarning(named, entries int, found, dryRun bool) string {
	switch {
	case !found:
		return "cost names: this bundle carries no cost record to name"
	case entries == 0:
		return "cost names: the cost record holds no models[] entry to name"
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
// the source in practice. conflict is set when the resolved scopes pin panels
// that differ: a cost record does not say which scope wrote it, so one pinned
// list would put the default seats' names on another panel's spend.
func pinnedPanel(dir string) (panel []string, conflict string) {
	rs, ok, err := config.ReadRunState(dir)
	if err != nil || !ok || rs.Resolved == nil {
		return nil, ""
	}
	pin := panelPin{panel: rs.Resolved.Defaults.Values.Panel, by: "the defaults", verb: " pin"}
	if len(pin.panel) == 0 {
		pin.by = ""
	}
	return pin.panel, pin.addScopes(rs.Resolved.Files)
}

// panelPin tracks the one panel a bundle pins, and who pinned it, so a second
// differing pin can name both sides of the disagreement.
type panelPin struct {
	verb  string
	by    string
	panel []string
}

// addScopes folds every resolved scope into the pin, returning the first
// conflict it meets.
func (p *panelPin) addScopes(files []config.FileResolution) string {
	for i := range files {
		if conflict := p.addScope(&files[i]); conflict != "" {
			return conflict
		}
	}
	return ""
}

// addScope folds one scope's panel into the pin. A scope that pins nothing, or
// pins what is already pinned, agrees. When the defaults pin nothing the first
// scope to pin is adopted and the rest are compared against it: scopes that
// agree name one panel, and refusing them would be a false conflict over a
// decision they did not make.
func (p *panelPin) addScope(file *config.FileResolution) string {
	scoped := file.Defaults.Values.Panel
	if len(scoped) == 0 || samePanel(scoped, p.panel) {
		return ""
	}
	if p.by == "" {
		p.panel, p.by, p.verb = scoped, "scope "+scopeName(file), " pins"
		return ""
	}
	return "scope " + scopeName(file) + " pins " + panelList(scoped) +
		" while " + p.by + p.verb + " " + panelList(p.panel)
}

// scopeName names the resolved scope a conflicting panel came from.
func scopeName(file *config.FileResolution) string {
	if len(file.Scopes) > 0 {
		return strings.Join(file.Scopes, ",")
	}
	return file.Path
}

// panelList renders a panel the way the flag takes it.
func panelList(panel []string) string {
	return "[" + strings.Join(panel, ", ") + "]"
}

// samePanel reports whether two panels name the same seats in the same order.
func samePanel(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
