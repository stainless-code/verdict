package record

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// maxAgentBytes caps one agent-written input (review.json, verification.json,
// chunk manifest) at 32 MiB. Go-only defense-in-depth (R3): the shell reads
// whatever the agent wrote, but a bounded read keeps a hostile bundle from
// exhausting memory. Unknown keys are never rejected — decoding stays open.
const maxAgentBytes = 32 << 20

// readAgentFile reads one agent-written input with the size cap applied.
// Missing/unreadable files report the read error; over-cap files report a
// typed refusal (callers distinguish with [errors.As]).
func readAgentFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		// Sealed at the layer boundary (GO-ERRORS-03, R4); callers only
		// distinguish over-cap refusals via errors.As.
		return nil, jq.Sealed("read agent file", err)
	}
	if len(raw) > maxAgentBytes {
		return nil, &cli.RefusalError{Code: codeBadReviewInput, Message: path +
			" exceeds the 32 MiB agent-input cap (shrink the review, then re-run)"}
	}
	return raw, nil
}

// readOptional reads an optional input file; ok is false when missing or
// unreadable (absent inputs read as absent claims/priors, never failures).
func readOptional(path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// agentRefusal reports whether err is an over-cap refusal from readAgentFile.
func agentRefusal(err error) error {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
		return ref
	}
	return nil
}

// validateInputs ports validate_review_input + validate_prior_findings: every
// shape jq would crash on downstream refuses here as a typed refusal, before
// any merge output is written. Order mirrors the shell (comments, resolved,
// triage, sections, verification claims, prior store) so the first refusal
// matches byte-for-byte.
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
		return &cli.RefusalError{Code: codeBadReviewInput, Message: msgMalformedComments}
	}
	for _, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectComment}
		}
		if err := checkCommentScalars(m); err != nil {
			return err
		}
	}
	return nil
}

// checkCommentScalars validates the string-concatenated comment fields: path,
// body, and suggestion must be strings when present (absent, null, and false
// read as absent, matching jq //). Numbers, booleans, and containers would
// crash the shell's string concatenation, so they refuse here.
func checkCommentScalars(m *jq.Obj) error {
	for _, key := range []string{fieldPath, fieldBody, fieldSuggestion} {
		v, has := m.Get(key)
		if !has || v == nil || v == false {
			continue
		}
		if _, ok := v.(string); !ok {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonStringScalar}
		}
	}
	return nil
}

// checkResolvedShape validates the resolved claim list: an array of strings.
func checkResolvedShape(review *jq.Obj) error {
	items, ok := reviewList(review, "resolved")
	if !ok {
		return &cli.RefusalError{Code: codeBadReviewInput, Message: msgMalformedResolved}
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonStringResolved}
		}
	}
	return nil
}

// checkTriageShape validates the triage claim list: an array of objects.
func checkTriageShape(review *jq.Obj) error {
	items, ok := reviewList(review, "triage")
	if !ok {
		return &cli.RefusalError{Code: codeBadReviewInput, Message: msgMalformedTriage}
	}
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectTriage}
		}
	}
	return nil
}

// checkSectionsShape validates the sections claim list: an array of objects.
func checkSectionsShape(review *jq.Obj) error {
	items, ok := reviewList(review, "sections")
	if !ok {
		return &cli.RefusalError{Code: codeBadReviewInput, Message: msgMalformedSections}
	}
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectSection}
		}
	}
	return nil
}

// checkVerificationShape validates the verification.json claims: an absent or
// unparsable file reads as no claims (matching the shell's silent skip), but
// a present file must hold an array of claim objects.
func checkVerificationShape(dir string) error {
	raw, err := readAgentFile(dir + "/verification.json")
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return ref
		}
		raw, _ = readOptional(dir + "/verification.json")
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
		//nolint:nilerr // undecodable evidence reads as "no claims" (never a refusal), matching the shell
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &cli.RefusalError{Code: codeBadReviewInput, Message: msgMalformedVerifyClaims}
	}
	return checkClaimItems(items)
}

// checkClaimItems validates every verification claim is an object.
func checkClaimItems(items []any) error {
	for _, item := range items {
		if m, ok := item.(*jq.Obj); !ok || m == nil {
			return &cli.RefusalError{Code: codeBadReviewInput, Message: msgNonObjectVerifyClaim}
		}
	}
	return nil
}

// checkPriorShape validates the prior findings store: absent, empty, null,
// false, or a JSON list read as (no) prior; anything else refuses as
// corrupt-store — the run never starts fresh over history. Every present
// element must be an object passing the store shape (entries_guard): exotic
// elements crashed the merge jq with exit 5 and no envelope, so they refuse
// typed here instead (R1). Null reads as no prior for the read verbs, but
// the merge indexes elements by sig, so the write path refuses null too.
func checkPriorShape(dir string) error {
	path := dir + "/findings.json"
	raw, ok := readOptional(path)
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	if doc == nil || doc == false {
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	return checkPriorElements(path, items)
}

// checkPriorElements validates every prior element against the store shape.
func checkPriorElements(path string, items []any) error {
	for _, item := range items {
		if !priorElementOK(item) {
			return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
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
