package recordcore

import (
	"fmt"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// warnCostModels warns once per cost models[] entry with no name, naming the
// file, the entry position, and the schema rule it breaks. The record stays
// accepted and priced: the reader tolerates a nameless entry so older bundles
// still price, and record is where the writer can still hear about it.
func (st *svc) warnCostModels(dir string) {
	for _, msg := range namelessCostEntries(dir) {
		st.addWarning(msg)
	}
}

// namelessCostEntries scans every cost record the bundle carries. Unreadable,
// undecodable, and unpriceable files stay silent here: they belong to another
// check's refusal or the rollup's own warning.
func namelessCostEntries(dir string) []string {
	var out []string
	for _, path := range readcommon.CostFiles(dir) {
		out = append(out, namelessCostFile(path)...)
	}
	return out
}

// namelessCostFile names every nameless models[] entry of one cost file. A
// document holding several records qualifies each warning with its record, so
// two records' models[0] cannot collapse into one line.
func namelessCostFile(path string) []string {
	records, ok := readCostRecords(path)
	if !ok {
		return nil
	}
	base := filepath.Base(path)
	var out []string
	for n, rec := range records {
		out = append(out, namelessRecordEntries(path, base, n, len(records), rec)...)
	}
	return out
}

// readCostRecords decodes one cost file into the record objects the reader
// prices. Anything missing, unreadable, undecodable, or unpriceable reads
// false and stays silent: it belongs to another check, never this warning.
func readCostRecords(path string) ([]*jq.Obj, bool) {
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
	if err != nil {
		return nil, false
	}
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return nil, false
	}
	records, ok := readcommon.CostRecordObjects(doc)
	if !ok {
		return nil, false
	}
	return records, true
}

// namelessRecordEntries names the nameless models[] entries of one record.
func namelessRecordEntries(path, base string, n, total int, rec *jq.Obj) []string {
	models, ref := recordEntries(path, rec)
	if ref != nil {
		return nil
	}
	who := base
	if total > 1 {
		who = fmt.Sprintf("%s record %d", base, n+1)
	}
	var out []string
	for i, entry := range models {
		if !hasCostName(entry) {
			out = append(out, fmt.Sprintf("%s: models[%d] has no name — the name is what attribution reads; add \"name\"", who, i))
		}
	}
	return out
}

// hasCostName reports whether an entry satisfies the schema's required name:
// present and a string. The schema sets no minimum length, so an empty string
// passes and only a missing or non-string name warns.
func hasCostName(entry *jq.Obj) bool {
	raw, ok := entry.Get("name")
	if !ok {
		return false
	}
	_, ok = raw.(string)
	return ok
}
