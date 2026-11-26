package readcommon

import (
	"errors"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Store refusal codes and messages, frozen as the wire contract.
const (
	codeMissingFindings = "missing-findings"
	codeCorruptStore    = "corrupt-store"
	codeBundleOutdated  = "bundle-outdated"
	msgMissingFindings  = "missing %s/findings.json (run post or record first)"
	msgCorruptStore     = "findings store %s/findings.json is unreadable or" +
		" not a findings array (re-run post/record to rebuild, or restore the file)"
)

// LoadFindings owns the findings-store preamble shared by the readers:
// the file must exist (missing-findings) and hold a JSON array
// (corrupt-store). Entries must additionally be objects-or-null with a
// null-or-string sig — anything else refuses typed corrupt-store instead of
// reaching the rank filters (see evals/cases/read-refusals.sh).
func LoadFindings(dir string) ([]any, error) {
	raw, err := jq.CappedFile(dir+"/findings.json", jq.MaxFindingsBytes)
	if err != nil {
		// An oversized store is present but unreadable, so it refuses
		// corrupt-store; only a genuinely absent file reads missing-findings.
		if errors.Is(err, jq.ErrTooLarge) {
			return nil, corruptStore(dir)
		}
		return nil, &core.RefusalError{Code: codeMissingFindings, Message: core.Msgf(msgMissingFindings, dir)}
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, corruptStore(dir)
	}
	entries, ok := doc.([]any)
	if !ok {
		return nil, corruptStore(dir)
	}
	if !ValidEntries(entries) {
		return nil, corruptStore(dir)
	}
	if ref := OutdatedEntries(dir, entries); ref != nil {
		return nil, ref
	}
	return entries, nil
}

// OutdatedEntries refuses a findings store an older verdict wrote: an entry
// whose verification is the bare array of panel results (0.7.x and before)
// rather than the {quote, panel} object. Nothing reads that shape, so the
// store must be rebuilt by re-running the review, not read with its panel
// silently dropped.
func OutdatedEntries(dir string, entries []any) *core.RefusalError {
	if !slices.ContainsFunc(entries, arrayVerification) {
		return nil
	}
	return &core.RefusalError{Code: codeBundleOutdated, Message: core.Msgf(
		"findings store %s/findings.json was written by an older verdict (a finding's "+
			"verification is a bare array) — re-run the review and record to rebuild it", filepath.ToSlash(dir))}
}

// arrayVerification reports whether one store entry carries the bare panel
// array an older verdict wrote in place of the verification object.
func arrayVerification(e any) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	v, _ := m.Get("verification")
	_, isArr := v.([]any)
	return isArr
}

func corruptStore(dir string) *core.RefusalError {
	return &core.RefusalError{Code: codeCorruptStore, Message: core.Msgf(msgCorruptStore, dir)}
}

// ValidEntries enforces the entry shape shared by the readers: every entry
// is null or an object whose string-indexed fields (sig, body, heading,
// kind) read null-or-string and whose indexed-map fields (severity, rigor)
// never read object-or-array. Anything else refuses typed corrupt-store
// instead of reaching the rank, resolve, display, or SARIF filters (see
// evals/cases/read-refusals.sh).
// Fields the readers only compare or interpolate (status, path, line,
// anchored, inBounds, kb) stay unguarded: exotic shapes flow crash-free.
func ValidEntries(entries []any) bool {
	for _, e := range entries {
		if !validEntry(e) {
			return false
		}
	}
	return true
}

func validEntry(e any) bool {
	if e == nil {
		return true
	}
	m, ok := e.(*jq.Obj)
	if !ok {
		return false
	}
	return validEntryStrings(m) && validEntryMaps(m)
}

func validEntryStrings(m *jq.Obj) bool {
	for _, key := range []string{"sig", "body", "heading", "kind", "suggestion"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func validEntryMaps(m *jq.Obj) bool {
	for _, key := range []string{"severity", "rigor"} {
		v, has := m.Get(key)
		if !has || v == nil {
			continue
		}
		switch v.(type) {
		case map[string]any, *jq.Obj, []any:
			return false
		default:
		}
	}
	return true
}

// ResolveSig resolves the unique full sig with the given prefix,
// or the NONE/MANY sentinels. Non-string sigs never match (the entry guard
// above already refused exotic stores).
func ResolveSig(entries []any, prefix string) string {
	var match string
	n := 0
	for _, e := range entries {
		s, ok := entrySig(e)
		if !ok {
			continue
		}
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			n++
			match = s
		}
	}
	switch n {
	case 0:
		return "NONE"
	case 1:
		return match
	default:
		return "MANY"
	}
}

func entrySig(e any) (string, bool) {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return "", false
	}
	sig, _ := m.Get("sig")
	s, ok := sig.(string)
	if !ok {
		return "", false
	}
	return s, true
}

const (
	sourceModel = "model"
	sourceTool  = "tool"
)

// SourceOf returns the finding entry's provenance ("tool" when ingested from
// static analysis, "model" otherwise, preserving backward compatibility for
// entries written before the source field existed).
func SourceOf(e any) string {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return sourceModel
	}
	v, _ := m.Get("source")
	if s, ok := v.(string); ok && s == sourceTool {
		return sourceTool
	}
	return sourceModel
}

// SourceCounts returns counts keyed by "model" and "tool" across non-nil
// entries.
func SourceCounts(entries []any) map[string]int {
	counts := map[string]int{sourceModel: 0, sourceTool: 0}
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		counts[SourceOf(m)]++
	}
	return counts
}
