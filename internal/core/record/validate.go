package recordcore

import (
	"bytes"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// validateInputs validates review input and prior findings: every shape the
// merge cannot read refuses here as a typed refusal, before any merge output
// is written. The order is fixed (comments, resolved, triage, sections,
// verification claims, prior store) so the first refusal is deterministic.
func (st *state) validateInputs() error {
	if err := checkCommentsShape(st.review); err != nil {
		return err
	}
	if err := checkResolvedShape(st.review); err != nil {
		return err
	}
	if err := checkTriageShape(st.review); err != nil {
		return err
	}
	if err := checkSectionsShape(st.review); err != nil {
		return err
	}
	if err := checkVerificationShape(st.dir); err != nil {
		return err
	}
	return checkPriorShape(st.dir)
}

// reviewList returns the named top-level claim list, or nil when absent,
// null, or false (all read as "no claims", matching jq //). ok is false when
// present but not an array.
func reviewList(review *jq.Obj, key string) ([]any, bool) {
	v, has := review.Get(key)
	if !has || v == nil || v == false {
		return nil, true
	}
	arr, ok := v.([]any)
	return arr, ok
}

// checkCommentsShape validates the comments claim list and its entries.
func checkCommentsShape(review *jq.Obj) error {
	items, ok := reviewList(review, "comments")
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedComments}
	}
	for _, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectComment}
		}
		if err := checkCommentScalars(m); err != nil {
			return err
		}
	}
	return nil
}

// checkCommentScalars validates the string-concatenated comment fields: path,
// body, and suggestion must be strings when present (absent, null, and false
// read as absent). Numbers, booleans, and containers would corrupt the
// rendered body, so they refuse here.
func checkCommentScalars(m *jq.Obj) error {
	for _, key := range []string{fieldPath, fieldBody, fieldSuggestion} {
		v, has := m.Get(key)
		if !has || v == nil || v == false {
			continue
		}
		if _, ok := v.(string); !ok {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonStringScalar}
		}
	}
	return nil
}

// checkResolvedShape validates the resolved claim list: an array of strings.
func checkResolvedShape(review *jq.Obj) error {
	items, ok := reviewList(review, "resolved")
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedResolved}
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonStringResolved}
		}
	}
	return nil
}

// checkTriageShape validates the triage claim list: an array of objects.
func checkTriageShape(review *jq.Obj) error {
	items, ok := reviewList(review, "triage")
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedTriage}
	}
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectTriage}
		}
	}
	return nil
}

// checkSectionsShape validates the sections claim list: an array of objects.
func checkSectionsShape(review *jq.Obj) error {
	items, ok := reviewList(review, "sections")
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedSections}
	}
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectSection}
		}
	}
	return nil
}

// checkVerificationShape validates the verification.json claims: an absent or
// unparsable file reads as no claims, but
// a present file must hold an array of claim objects.
func checkVerificationShape(dir string) error {
	raw, err := readAgentFile(dir + "/verification.json")
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return ref
		}
		raw, _ = ReadOptional(dir + "/verification.json")
	}
	return checkVerificationClaims(raw)
}

// checkVerificationClaims validates decoded verification claims.
func checkVerificationClaims(raw []byte) error {
	if raw == nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		//nolint:nilerr // undecodable evidence reads as "no claims", never a refusal
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: msgMalformedVerifyClaims}
	}
	return checkClaimItems(items)
}

// checkClaimItems validates every verification claim is an object.
func checkClaimItems(items []any) error {
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &core.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectVerifyClaim}
		}
	}
	return nil
}

// checkPriorShape validates the prior findings store: absent, empty, null,
// false, or a JSON list read as (no) prior; anything else refuses as
// corrupt-store — the run never starts fresh over history. Every present
// element must be an object passing the store shape: exotic elements refuse
// typed here instead of corrupting the store. Null reads as no prior for the
// read verbs, but the merge indexes elements by sig, so the write path
// refuses null too.
func checkPriorShape(dir string) error {
	path := dir + "/findings.json"
	raw, ok := ReadOptional(path)
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	if doc == nil || doc == false {
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	return checkPriorElements(path, items)
}

// checkPriorElements validates every prior element against the store shape.
func checkPriorElements(path string, items []any) error {
	for _, item := range items {
		if !priorElementOK(item) {
			return &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
		}
	}
	return nil
}

// priorElementOK reports whether one prior element passes the store shape:
// a non-null object whose string-indexed fields read null-or-string and
// whose indexed-map fields never read object-or-array (entries_guard).
func priorElementOK(item any) bool {
	m, ok := item.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	return priorScalarsOK(m) && priorMapsOK(m)
}

// priorScalarsOK validates the string-indexed fields.
func priorScalarsOK(m *jq.Obj) bool {
	for _, key := range []string{fieldSig, fieldBody, fieldHeading, "kind", fieldSuggestion} {
		if v, has := m.Get(key); has && v != nil && v != false {
			if _, ok := v.(string); !ok {
				return false
			}
		}
	}
	return true
}

// priorMapsOK validates the indexed-map fields.
func priorMapsOK(m *jq.Obj) bool {
	for _, key := range []string{fieldSeverity, fieldRigor} {
		if v, has := m.Get(key); has && isContainer(v) {
			return false
		}
	}
	return true
}

// isContainer reports whether v decodes as a JSON object or array.
func isContainer(v any) bool {
	switch v.(type) {
	case *jq.Obj, map[string]any, []any:
		return true
	default:
		return false
	}
}
