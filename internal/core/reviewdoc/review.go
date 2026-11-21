// Package reviewdoc maps the findings-only review document to the internal
// merge shape and renders the deterministic review body.
package reviewdoc

import (
	"encoding/json"
	"os"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Prepare rewrites one checked review document into the internal merge shape:
// claim becomes body and finder confidence gets its store name. The quote
// verification standing is left untouched: the bundle evidence pass stamps
// verified or unverified from the diff bytes before anything is stored.
func Prepare(review *jq.Obj) {
	for _, item := range Findings(review) {
		if m, ok := item.(*jq.Obj); ok {
			prepareFinding(m)
		}
	}
}

// prepareFinding maps one checked finding to its store keys.
func prepareFinding(m *jq.Obj) {
	if claim, has := m.Get("claim"); has {
		m.Set("body", claim)
	}
	if confidence, has := m.Get("confidence"); has {
		m.Set("finder_confidence", confidence)
	}
}

// Summary returns the optional human summary. It is never agent-controlled
// body markup: the renderer escapes nothing and renders it as one paragraph.
func Summary(review *jq.Obj) string {
	if review == nil {
		return ""
	}
	value, _ := review.Get("summary")
	if value == nil {
		return ""
	}
	return jq.R(value)
}

// Findings returns the raw findings array.
func Findings(review *jq.Obj) []any {
	return rawArray(review, "findings")
}

// SpecChecks returns the raw spec-check array.
func SpecChecks(review *jq.Obj) []any {
	return rawArray(review, "spec_checks")
}

// Receipts returns the raw receipt array.
func Receipts(review *jq.Obj) []any {
	return rawArray(review, "receipts")
}

// rawArray returns one top-level array, or nothing when it is absent or
// carries another type: the checker owns the refusal for either.
func rawArray(review *jq.Obj, key string) []any {
	if review == nil {
		return nil
	}
	value, _ := review.Get(key)
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

// SpecChecksTyped decodes the raw spec checks into the stored shape.
func SpecChecksTyped(review *jq.Obj) []core.SpecCheck {
	return decodeSlice[core.SpecCheck](SpecChecks(review))
}

// ReceiptsTyped decodes the raw receipts into the stored shape.
func ReceiptsTyped(review *jq.Obj) []core.Receipt {
	return decodeSlice[core.Receipt](Receipts(review))
}

func decodeSlice[T any](items []any) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		if value, ok := decodeOne[T](item); ok {
			out = append(out, value)
		}
	}
	return out
}

// decodeOne decodes one array entry, reporting whether it took the value.
func decodeOne[T any](item any) (T, bool) {
	var value T
	raw, ok := rawJSON(item)
	if !ok {
		return value, false
	}
	return value, json.Unmarshal(raw, &value) == nil
}

// rawJSON encodes one array entry: an ordered object keeps its byte order.
func rawJSON(item any) ([]byte, bool) {
	if obj, ok := item.(*jq.Obj); ok {
		return []byte(obj.MarshalCompact()), true
	}
	raw, err := json.Marshal(item)
	return raw, err == nil
}

// SpecFailuresOfBundle reads the record receipt's stored spec checks and
// returns every check that withholds approval, in stored order. A missing or
// unreadable record has no new evidence to judge.
func SpecFailuresOfBundle(dir string) []core.SpecFailure {
	raw, err := os.ReadFile(dir + readcommonRecordSuffix)
	if err != nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return nil
	}
	return SpecFailures(obj)
}

const readcommonRecordSuffix = "/record.json"

// HasSpecFailure reports a spec check that withholds approval in a review or
// record: one the code violates, or one nothing implements.
func HasSpecFailure(obj *jq.Obj) bool {
	return len(SpecFailures(obj)) > 0
}

// SpecFailures returns the status and item of every spec check that withholds
// approval — `violated` and `unmet` — in stored order. The status travels with
// the item because the decision names which failure kind it is: a violated
// requirement and a missing one are different sentences to the author, and the
// width of the list is the tally.
func SpecFailures(obj *jq.Obj) []core.SpecFailure {
	var out []core.SpecFailure
	for _, item := range SpecChecks(obj) {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		status, _ := m.Get("status")
		name := jq.R(status)
		if name != statusViolated && name != statusUnmet {
			continue
		}
		text, _ := m.Get("item")
		out = append(out, core.SpecFailure{Status: name, Item: jq.R(text)})
	}
	return out
}

// Verdict lead lines written at the top of every rendered review body.
const (
	CleanLead                = "> ✅ No new issues found.\n"
	MinorLead                = "> ℹ️ Informational findings only.\n"
	AttentionLead            = "> ⚠️ Findings require attention.\n"
	EvidenceInsufficientLead = "> ⛔ Evidence checks insufficient — no approval.\n"
)
